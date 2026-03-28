# launchdock

Use your Claude Max or ChatGPT account across AI coding tools.

`launchdock` lets you log in once and use that account across tools like OpenCode, Codex, Claude Code, Droid, and Pi. It gives you one local endpoint, one place to manage auth, and one fast setup flow for your personal AI stack.

## Why People Use It

People reach for `launchdock` when they want to:

- use `opencode`, `codex`, or other tools even though they only have a Claude Max or ChatGPT account
- reuse the accounts they already pay for instead of managing separate API keys everywhere
- get one local endpoint for personal AI tools and services
- log in once and launch many tools quickly
- spread traffic across multiple accounts to reduce rate-limit pain

In short: one login, many tools, one local endpoint.

## What You Get

- one account can work across multiple AI coding tools
- one local runtime on `localhost:8090`
- one auth surface for Claude and OpenAI
- one launch flow that writes config and starts the runtime automatically
- support for multiple discovered accounts and credential rotation

## Install

### Local install

```bash
curl -fsSL https://raw.githubusercontent.com/nghyane/launchdock/main/install.sh | sh
launchdock version
```

Optional:

```bash
# install a specific version
curl -fsSL https://raw.githubusercontent.com/nghyane/launchdock/main/install.sh | LAUNCHDOCK_VERSION=v0.1.0 sh

# install into a custom directory
curl -fsSL https://raw.githubusercontent.com/nghyane/launchdock/main/install.sh | INSTALL_DIR=/usr/local/bin sh
```

The installer:

- detects your OS and CPU architecture
- downloads the matching GitHub Release asset
- verifies the release checksum
- installs `launchdock` into `~/.local/bin` by default

### Personal servers

You usually do not need to install `launchdock` manually on your VPS.

```bash
launchdock auth push my-vps
```

That command will install or update `launchdock` on the remote host automatically, then import your managed credentials.

## Quickstart

```bash
# log in directly, or rely on auto-discovery if you already use Claude/Codex
launchdock auth login claude
launchdock auth login openai

# inspect discovered credentials
launchdock auth list

# push managed credentials to a personal server
launchdock auth push my-vps

# launch tools
launchdock launch claude-code
launchdock launch codex
launchdock launch opencode
```

`launchdock launch <tool>` automatically:

- checks credentials
- starts the local runtime if needed
- picks compatible models for the selected tool
- writes tool config when required
- launches the tool

## Supported Tools

- `claude-code`
- `codex`
- `opencode`
- `droid`
- `pi`

## Supported Auth Sources

- Claude Code local auth
- Codex local auth
- `ANTHROPIC_API_KEY`
- `OPENAI_API_KEY`
- `~/.config/launchdock/config.json`
- direct OAuth via `launchdock auth login ...`

## Commands

```bash
launchdock auth list
launchdock auth export [credential-id ...]
launchdock auth import
launchdock auth login claude [label]
launchdock auth login openai
launchdock auth push <ssh-target> [credential-id ...]
launchdock auth remove <credential-id>

launchdock launch
launchdock launch <tool>
launchdock launch <tool> --model <model>
launchdock launch <tool> --config

launchdock start
launchdock ps
launchdock logs
launchdock restart
launchdock stop
launchdock update
launchdock version
```

## How It Works

`launchdock` runs a local runtime on `http://localhost:8090` and exposes:

- `/v1/chat/completions`
- `/v1/messages`
- `/v1/responses`
- `/v1/models`
- `/health`

It discovers credentials from local tools, environment variables, and `launchdock` config, then routes requests to the right provider for the selected model.

State files live in:

- `~/.launchdock/launchdock.pid`
- `~/.launchdock/launchdock.log`
- `~/.config/launchdock/config.json`

## Migration

This repo is moving from `llm-mux` to `launchdock`.

- default branch: `main`
- legacy code: `legacy/llm-mux`
- module path: `github.com/nghiahoang/launchdock`
- binary: `launchdock`

## Build

```bash
go build -o launchdock .
```

## Notes

- native terminal UI uses raw ANSI input and no external Go dependencies
- Claude and OpenAI OAuth flows use a local browser callback
- OpenAI credentials keep account metadata for provider-aware routing
