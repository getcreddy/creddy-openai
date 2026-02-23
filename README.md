# creddy-openai

Creddy plugin for ephemeral OpenAI API keys.

## Overview

This plugin creates and manages ephemeral OpenAI API keys using the OpenAI Admin API. Keys are created on-demand and automatically revoked when they expire or are no longer needed.

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

### Required Settings

| Setting | Description |
|---------|-------------|
| `admin_key` | OpenAI Admin API key with permission to manage API keys |

## Scopes

| Pattern | Description |
|---------|-------------|
| `openai` | Full access to the OpenAI API |
| `openai:gpt` | Access to GPT models (chat completions) |
| `openai:dall-e` | Access to DALL-E image generation |
| `openai:whisper` | Access to Whisper audio transcription |

> **Note:** OpenAI API keys currently provide full API access. Scopes are informational and used for policy enforcement within Creddy. Future OpenAI API updates may support scoped keys natively.

## Usage

```bash
# Get a full-access key
creddy get openai --scope "openai"

# Get a key scoped for GPT usage
creddy get openai --scope "openai:gpt"

# Get a key for image generation
creddy get openai --scope "openai:dall-e"
```

## Development

### Standalone Testing

The plugin can run standalone for testing without Creddy:

```bash
# Build
make build

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

# Get a credential
make get CONFIG=test-config.json SCOPE="openai"
```

### Dev Mode

Auto-rebuild and install on file changes:

```bash
make dev
```

### Testing

```bash
make test
```

## How It Works

1. Plugin uses the OpenAI Admin API to create a new API key
2. Key ID is stored as ExternalID for later revocation
3. Creddy manages TTL and revokes the key when it expires
4. On revocation, the key is deleted via the Admin API

## Requirements

- OpenAI account with Admin API access
- Admin API key with `api_keys:write` permission

## License

Apache 2.0
