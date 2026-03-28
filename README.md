# launchdock

[![CI](https://github.com/nghyane/launchdock/actions/workflows/ci.yml/badge.svg)](https://github.com/nghyane/launchdock/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/nghyane/launchdock)](https://github.com/nghyane/launchdock/releases/latest)
[![GitHub stars](https://img.shields.io/github/stars/nghyane/launchdock)](https://github.com/nghyane/launchdock/stargazers)

Use Claude Max or ChatGPT across AI coding tools.

`launchdock` gives you one local endpoint for tools like OpenCode, Codex, Claude Code, Droid, and Pi.

Why people use it:

- use OpenCode or Codex with the accounts you already have
- avoid managing separate API keys everywhere
- log in once and reuse that auth across multiple tools
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
launchdock launch opencode
```

`launchdock launch <tool>` checks credentials, starts the local runtime if needed, writes tool config when required, and launches the tool.

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

## Compatibility

`launchdock` is tested with the official OpenAI SDK and works across both Claude and GPT models.

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
