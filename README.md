# Daily Notes MCP

[![xc compatible](https://xcfile.dev/badge.svg)](https://xcfile.dev)

MCP server for reading and editing daily markdown notes. Runs over streamable HTTP and is intended to be deployed alongside agents in Kubernetes.

## Tools

- `get_today_note` — get today's note, creating it with an empty body if needed.
- `update_today_note` — replace today's note body; requires the current revision.
- `get_note` — get a note body and revision by date.
- `search_notes` — case-insensitive substring search with line snippets.

By default notes are stored as `<notes-dir>/YYYY-MM-DD.md`. The path can be
configured with an Obsidian `daily-notes.json` file (see below). Updates use
optimistic concurrency: call `get_today_note`, then pass the returned
`revision` to `update_today_note`. Writes are atomic and protected by a
per-note file lock.

## Configuration

| Flag | Environment | Default | Description |
|---|---|---|---|
| `-host` | `HOST` | `localhost` | Listen host |
| `-port` | `PORT` | `8000` | Listen port |
| `-notes-dir` | `NOTES_DIR` | `./notes` | Notes directory (vault root) |
| `-config` | `NOTES_CONFIG` | _(unset)_ | Path to an Obsidian `daily-notes.json` |
| `-tz` | `NOTES_TZ` | `UTC` | Timezone for "today" |

The Docker image is configured entirely through environment variables
(`HOST=0.0.0.0`, `PORT=8080`, `NOTES_DIR=/data/notes`, `NOTES_CONFIG`, `NOTES_TZ`).
Flags are for local development only.

### Obsidian daily notes config

Point `-config` / `NOTES_CONFIG` at an Obsidian daily-notes config to control
the folder, filename format, and template. The notes directory is the vault
root, so the config can be mounted directly from the vault:

```json
{
  "folder": "Daily",
  "format": "YYYY/MM-MMMM/YYYY-MM-DD-dddd",
  "template": "Daily/Template"
}
```

With this config a note for `2026-11-04` is stored at:

```
<notes-dir>/Daily/2026/11-November/2026-11-04-Wednesday.md
```

When the config is omitted, the previous flat `YYYY-MM-DD.md` layout is used.

Supported format tokens: `YYYY`, `YY`, `MMMM`, `MMM`, `MM`, `M`, `DD`, `D`,
`dddd`, `ddd`, `dd`, `d`, `HH`, `H`, `hh`, `h`, `mm`, `m`, `ss`, `s`, `A`, `a`.
Wrap literal text in brackets, for example `[Week] YYYY-MM-DD`. The format must
include year, month, and day tokens.

New notes are created from the template. Template frontmatter is merged with
the server-managed `date` property, and existing frontmatter is preserved on
updates. Supported template placeholders:

- `{{date}}` and `{{date:FORMAT}}`
- `{{time}}` and `{{time:FORMAT}}`
- `{{title}}` — the generated note filename without directories or `.md`

Notes are listed and searched recursively below the notes directory. The date
is read from the configured filename format, falling back to the `date`
frontmatter property.

Endpoints:

- `/mcp` — MCP streamable HTTP endpoint
- `/healthz` — liveness probe
- `/readyz` — readiness probe (checks the notes directory is writable)

## Kubernetes

Example manifests for a single-replica deployment backed by a
`ReadWriteOnce` PVC. Replace the image tag with a release version. The pod uses
`fsGroup: 65532` so the non-root container can write to the mounted volume.

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: daily-notes
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: daily-notes-mcp
spec:
  replicas: 1
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app: daily-notes-mcp
  template:
    metadata:
      labels:
        app: daily-notes-mcp
    spec:
      securityContext:
        fsGroup: 65532
      containers:
        - name: mcp
          image: ghcr.io/mykolabilyi/daily-notes-mcp
          ports:
            - name: http
              containerPort: 8080
          env:
            - name: NOTES_TZ
              value: Europe/Prague
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 2
            periodSeconds: 10
            timeoutSeconds: 3
            failureThreshold: 3
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 2
            periodSeconds: 10
            timeoutSeconds: 3
            failureThreshold: 3
          volumeMounts:
            - name: notes
              mountPath: /data/notes
      volumes:
        - name: notes
          persistentVolumeClaim:
            claimName: daily-notes
---
apiVersion: v1
kind: Service
metadata:
  name: daily-notes-mcp
spec:
  selector:
    app: daily-notes-mcp
  ports:
    - name: http
      port: 8080
      targetPort: http
```

Keep one replica when using a `ReadWriteOnce` volume. The service is a shared
vault without authentication, so restrict access with a Kubernetes
`NetworkPolicy`.

## Tasks

### run

Run project

```sh
go run ./cmd/app -host 0.0.0.0 -port 8080
```

### build

Build the binary with the version derived from git tags

```sh
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
go build -ldflags "-X github.com/mykolabilyi/daily-notes-mcp/internal/version.Version=$VERSION" -o bin/app ./cmd/app
```

### build-docker

Create docker image

```sh
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
docker build --build-arg VERSION="$VERSION" -t ghcr.io/mykolabilyi/daily-notes-mcp:dev .
```

## Versioning

The server version comes from git tags at build time. Tag a release with
`git tag v1.2.3`; builds from a tagged commit report `v1.2.3`, while untagged
commits report `v1.2.3-<n>-g<sha>[-dirty]`. Without any tags, git falls back to
the commit SHA. Local `go run` builds report `dev`.

## Development

```sh
gofmt -w .
go vet ./...
go test ./...
```
