FROM golang:1.26-alpine AS builder

ENV NODE_ENV=production
ENV CI=true
# Delta 1: pure-Go sqlite (modernc.org/sqlite) → fully static binary, safe for
# the distroless-style nonroot final stage.
ENV CGO_ENABLED=0

WORKDIR /src

# Delta 2: no brotli/gzip — this project's go generate does not pre-compress.
RUN apk add --no-cache nodejs-current pnpm

COPY web/package.json web/pnpm-lock.yaml ./web/
RUN pnpm install --frozen-lockfile --dir web

COPY . .

# Build SPA into internal/ui/dist (go:embed picks it up at `go build`).
RUN go generate ./...

RUN mkdir -p /rootfs/data && \
    chown -R 65532:65532 /rootfs && \
    go build -o /rootfs/todo .
    # Delta 3: binary is `todo` (not `dcm`).

FROM ghcr.io/greboid/dockerbase/nonroot:1.20260714.0
COPY --from=builder /rootfs /
# Delta 4: default todo.db is cwd-relative → run from /data (owned 65532 via
# the builder chown above) so the DB lands in the writable, volume-mountable
# location without needing TODO_DB.
WORKDIR /data
ENTRYPOINT ["/todo"]
