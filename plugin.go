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
	OpenAIAPIBase = "https://api.openai.com/v1"
)

// OpenAIPlugin implements the Creddy Plugin interface for OpenAI
type OpenAIPlugin struct {
	config *OpenAIConfig
}

// OpenAIConfig contains the plugin configuration
type OpenAIConfig struct {
	// AdminKey is an API key with admin permissions to create/delete keys
	AdminKey string `json:"admin_key"`
	// OrgID is the organization ID (optional, for org-scoped operations)
	OrgID string `json:"org_id,omitempty"`
	// ProjectID is the default project ID for scoped keys
	ProjectID string `json:"project_id,omitempty"`
}

func (p *OpenAIPlugin) Info(ctx context.Context) (*sdk.PluginInfo, error) {
	return &sdk.PluginInfo{
		Name:             PluginName,
		Version:          PluginVersion,
		Description:      "OpenAI API keys with project scoping",
		MinCreddyVersion: "0.4.0",
	}, nil
}

func (p *OpenAIPlugin) Scopes(ctx context.Context) ([]sdk.ScopeSpec, error) {
	return []sdk.ScopeSpec{
		{
			Pattern:     "openai:*",
			Description: "Full API access",
			Examples:    []string{"openai:*"},
		},
		{
			Pattern:     "openai:<project>",
			Description: "Access scoped to a specific project",
			Examples:    []string{"openai:proj_abc123"},
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

	if !strings.HasPrefix(config.AdminKey, "sk-") {
		return fmt.Errorf("admin_key must be a valid OpenAI API key (starts with sk-)")
	}

	p.config = &config
	return nil
}

func (p *OpenAIPlugin) Validate(ctx context.Context) error {
	if p.config == nil {
		return fmt.Errorf("plugin not configured")
	}

	// Test the API key by listing models
	req, err := http.NewRequestWithContext(ctx, "GET", OpenAIAPIBase+"/models", nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+p.config.AdminKey)
	if p.config.OrgID != "" {
		req.Header.Set("OpenAI-Organization", p.config.OrgID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to OpenAI: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OpenAI API error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (p *OpenAIPlugin) GetCredential(ctx context.Context, req *sdk.CredentialRequest) (*sdk.Credential, error) {
	if p.config == nil {
		return nil, fmt.Errorf("plugin not configured")
	}

	// Parse scope for project ID
	projectID := p.config.ProjectID
	if strings.HasPrefix(req.Scope, "openai:") && req.Scope != "openai:*" {
		projectID = strings.TrimPrefix(req.Scope, "openai:")
	}

	// Create a new API key
	keyName := fmt.Sprintf("creddy-%s-%d", req.Agent.Name, time.Now().Unix())
	
	apiKey, keyID, err := p.createAPIKey(ctx, keyName, projectID)
	if err != nil {
		return nil, err
	}

	// Calculate expiry (OpenAI keys don't have native TTL, we track it ourselves)
	expiresAt := time.Now().Add(req.TTL)

	return &sdk.Credential{
		Value:      apiKey,
		ExpiresAt:  expiresAt,
		ExternalID: keyID,
		Metadata: map[string]string{
			"key_name":   keyName,
			"project_id": projectID,
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
	return strings.HasPrefix(scope, "openai:"), nil
}

// createAPIKey creates a new API key via the OpenAI Admin API
func (p *OpenAIPlugin) createAPIKey(ctx context.Context, name, projectID string) (string, string, error) {
	// OpenAI's API key management endpoint
	// Note: This uses the organization API keys endpoint
	url := "https://api.openai.com/v1/organization/api_keys"

	reqBody := map[string]interface{}{
		"name": name,
	}
	if projectID != "" {
		reqBody["project_id"] = projectID
	}

	bodyJSON, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", "", err
	}

	req.Header.Set("Authorization", "Bearer "+p.config.AdminKey)
	req.Header.Set("Content-Type", "application/json")
	if p.config.OrgID != "" {
		req.Header.Set("OpenAI-Organization", p.config.OrgID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to create API key: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("OpenAI API error (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		Secret string `json:"secret"` // Some API versions use this
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("failed to parse response: %w", err)
	}

	apiKey := result.Key
	if apiKey == "" {
		apiKey = result.Secret
	}

	return apiKey, result.ID, nil
}

// deleteAPIKey deletes an API key
func (p *OpenAIPlugin) deleteAPIKey(ctx context.Context, keyID string) error {
	url := fmt.Sprintf("https://api.openai.com/v1/organization/api_keys/%s", keyID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+p.config.AdminKey)
	if p.config.OrgID != "" {
		req.Header.Set("OpenAI-Organization", p.config.OrgID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete API key: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OpenAI API error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}
