# syntax=docker/dockerfile:1

# Reproducible image for the Pure-Go Provider Gateway (#68).
#
# The image builds and runs the SAME production composition entrypoint
# `cmd/gateway` created by #44. There is no sandbox-only main, handler, or
# runtime path: the binary is exactly what production composition produces.
# The build context is the `apps/gateway` module directory only, so the repo
# root, `.ref/`, secrets, and credential files are never in the build context.

# --- Build stage -------------------------------------------------------------
# Compile from tracked sources with the CGO-free, zero-third-party dependency
# budget locked in docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md.
FROM golang:1.25.5-alpine AS build
WORKDIR /src

# Module manifest first for a cached, reproducible resolution step. The gateway
# module declares no third-party dependencies, so this is empty-safe and needs
# no go.sum.
COPY go.mod ./
RUN go mod download

# Only the production source trees. Nothing else from the module is copied.
COPY cmd ./cmd
COPY internal ./internal

# Static, trimmed, stripped build so the binary runs on a distroless read-only
# root filesystem with no libc, shell, or package manager.
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

# --- Runtime stage -----------------------------------------------------------
# Distroless static nonroot: no shell, no package manager, and a non-root user
# (uid 65532) by default so the sandbox runs unprivileged on a read-only root
# filesystem.
FROM gcr.io/distroless/static:nonroot AS runtime

COPY --from=build /out/gateway /gateway

# Non-secret process settings only. The process binds inside the container's
# isolated network namespace; the sandbox publishes it to the HOST loopback
# only (see deploy/sandbox/docker-compose.yml). No credential-bearing value is
# baked into any image layer.
ENV PIXELPLUS_GATEWAY_ADDR=0.0.0.0:8080 \
    PIXELPLUS_GATEWAY_STARTUP_TIMEOUT=10s \
    PIXELPLUS_GATEWAY_SHUTDOWN_TIMEOUT=10s

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/gateway"]
