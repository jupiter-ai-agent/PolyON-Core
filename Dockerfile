# Stage 1: Build
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build main API server
ARG CORE_VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X github.com/triangles/polyon-core/internal/api.CoreVersion=${CORE_VERSION}" -o /polyon-core ./cmd/polyon/

# setup-runner is no longer built — lifecycle is handled by Sentinel (lifecycle-api.sh)

# Stage 2: Runtime
FROM alpine:3.21

# kubectl + samba-client for AD/DC ops (K8s environment)
RUN apk add --no-cache \
    ca-certificates \
    curl \
    openssl \
    samba-client \
 && ARCH=$(uname -m) \
 && case "$ARCH" in aarch64) KARCH=arm64;; x86_64) KARCH=amd64;; *) KARCH=amd64;; esac \
 && curl -fsSL "https://dl.k8s.io/release/$(curl -fsSL https://dl.k8s.io/release/stable.txt)/bin/linux/${KARCH}/kubectl" \
    -o /usr/local/bin/kubectl \
 && chmod +x /usr/local/bin/kubectl \
 && apk del curl

# Binaries
COPY --from=builder /polyon-core  /app/polyon-core

# Migrations (SQL embedded for reference; runner runs them via pgx)
COPY migrations/ /app/migrations/

EXPOSE 8000

# Default entrypoint is the API server.
# Setup runner is launched explicitly: /app/setup-runner
ENTRYPOINT ["/app/polyon-core"]
