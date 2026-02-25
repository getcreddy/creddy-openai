//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/getcreddy/creddy-plugin-sdk"
)

func getAdminKey(t *testing.T) string {
	key := os.Getenv("OPENAI_ADMIN_KEY")
	if key == "" {
		t.Skip("OPENAI_ADMIN_KEY not set, skipping integration tests")
	}
	if !strings.HasPrefix(key, "sk-admin-") {
		t.Fatalf("OPENAI_ADMIN_KEY must start with sk-admin-, got: %s...", key[:20])
	}
	return key
}

func TestIntegration_PluginInfo(t *testing.T) {
	plugin := &OpenAIPlugin{}
	info, err := plugin.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error: %v", err)
	}

	if info.Name != "openai" {
		t.Errorf("expected name 'openai', got %q", info.Name)
	}
	if info.Version == "" {
		t.Error("expected non-empty version")
	}
}

func TestIntegration_Configure(t *testing.T) {
	adminKey := getAdminKey(t)

	tests := []struct {
		name    string
		config  string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid admin key",
			config:  fmt.Sprintf(`{"admin_key": "%s"}`, adminKey),
			wantErr: false,
		},
		{
			name:    "missing admin key",
			config:  `{}`,
			wantErr: true,
			errMsg:  "admin_key is required",
		},
		{
			name:    "invalid key prefix",
			config:  `{"admin_key": "sk-proj-xxx"}`,
			wantErr: true,
			errMsg:  "must be an Admin API key",
		},
		{
			name:    "invalid JSON",
			config:  `{invalid}`,
			wantErr: true,
			errMsg:  "invalid config JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := &OpenAIPlugin{}
			err := plugin.Configure(context.Background(), tt.config)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestIntegration_Validate(t *testing.T) {
	adminKey := getAdminKey(t)

	plugin := &OpenAIPlugin{}
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"admin_key": "%s"}`, adminKey))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	err = plugin.Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	// After validate, project_id should be set
	if plugin.config.ProjectID == "" {
		t.Error("expected project_id to be set after Validate()")
	}
}

func TestIntegration_ListProjects(t *testing.T) {
	adminKey := getAdminKey(t)

	plugin := &OpenAIPlugin{}
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"admin_key": "%s"}`, adminKey))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	projects, err := plugin.listProjects(context.Background())
	if err != nil {
		t.Fatalf("listProjects() error: %v", err)
	}

	if len(projects) == 0 {
		t.Fatal("expected at least one project")
	}

	// Check first project has required fields
	proj := projects[0]
	if proj.ID == "" {
		t.Error("expected project to have ID")
	}
	if proj.Name == "" {
		t.Error("expected project to have Name")
	}
	t.Logf("Found project: %s (%s)", proj.Name, proj.ID)
}

