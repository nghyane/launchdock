# launchdock

[![CI](https://github.com/nghyane/launchdock/actions/workflows/ci.yml/badge.svg)](https://github.com/nghyane/launchdock/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/nghyane/launchdock)](https://github.com/nghyane/launchdock/releases/latest)
[![GitHub stars](https://img.shields.io/github/stars/nghyane/launchdock)](https://github.com/nghyane/launchdock/stargazers)

Use Claude and GPT across AI coding tools through one local gateway, with both OpenAI-compatible and Claude-native API surfaces.

`launchdock` gives you one local endpoint for tools like OpenCode, Codex, Claude Code, Droid, and Pi.

Why people use it:

- use OpenCode or Codex with the accounts you already have
- use Claude through either OpenAI-compatible chat clients or the native Claude Messages API
- avoid managing separate credentials and configs everywhere
- log in once and reuse managed auth across multiple tools
- push managed auth to a personal server when needed

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/nghyane/launchdock/main/install.sh | sh
launchdock version
```

Optional:

```bash
curl -fsSL https://raw.githubusercontent.com/nghyane/launchdock/main/install.sh | env LAUNCHDOCK_VERSION=v0.1.1 sh
curl -fsSL https://raw.githubusercontent.com/nghyane/launchdock/main/install.sh | env INSTALL_DIR=/usr/local/bin sh
```

## Quickstart

```bash
launchdock auth login claude
launchdock auth login openai

launchdock auth list
launchdock start
launchdock launch opencode --config
```

`launchdock launch <tool>` checks credentials, starts the local runtime if needed, writes tool config when required, and launches the tool.

Check the local API:

```bash
curl http://localhost:8090/v1/models
```

## Authentication

Supported auth sources:

- Claude: `launchdock auth login claude`
- OpenAI/Codex: `launchdock auth login openai`

Why this auth model is useful:

- sign in once through the native Claude/OpenAI login flow
- reuse the same managed auth across local tools and personal servers
- avoid copying API keys into multiple configs or shell environments
- keep the workflow aligned with Claude Code and Codex account-based usage

## Personal server

```bash
launchdock auth push user@server.example.com
ssh user@server.example.com '$HOME/.local/bin/launchdock start'
```

`auth push` installs or updates `launchdock` on the remote host automatically, then imports your managed credentials.

## Supported tools

- `claude-code`
- `codex`
- `opencode`
- `droid`
- `pi`

## Models

`launchdock` exposes both Claude and GPT models from one local provider.

Claude thinking aliases are also available:

- `claude-sonnet-4-6-thinking`
- `claude-opus-4-6-thinking`

These aliases automatically enable Claude thinking for OpenAI-compatible clients that do not serialize Claude thinking config reliably.

## Compatibility

`launchdock` is tested with the official OpenAI Python SDK and works across both Claude and GPT models.

Validated locally with `claude-opus-4-6` and `gpt-5.4` for:

- basic chat
- multi-turn conversations
- streaming
- tool calls
- tool result round-trips

Supported API surfaces:

- `/v1/chat/completions`
- `/v1/responses`
- `/v1/messages`

### Support matrix

| Endpoint | GPT | Claude | Semantics | Tools |
|---|---|---|---|---|
| `/v1/chat/completions` | ✅ | ✅ | OpenAI-compatible chat | ✅ |
| `/v1/responses` | ✅ | ✅ | Native OpenAI Responses semantics | ✅ |
| `/v1/messages` | — | ✅ | Native Claude Messages format | ✅ |

### Endpoint semantics

#### `/v1/chat/completions`

- standard OpenAI-compatible chat chunks
- reasoning/thinking is exposed through compatibility fields such as `choices[].delta.reasoning_content`
- best default for broad client compatibility across GPT and Claude

#### `/v1/responses`

- native OpenAI Responses semantics
- reasoning is exposed through native events/items such as:
  - `response.reasoning_summary_text.delta`
  - `reasoning` output items
  - `function_call` items/events
- preferred endpoint for the richest reasoning lifecycle

#### `/v1/messages`

- native Claude Messages request/response format
- useful for Claude-native clients or integrations that expect Anthropic-style payloads
- uses the same managed Claude account auth as the OpenAI-compatible surfaces

### Tool naming

Claude OAuth internally prefixes tool names with `mcp_` when talking to Anthropic.

`launchdock` strips that prefix before returning tool/function names to clients, so clients still see the original tool name (for example `get_weather`).

### OpenCode note

`launchdock launch opencode --config` merges into an existing OpenCode config instead of overwriting unrelated keys.

For Claude thinking in OpenCode, prefer the model aliases:

- `claude-sonnet-4-6-thinking`
- `claude-opus-4-6-thinking`

These are more reliable than trying to rely on client-side Claude thinking config through a custom OpenAI-compatible provider.

## Commands

```bash
launchdock auth
launchdock launch [tool]
launchdock start | ps | logs | restart | stop
launchdock update
```

## Technical note

`launchdock` runs on `http://localhost:8090`.

State lives in:

- `~/.launchdock/launchdock.pid`
- `~/.launchdock/launchdock.log`
- `~/.config/launchdock/config.json`

Legacy `llm-mux` code is preserved on `legacy/llm-mux`.
