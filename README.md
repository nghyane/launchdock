# launchdock

[![CI](https://github.com/nghyane/launchdock/actions/workflows/ci.yml/badge.svg)](https://github.com/nghyane/launchdock/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/nghyane/launchdock)](https://github.com/nghyane/launchdock/releases/latest)
[![GitHub stars](https://img.shields.io/github/stars/nghyane/launchdock)](https://github.com/nghyane/launchdock/stargazers)

Use Claude Max or ChatGPT across AI coding tools.

`launchdock` turns the accounts you already have into one local endpoint for tools like OpenCode, Codex, Claude Code, Droid, and Pi.

Why people use it:

- use `opencode` or `codex` even if you only have Claude Max or ChatGPT auth
- avoid managing separate API keys everywhere
- log in once and reuse that account across multiple tools
- push your managed auth to a personal server and keep it running 24/7

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/nghyane/launchdock/main/install.sh | sh
launchdock version
```

Optional:

```bash
curl -fsSL https://raw.githubusercontent.com/nghyane/launchdock/main/install.sh | LAUNCHDOCK_VERSION=v0.1.1 sh
curl -fsSL https://raw.githubusercontent.com/nghyane/launchdock/main/install.sh | INSTALL_DIR=/usr/local/bin sh
```

The installer downloads the right GitHub Release, verifies its checksum, and installs `launchdock` into `~/.local/bin` by default.

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

## Commands

```bash
launchdock auth
launchdock auth list
launchdock auth login claude [label]
launchdock auth login openai
launchdock auth push <ssh-target> [credential-id ...]
launchdock auth remove <credential-id>

launchdock launch [tool]
launchdock start | ps | logs | restart | stop
launchdock update
launchdock version
```

## Technical note

`launchdock` runs a local runtime on `http://localhost:8090` and exposes:

- `/v1/chat/completions`
- `/v1/messages`
- `/v1/responses`
- `/v1/models`
- `/health`

State lives in:

- `~/.launchdock/launchdock.pid`
- `~/.launchdock/launchdock.log`
- `~/.config/launchdock/config.json`

Legacy `llm-mux` code is preserved on `legacy/llm-mux`.
