# launchdock

Launch AI coding tools through one local runtime.

`launchdock` discovers your Claude and OpenAI credentials, starts a local runtime when needed, and launches supported CLI tools against a single local endpoint.

It is designed for people who use multiple coding agents and want one place to manage auth, models, and local routing.

## Migration

This repo is moving from `llm-mux` to `launchdock`.

- default branch: `launchdock`
- legacy code: keep on `legacy/llm-mux`
- module path: `github.com/nghiahoang/launchdock`
- binary: `launchdock`

## What It Does

- auto-discovers credentials from Claude Code, Codex, environment variables, and `launchdock` config
- supports direct OAuth login for Claude and OpenAI
- auto-starts a local runtime on `localhost:8090`
- launches supported tools with the right provider and model wiring
- pools credentials across accounts and routes requests by model/provider

## Quickstart

```bash
# log in directly, or rely on auto-discovery if you already use Claude/Codex
launchdock auth login claude
launchdock auth login openai

# inspect discovered credentials
launchdock auth list

# launch a tool
launchdock launch claude-code
launchdock launch codex
```

`launchdock launch <tool>` automatically:

- checks credentials
- starts the local runtime if it is not already running
- lets you pick a compatible model when needed
- writes tool config if the tool expects one
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
launchdock auth login claude [label]
launchdock auth login openai

launchdock launch
launchdock launch <tool>
launchdock launch <tool> --model <model>
launchdock launch <tool> --config

launchdock start
launchdock ps
launchdock logs
launchdock restart
launchdock stop
```

## Runtime

The runtime listens on `http://localhost:8090` by default and exposes:

- `/v1/chat/completions`
- `/v1/messages`
- `/v1/responses`
- `/v1/models`
- `/health`

`launchdock launch ...` auto-starts this runtime in the background when necessary.

State files live in:

- `~/.launchdock/launchdock.pid`
- `~/.launchdock/launchdock.log`
- `~/.config/launchdock/config.json`

## Build

```bash
go build -o launchdock .
```

## Notes

- native terminal UI uses raw ANSI input, no external Go dependencies
- Claude and OpenAI OAuth flows use a local browser callback
- OpenAI auth is provider-aware and stores refreshable credentials in `launchdock` config
