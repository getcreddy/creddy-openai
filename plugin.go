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
	PluginVersion = "0.2.0"
	BaseURL       = "https://api.openai.com/v1"
)

// OpenAIPlugin implements the Creddy Plugin interface for OpenAI
type OpenAIPlugin struct {
	config *OpenAIConfig
}

// OpenAIConfig contains the plugin configuration
type OpenAIConfig struct {
	AdminKey  string `json:"admin_key"`  // Admin API key (sk-admin-...)
	ProjectID string `json:"project_id"` // Project ID to create service accounts in
}

// serviceAccountResponse represents the response from creating a service account
type serviceAccountResponse struct {
	ID        string                   `json:"id"`
	Object    string                   `json:"object"`
	Name      string                   `json:"name"`
	Role      string                   `json:"role"`
	CreatedAt int64                    `json:"created_at"`
	APIKey    *serviceAccountAPIKey    `json:"api_key,omitempty"`
}

// serviceAccountAPIKey represents the API key returned when creating a service account
type serviceAccountAPIKey struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Name      string `json:"name"`
	Value     string `json:"value"` // The actual key - only returned on creation
	CreatedAt int64  `json:"created_at"`
}

// projectListResponse represents the list projects response
type projectListResponse struct {
	Object  string    `json:"object"`
	Data    []project `json:"data"`
	HasMore bool      `json:"has_more"`
}

// project represents an OpenAI project
type project struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
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

func (p *OpenAIPlugin) ConfigSchema(ctx context.Context) ([]sdk.ConfigField, error) {
	return []sdk.ConfigField{
		{
			Name:        "admin_key",
			Type:        "secret",
			Description: "OpenAI Admin API key (sk-admin-...). Create at https://platform.openai.com/settings/organization/admin-keys",
			Required:    true,
		},
		{
			Name:        "project_id",
			Type:        "string",
			Description: "Project ID to create service accounts in. If empty, uses the default project.",
			Required:    false,
		},
	}, nil
}

func (p *OpenAIPlugin) Constraints(ctx context.Context) (*sdk.Constraints, error) {
	// OpenAI API keys don't have inherent TTL limits - Creddy manages expiration via revocation
	return nil, nil
}

func (p *OpenAIPlugin) Configure(ctx context.Context, configJSON string) error {
	var config OpenAIConfig
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}

	if config.AdminKey == "" {
		return fmt.Errorf("admin_key is required")
	}

	if !strings.HasPrefix(config.AdminKey, "sk-admin-") {
		return fmt.Errorf("admin_key must be an Admin API key (starts with sk-admin-). Create one at https://platform.openai.com/settings/organization/admin-keys")
	}

	p.config = &config
	return nil
}

func (p *OpenAIPlugin) Validate(ctx context.Context) error {
	if p.config == nil {
		return fmt.Errorf("plugin not configured")
	}

	// Test the admin key by listing projects
	projects, err := p.listProjects(ctx)
	if err != nil {
		return fmt.Errorf("failed to validate admin key: %w", err)
	}

	// If no project_id configured, find the default project
	if p.config.ProjectID == "" {
		if len(projects) == 0 {
			return fmt.Errorf("no projects found in organization")
		}
		// Use first active project as default
		for _, proj := range projects {
			if proj.Status == "active" {
				p.config.ProjectID = proj.ID
				break
			}
		}
		if p.config.ProjectID == "" {
			return fmt.Errorf("no active projects found in organization")
		}
	}

	return nil
}

func (p *OpenAIPlugin) GetCredential(ctx context.Context, req *sdk.CredentialRequest) (*sdk.Credential, error) {
	if p.config == nil {
		return nil, fmt.Errorf("plugin not configured")
	}

	// Create a new service account with a unique name
	name := fmt.Sprintf("creddy-%s-%d", req.Agent.Name, time.Now().UnixNano())

	sa, err := p.createServiceAccount(ctx, name)
	if err != nil {
		return nil, err
	}

	if sa.APIKey == nil || sa.APIKey.Value == "" {
		return nil, fmt.Errorf("service account created but no API key returned")
	}

	// Store service account ID as ExternalID for revocation
	return &sdk.Credential{
		Value:      sa.APIKey.Value,
		ExternalID: sa.ID, // Service account ID, not key ID
		ExpiresAt:  time.Now().Add(req.TTL),
		Metadata: map[string]string{
			"service_account_id":   sa.ID,
			"service_account_name": name,
			"api_key_id":           sa.APIKey.ID,
			"project_id":           p.config.ProjectID,
			"scope":                req.Scope,
		},
	}, nil
}

func (p *OpenAIPlugin) RevokeCredential(ctx context.Context, externalID string) error {
	if p.config == nil {
		return fmt.Errorf("plugin not configured")
	}

	// externalID is the service account ID
	return p.deleteServiceAccount(ctx, externalID)
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

func (p *OpenAIPlugin) listProjects(ctx context.Context) ([]project, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", BaseURL+"/organization/projects", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+p.config.AdminKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("invalid admin key")
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("admin key does not have permission to manage projects")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API error (%d): %s", resp.StatusCode, string(body))
	}

	var listResp projectListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return listResp.Data, nil
}

func (p *OpenAIPlugin) createServiceAccount(ctx context.Context, name string) (*serviceAccountResponse, error) {
	reqBody, _ := json.Marshal(map[string]interface{}{
		"name": name,
	})

	url := fmt.Sprintf("%s/organization/projects/%s/service_accounts", BaseURL, p.config.ProjectID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+p.config.AdminKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create service account: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("OpenAI API error (%d): %s", resp.StatusCode, string(body))
	}

	var sa serviceAccountResponse
	if err := json.Unmarshal(body, &sa); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &sa, nil
}

func (p *OpenAIPlugin) deleteServiceAccount(ctx context.Context, serviceAccountID string) error {
	if p.config.ProjectID == "" {
		return fmt.Errorf("project_id not set - cannot delete service account")
	}

	url := fmt.Sprintf("%s/organization/projects/%s/service_accounts/%s", BaseURL, p.config.ProjectID, serviceAccountID)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+p.config.AdminKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete service account: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Accept 200, 204, or 404 (already deleted) as success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("OpenAI API error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}
