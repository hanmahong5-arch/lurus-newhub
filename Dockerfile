FROM node:22-slim AS node

FROM oven/bun:latest AS builder
# bun stays the package manager and script runner. The node binary is here
# only so that `bun run build` executes vite under node (vite's bin has a
# node shebang; without a node on PATH bun runs it itself). Under bun the
# build peaked at 6.5G RSS, measured, and was OOM-killed on the 6-7G CI
# runners; under node it peaks at ~3.2G. NODE_OPTIONS gives node the ~4G
# heap it would size for itself on a 16G host instead of deriving a smaller
# one from the container's cgroup.
COPY --from=node /usr/local/bin/node /usr/local/bin/node

WORKDIR /build
COPY web/package.json .
COPY web/bun.lock .
RUN bun install
COPY ./web .
COPY ./VERSION .
RUN DISABLE_ESLINT_PLUGIN='true' NODE_OPTIONS=--max-old-space-size=4096 VITE_REACT_APP_VERSION=$(cat VERSION) bun run build

FROM golang:1.26-alpine AS builder2
ENV GO111MODULE=on CGO_ENABLED=0

ARG TARGETOS
ARG TARGETARCH
ENV GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64}
ENV GOEXPERIMENT=greenteagc

WORKDIR /build

# Copy zita-sdk-go (identity SDK, ADR-0011) — pinned via CI checkout ref
COPY zita-sdk-go/ /shared/zita-sdk-go/

# Copy lurus-entkit (offline entitlement-token verifier, platform ADR 0030)
COPY lurus-entkit/ /shared/lurus-entkit/

# Module proxy is a build arg so CI can try a nearby mirror first; go.sum
# pins every module's hash, so the proxy cannot change what gets built.
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
ADD go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=builder /build/dist ./web/dist
RUN go build -ldflags "-s -w -X 'github.com/LurusTech/lurus-hub/internal/pkg/common.Version=$(cat VERSION)'" -o lurus-api ./cmd/server

FROM debian:bookworm-slim

# apt-get upgrade applies the latest Debian security patches (e.g. libgnutls30
# 3.7.9-2+deb12u7, pulled in transitively by wget) — without it the GHA layer
# cache can pin an older, CVE-flagged package set. Keep this so the Trivy gate
# stays green on base-image CVEs.
# SECURITY_REFRESH below busts the GHA layer cache: a cached RUN layer keeps
# the package set frozen at cache time, so a CVE fixed upstream (e.g.
# CVE-2026-45447 libssl3 deb12u2) never reaches the image until the
# instruction text changes. Bump the date whenever Trivy flags a fixed CVE.
ARG SECURITY_REFRESH=2026-09-14
RUN apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends ca-certificates tzdata libasan8 wget \
    && rm -rf /var/lib/apt/lists/* \
    && update-ca-certificates

COPY --from=builder2 /build/lurus-api /
EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/lurus-api"]
