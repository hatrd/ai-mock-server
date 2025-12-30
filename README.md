# Anthropic Mock Server (Go)

A tiny Anthropic-compatible mock server that always replies with "喵喵喵喵~".

## Run

```bash
go run .
```

The server listens on `:8080` by default. Set `PORT` to change it.

## Use with Claude Code

Point Claude Code at this server and use any API key; the server ignores auth.
Example environment variables:

```bash
export ANTHROPIC_BASE_URL=http://localhost:8080
export ANTHROPIC_API_KEY=anything
```

Supported endpoints:
- `POST /v1/messages` (streaming and non-streaming)
- `POST /v1/complete`
- `GET /v1/models`
- `POST /api/event_logging/batch` (no-op, returns 204)

If you only see `/api/event_logging/batch` in logs and no model replies,
double-check that Claude Code is using the mock server as the API base URL.

## Honeypot logging

By default, the server logs request bodies to stdout. You can also write logs
to a file:

```bash
export HONEYPOT_LOG_PATH=./honeypot.log
```

Optional:
- `HONEYPOT_LOG_BODY=0` disables body logging.
- `HONEYPOT_LOG_MAX_BYTES=1048576` limits logged body size.

## Mock tool calls

Tool call simulation is enabled by default. The server returns `tool_use` blocks
when request includes `tools`, and keeps returning tool calls indefinitely.

To disable:

```bash
export MOCK_TOOL_CALLS=0
```

## Custom Bash commands

By default, the Bash tool returns `echo 喵喵喵喵~`. You can provide a file with
custom commands (one per line), and the server will randomly pick one each time:

```bash
export BASH_COMMANDS_FILE=./bash_commands.txt
```

Example file format:

```text
# Comments start with #
echo "Hello World"
ls -la
pwd
date
```