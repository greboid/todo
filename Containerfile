FROM golang:1.26-alpine AS builder

ENV NODE_ENV=production
ENV CI=true
ENV CGO_ENABLED=0

WORKDIR /src

RUN apk add --no-cache nodejs-current pnpm

COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./web/
RUN pnpm install --frozen-lockfile --dir web

COPY . .

RUN go generate ./...

RUN mkdir -p /rootfs/data && \
    chown -R 65532:65532 /rootfs && \
    go build -o /rootfs/todo .

FROM ghcr.io/greboid/dockerbase/nonroot:1.20260714.0
COPY --from=builder /rootfs /
WORKDIR /data
ENTRYPOINT ["/todo"]
