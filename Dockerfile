FROM golang:1.27.3-alpine AS builder

ARG VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X github.com/mykolabilyi/daily-notes-mcp/internal/version.Version=${VERSION}" \
    -o /out/app ./cmd/app

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

RUN apk --no-cache add ca-certificates tzdata
COPY --from=builder /out/app /usr/local/bin/app

ENV HOST=0.0.0.0 \
    PORT=8080 \
    NOTES_DIR=/data/notes \
    NOTES_CONFIG= \
    NOTES_TZ=UTC

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/healthz" || exit 1

ENTRYPOINT ["/usr/local/bin/app"]
