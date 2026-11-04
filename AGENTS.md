# AGENTS.md

Guidance for agents working in this repository.

## Project

`daily-notes-mcp` is a Model Context Protocol (MCP) server written in Go, built with the official
[`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) v1.8.0.
It uses the streamable HTTP transport and exposes tools for reading and editing daily markdown notes.

Notes live as `<notes-dir>/YYYY-MM-DD.md` with server-managed YAML frontmatter containing only
`date`. Writes use optimistic concurrency (a SHA-256 revision), a per-note file lock, and atomic
temp-file rename.

```sh
go run ./cmd/app -host 0.0.0.0 -port 8080
```

## Layout

- `cmd/app/main.go` — entrypoint, flag/env configuration, HTTP server lifecycle.
- `internal/notes/` — note storage: dates, frontmatter, revisions, locking, atomic writes, search.
- `internal/server/` — MCP tool registration and HTTP mux (MCP endpoint + health probes).
- `internal/version/` — version string injected at build time from git tags.
- `internal/middleware/` — cross-cutting MCP middleware (request/response logging).
- `pkg/` — reusable packages intended for external use; keep `internal/` for project-private code.
- `Dockerfile` / `docker-compose.yaml` — multi-stage build, non-root user, exposes `8080`.
- `.devcontainer/` — Go devcontainer with mise; `setup.sh` installs mise tools.
- `mise.toml` — developer tools (`chezmoi`, `xc`).

## Toolchain

- Go `1.27.3` (match `go.mod` and the Dockerfile).
- Module path: `github.com/mykolabilyi/daily-notes-mcp`.
- Use the standard library and the MCP SDK; do not add dependencies without a clear need.
- `xc` tasks are defined in the README (`run`, `build-docker`). Prefer mise-managed tools.

## Commands

```sh
go build ./...                      # build all packages
go run ./cmd/app                    # run MCP server (localhost:8000)
go test ./...                       # run tests
go vet ./...                        # static checks
gofmt -w .                          # format (or: gofmt -l . to check)
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
docker build --build-arg VERSION="$VERSION" -t ghcr.io/mykolabilyi/daily-notes-mcp:dev .
```

The MCP server version is `internal/version.Version`, a `var` set with
`-ldflags "-X github.com/mykolabilyi/daily-notes-mcp/internal/version.Version=<version>"`.
Release builds should pass the git tag; local builds default to `dev`.

Always run `gofmt`, `go vet ./...`, and `go test ./...` before finishing a change.
No linter is configured; do not invent one unless the task requires it.

## Configuration

Flags with environment fallbacks:

| Flag | Environment | Default |
|---|---|---|
| `-host` | `HOST` | `localhost` |
| `-port` | `PORT` | `8000` |
| `-notes-dir` | `NOTES_DIR` | `./notes` |
| `-tz` | `NOTES_TZ` | `UTC` |

## MCP conventions

- Register tools with `mcp.AddTool(server, &mcp.Tool{...}, handler)`. Tool handlers have the
  signature `func(context.Context, *mcp.CallToolRequest, *In) (*mcp.CallToolResult, Out, error)`.
- Tool parameter structs use `json` and `jsonschema` tags; the schema is derived from the struct.
- Return user-facing text via `&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{...}}}`;
  typed outputs populate `StructuredContent`.
- Return an error for invalid input or failed operations; do not panic in handlers.
- Add cross-cutting behavior (logging, auth, metrics) as middleware in `internal/middleware` and
  register it with `server.AddReceivingMiddleware(...)`.
- Keep tool names and descriptions stable and descriptive; MCP clients depend on them.

Current tools: `get_today_note`, `update_today_note`, `get_note`, `search_notes`.

## Code style

- Idiomatic Go: small packages, exported identifiers documented, errors wrapped with `%w`.
- Match the existing style; keep `main` thin and move logic into `internal/` or `pkg/`.
- Do not add comments that restate the code. Preserve existing license headers on copied SDK files.
- Defaults should make local development work out of the box.

## Docker

- Multi-stage build on `golang:<go-version>-alpine`; final image is `alpine` with CA certs.
- Runs as UID/GID `65532`; bind to `0.0.0.0:8080` inside the container.
- The container is configured through environment variables (`HOST`, `PORT`,
  `NOTES_DIR`, `NOTES_TZ`), not command flags.
- Notes directory defaults to `/data/notes`, owned by `65532:65532`.
- Image name used by compose: `ghcr.io/mykolabilyi/daily-notes-mcp:dev`.

## Notes for agents

- Do not commit, push, or open PRs unless explicitly asked.
- Do not add secrets or credentials; configuration is via flags/environment.
- The service is a single shared notes vault without authentication; access control is expected to
  come from Kubernetes network policy.
