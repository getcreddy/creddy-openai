# creddy-openai

Creddy plugin for ephemeral OpenAI API keys.

## Overview

This plugin creates and manages ephemeral OpenAI API keys using the OpenAI Admin API. Keys are created as service accounts on-demand and automatically revoked when they expire or are no longer needed.

## Prerequisites

### Create an Admin API Key

1. Go to [platform.openai.com/settings/organization/admin-keys](https://platform.openai.com/settings/organization/admin-keys)
2. Click **Create admin key**
3. Give it a name (e.g., "creddy")
4. Copy the key — it starts with `sk-admin-`

> **Important:** Admin API keys are different from regular API keys. They can only be created by organization owners and are used for administrative operations like managing service accounts.

## Installation

```bash
creddy plugin install openai
```

Or build from source:

```bash
make build
make install  # copies to ~/.creddy/plugins/
```

## Configuration

Add the OpenAI backend to Creddy:

```bash
creddy backend add openai --admin-key "sk-admin-..."
```

### Settings

| Setting | Required | Description |
|---------|----------|-------------|
| `admin_key` | Yes | OpenAI Admin API key (`sk-admin-...`) |
| `project_id` | No | Project ID for service accounts. Defaults to first active project. |

## Scopes

| Pattern | Description |
|---------|-------------|
| `openai` | Full access to the OpenAI API |
| `openai:gpt` | Access to GPT models (chat completions) |
| `openai:dall-e` | Access to DALL-E image generation |
| `openai:whisper` | Access to Whisper audio transcription |

> **Note:** OpenAI API keys currently provide full API access. Scopes are informational and used for policy enforcement within Creddy.

## Usage

```bash
# Get a full-access key
creddy get openai --scope "openai"

# Get a key scoped for GPT usage
creddy get openai --scope "openai:gpt"

# Get a key for image generation
creddy get openai --scope "openai:dall-e"
```

## How It Works

1. Agent requests a credential with a TTL
2. Plugin creates a new service account via Admin API
3. Service account creation returns an API key (`sk-svcacct-...`)
4. Creddy tracks the TTL and service account ID
5. On expiration, plugin deletes the service account
6. Key is immediately invalidated

### API Endpoints Used

```
GET  /v1/organization/projects                                    # List projects
POST /v1/organization/projects/{project_id}/service_accounts      # Create service account + key
DELETE /v1/organization/projects/{project_id}/service_accounts/{id}  # Revoke key
```

## Development

### Building

```bash
make build
```

### Standalone Testing

The plugin can run standalone for testing without Creddy:

```bash
# Show plugin info
make info

# List supported scopes
make scopes

# Test with a config file
echo '{
  "admin_key": "sk-admin-..."
}' > test-config.json

# Validate configuration
make validate CONFIG=test-config.json

# Get a credential (creates service account, returns key)
make get CONFIG=test-config.json SCOPE="openai"
```

### Integration Tests

Run the integration test suite (requires `OPENAI_ADMIN_KEY` env var):

```bash
# Run all integration tests
make integration-test

# Or directly
OPENAI_ADMIN_KEY=sk-admin-... go test -v -tags=integration ./...
```

### Dev Mode

Auto-rebuild and install on file changes:

```bash
make dev
```

## Requirements

- OpenAI organization account
- Admin API key (organization owner required to create)

## License

Apache 2.0
