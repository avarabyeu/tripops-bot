# syntax=docker/dockerfile:1
#
# One image. The Mini App is compiled into the binary (internal/webapp), so
# there is no nginx container, no private network between the two, and no way
# to ship a new front end against an old backend.

# ------------------------------------------------------------- web builder --
# Pinned to the *build* platform: the bundle is static files with no
# architecture, so building npm under emulation would cost minutes for nothing.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web

WORKDIR /app/miniapp

COPY miniapp/package.json miniapp/package-lock.json* ./
RUN npm ci --no-audit --no-fund || npm install --no-audit --no-fund

COPY miniapp/ ./
# vite.config.ts writes to ../internal/webapp/dist, which is where the Go
# build below expects to find it.
RUN npm run build

# ----------------------------------------------------------------- builder --
# Pinned to the *build* platform, not the target. Go cross-compiles, so an
# amd64 image builds at full speed on an arm64 laptop; without this, a
# --platform build runs the whole compiler under QEMU and takes minutes.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder

WORKDIR /src

# Dependencies first so a source-only change reuses the module cache.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# The bundle, into the directory internal/webapp embeds. It lands after the
# source copy so a front-end-only change does not invalidate the Go module
# cache above it.
COPY --from=web /app/internal/webapp/dist ./internal/webapp/dist

ARG VERSION=docker
# Set by BuildKit from --platform; empty on a plain `docker build`, where the
# Go toolchain then defaults to the host architecture, which is what we want.
ARG TARGETARCH
# CGO stays off: the SQLite driver is pure Go, so the result is a static binary
# that runs on an empty base image — and it cross-compiles with nothing but
# GOARCH.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build \
      -trimpath \
      -ldflags "-s -w -X github.com/avarabyeu/tripops-bot/internal/cli.version=${VERSION}" \
      -o /out/tripops ./cmd/tripops

# The data directory has to exist in the image, not just in the compose file.
# Docker seeds a fresh named volume from the image directory — ownership
# included — and a mountpoint it has to invent instead belongs to root, which
# the nonroot user then cannot write a SQLite file into.
RUN mkdir -p /out/data

# ----------------------------------------------------------------- runtime --
FROM gcr.io/distroless/static-debian12:nonroot

# The binary needs CA certificates to reach api.telegram.org and the timezone
# database to convert trip times; distroless/static ships both.
COPY --from=builder /out/tripops /usr/local/bin/tripops

# 65532 is the nonroot user in the distroless images.
COPY --from=builder --chown=65532:65532 /out/data /data

USER nonroot:nonroot
WORKDIR /data
EXPOSE 8080

ENV APP_ENV=production \
    PORT=8080

ENTRYPOINT ["/usr/local/bin/tripops"]
CMD ["serve"]
