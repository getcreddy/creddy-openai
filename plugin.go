package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	sdk "github.com/getcreddy/creddy-plugin-sdk"
)

const (
	PluginName    = "openai"
	PluginVersion = "0.1.0"
)

// OpenAIPlugin implements the Creddy Plugin interface for OpenAI
type OpenAIPlugin struct {
	config *OpenAIConfig
}

// OpenAIConfig contains the plugin configuration
type OpenAIConfig struct {
	AdminKey string `json:"admin_key"` // Admin API key for managing keys
}

// openAIAPIKey represents an API key returned by the Admin API
type openAIAPIKey struct {
	Object    string `json:"object"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Key       string `json:"key"` // Only returned on creation (redacted otherwise)
	CreatedAt int64  `json:"created_at"`
}

// openAIKeyListResponse represents the list keys response
type openAIKeyListResponse struct {
	Object string         `json:"object"`
	Data   []openAIAPIKey `json:"data"`
}

func (p *OpenAIPlugin) Info(ctx context.Context) (*sdk.PluginInfo, error) {
	return &sdk.PluginInfo{
		Name:             PluginName,
		Version:          PluginVersion,
		Description:      "Ephemeral OpenAI API keys via Admin API",
		MinCreddyVersion: "0.4.0",
	}, nil
}

func (p *OpenAIPlugin) Scopes(ctx context.Context) ([]sdk.ScopeSpec, error) {
	return []sdk.ScopeSpec{
		{
			Pattern:     "openai",
			Description: "Full access to the OpenAI API",
			Examples:    []string{"openai"},
		},
		{
			Pattern:     "openai:gpt",
			Description: "Access to GPT models (chat completions)",
			Examples:    []string{"openai:gpt"},
		},
		{
			Pattern:     "openai:dall-e",
			Description: "Access to DALL-E image generation",
			Examples:    []string{"openai:dall-e"},
		},
		{
			Pattern:     "openai:whisper",
			Description: "Access to Whisper audio transcription",
			Examples:    []string{"openai:whisper"},
		},
	}, nil
}

func (p *OpenAIPlugin) Configure(ctx context.Context, configJSON string) error {
	var config OpenAIConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}

	if config.AdminKey == "" {
		return fmt.Errorf("admin_key is required")
	}

	p.config = &config
	return nil
}

func (p *OpenAIPlugin) Validate(ctx context.Context) error {
	if p.config == nil {
		return fmt.Errorf("plugin not configured")
	}

	// Test the admin key by listing existing keys
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.openai.com/v1/organization/api_keys", nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+p.config.AdminKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to validate admin key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid admin key")
	}
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("admin key does not have permission to manage API keys")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai API error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (p *OpenAIPlugin) GetCredential(ctx context.Context, req *sdk.CredentialRequest) (*sdk.Credential, error) {
	if p.config == nil {
		return nil, fmt.Errorf("plugin not configured")
	}

	// Create a new API key with a unique name
	name := fmt.Sprintf("creddy-%s-%d", req.Agent.Name, time.Now().UnixNano())

	apiKey, err := p.createAPIKey(ctx, name)
	if err != nil {
		return nil, err
	}

	// OpenAI keys don't have inherent expiry - Creddy manages TTL
	// The key ID is stored as ExternalID for revocation
	return &sdk.Credential{
		Value:      apiKey.Key,
		ExternalID: apiKey.ID,
		ExpiresAt:  time.Now().Add(req.TTL),
		Metadata: map[string]string{
			"key_id":   apiKey.ID,
			"key_name": name,
			"scope":    req.Scope,
		},
	}, nil
}

func (p *OpenAIPlugin) RevokeCredential(ctx context.Context, externalID string) error {
	if p.config == nil {
		return fmt.Errorf("plugin not configured")
	}

	return p.deleteAPIKey(ctx, externalID)
}

func (p *OpenAIPlugin) MatchScope(ctx context.Context, scope string) (bool, error) {
	// Match "openai" or "openai:*"
	if scope == "openai" {
		return true, nil
	}
	if !strings.HasPrefix(scope, "openai:") {
		return false, nil
	}

	// Validate known sub-scopes
	subScope := strings.TrimPrefix(scope, "openai:")
	validScopes := map[string]bool{
		"gpt":     true,
		"dall-e":  true,
		"whisper": true,
	}

	return validScopes[subScope], nil
}

// --- OpenAI Admin API helpers ---

func (p *OpenAIPlugin) createAPIKey(ctx context.Context, name string) (*openAIAPIKey, error) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"name": name,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/organization/api_keys", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+p.config.AdminKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create API key: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("openai API error (%d): %s", resp.StatusCode, string(body))
	}

	var key openAIAPIKey
	if err := json.Unmarshal(body, &key); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &key, nil
}

func (p *OpenAIPlugin) deleteAPIKey(ctx context.Context, keyID string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", "https://api.openai.com/v1/organization/api_keys/"+keyID, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+p.config.AdminKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete API key: %w", err)
	}
	defer resp.Body.Close()

	// Accept 200, 204, or 404 (already deleted) as success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai API error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}
