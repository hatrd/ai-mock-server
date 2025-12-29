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

Not working for now. Enable tool call simulation in `/v1/messages` responses:

```bash
export MOCK_TOOL_CALLS=1
```

When enabled, the server only returns tool_use blocks if the request includes
`tools` and does not already include any `tool_result`. After the client sends
tool results, the server returns the normal text reply.

Tool commands are intentionally safe defaults. If you need different commands,
edit `mockToolUses` in `main.go`.