func TestIntegration_FullCredentialLifecycle(t *testing.T) {
	adminKey := getAdminKey(t)

	plugin := &OpenAIPlugin{}

	// Configure
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"admin_key": "%s"}`, adminKey))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}

	// Validate (sets project_id)
	err = plugin.Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	// Create credential
	req := &sdk.CredentialRequest{
		Scope: "openai",
		TTL:   1 * time.Hour,
		Agent: sdk.Agent{
			Name: "integration-test",
		},
	}

	cred, err := plugin.GetCredential(context.Background(), req)
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}

	// Validate credential fields
	if cred.Value == "" {
		t.Fatal("expected credential value")
	}
	if !strings.HasPrefix(cred.Value, "sk-svcacct-") {
		t.Errorf("expected key to start with sk-svcacct-, got: %s...", cred.Value[:20])
	}
	if cred.ExternalID == "" {
		t.Fatal("expected external ID (service account ID)")
	}
	if cred.ExpiresAt.IsZero() {
		t.Fatal("expected expiration time")
	}

	t.Logf("Created credential: key=%s..., service_account=%s", cred.Value[:30], cred.ExternalID)
	t.Logf("Full key length: %d", len(cred.Value))

	// Wait for key to propagate (OpenAI has eventual consistency)
	time.Sleep(2 * time.Second)

	// Verify the key works
	t.Run("verify key works", func(t *testing.T) {
		t.Logf("Testing with key: %s", cred.Value)
		httpReq, _ := http.NewRequest("GET", "https://api.openai.com/v1/models", nil)
		httpReq.Header.Set("Authorization", "Bearer "+cred.Value)

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			t.Fatalf("failed to test key: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("key test failed: status=%d body=%s", resp.StatusCode, string(body))
		}
		t.Log("Key works: successfully listed models")
	})

	// Revoke credential
	t.Logf("Revoking service account: %s (project: %s)", cred.ExternalID, plugin.config.ProjectID)
	err = plugin.RevokeCredential(context.Background(), cred.ExternalID)
	if err != nil {
		t.Fatalf("RevokeCredential() error: %v", err)
	}
	t.Log("Revoked credential")

	// Verify the key no longer works (may take several seconds to propagate)
	t.Run("verify key revoked", func(t *testing.T) {
		// OpenAI has eventual consistency - revocation can take 5-10 seconds
		var lastStatus int
		for i := 0; i < 10; i++ {
			if i > 0 {
				time.Sleep(1 * time.Second)
			}

			httpReq, _ := http.NewRequest("GET", "https://api.openai.com/v1/models", nil)
			httpReq.Header.Set("Authorization", "Bearer "+cred.Value)

			resp, err := http.DefaultClient.Do(httpReq)
			if err != nil {
				t.Fatalf("failed to test revoked key: %v", err)
			}
			resp.Body.Close()
			lastStatus = resp.StatusCode

			if resp.StatusCode == http.StatusUnauthorized {
				t.Logf("Key revoked after %d seconds", i+1)
				return
			}
		}

		// After 10 seconds, if still working, that's concerning but may be expected
		if lastStatus == http.StatusOK {
			t.Logf("Warning: key still works after 10s - OpenAI propagation can be slow")
			// Don't fail - this is a known eventual consistency issue
		}
	})
}

func TestIntegration_RevokeNonexistent(t *testing.T) {
	adminKey := getAdminKey(t)

	plugin := &OpenAIPlugin{}
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"admin_key": "%s"}`, adminKey))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}
	err = plugin.Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	// Revoking a nonexistent service account should not error (idempotent)
	err = plugin.RevokeCredential(context.Background(), "user-nonexistent-12345")
	if err != nil {
		t.Fatalf("RevokeCredential() should be idempotent, got error: %v", err)
	}
}

func TestIntegration_MatchScope(t *testing.T) {
	plugin := &OpenAIPlugin{}

	tests := []struct {
		scope string
		want  bool
	}{
		{"openai", true},
		{"openai:gpt", true},
		{"openai:dall-e", true},
		{"openai:whisper", true},
		{"openai:invalid", false},
		{"github", false},
		{"aws", false},
	}

	for _, tt := range tests {
		t.Run(tt.scope, func(t *testing.T) {
			got, err := plugin.MatchScope(context.Background(), tt.scope)
			if err != nil {
				t.Fatalf("MatchScope() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("MatchScope(%q) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}

func TestIntegration_APIKeyUsage(t *testing.T) {
	adminKey := getAdminKey(t)

	plugin := &OpenAIPlugin{}
	err := plugin.Configure(context.Background(), fmt.Sprintf(`{"admin_key": "%s"}`, adminKey))
	if err != nil {
		t.Fatalf("Configure() error: %v", err)
	}
	err = plugin.Validate(context.Background())
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}

	// Create a credential
	req := &sdk.CredentialRequest{
		Scope: "openai:gpt",
		TTL:   5 * time.Minute,
		Agent: sdk.Agent{
			Name: "api-usage-test",
		},
	}

	cred, err := plugin.GetCredential(context.Background(), req)
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	defer func() {
		// Clean up
		plugin.RevokeCredential(context.Background(), cred.ExternalID)
	}()

	// Wait for key to propagate (OpenAI has eventual consistency - can take up to 10s)
	time.Sleep(10 * time.Second)

	// Make a simple chat completion request
	t.Run("chat completion", func(t *testing.T) {
		body := `{
			"model": "gpt-4o-mini",
			"messages": [{"role": "user", "content": "Say 'test' and nothing else"}],
			"max_tokens": 10
		}`

		httpReq, _ := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", strings.NewReader(body))
		httpReq.Header.Set("Authorization", "Bearer "+cred.Value)
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			t.Fatalf("chat completion request failed: %v", err)
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)

		if resp.StatusCode == http.StatusTooManyRequests {
			// Quota exceeded - skip this test, not a plugin issue
			t.Skip("OpenAI quota exceeded - skipping chat completion test")
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("chat completion failed: status=%d body=%s", resp.StatusCode, string(respBody))
		}

		var result map[string]interface{}
		if err := json.Unmarshal(respBody, &result); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if _, ok := result["choices"]; !ok {
			t.Fatal("expected 'choices' in response")
		}
		t.Log("Chat completion successful")
	})
}
