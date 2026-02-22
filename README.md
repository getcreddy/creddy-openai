# creddy-openai

Creddy plugin for OpenAI API keys with project scoping.

## Installation

```bash
creddy plugin install openai
```

## Configuration

```bash
creddy backend add openai \
  --admin-key "sk-..." \
  --org-id "org-..." \        # optional
  --project-id "proj-..."     # optional default project
```

## Scopes

| Scope | Description |
|-------|-------------|
| `openai:*` | Full API access |
| `openai:<project_id>` | Access scoped to specific project |

## Usage

```bash
# Get an API key
export OPENAI_API_KEY=$(creddy get openai)

# Project-scoped key
export OPENAI_API_KEY=$(creddy get openai --scope "openai:proj_abc123")
```

## Requirements

- OpenAI API key with admin permissions (ability to create/delete API keys)
- Organization admin access for project-scoped keys

## License

Apache 2.0
