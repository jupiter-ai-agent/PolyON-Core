# Stage 1: Build
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build main API server
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /polyon-core ./cmd/polyon/

# setup-runner is no longer built — lifecycle is handled by Sentinel (lifecycle-api.sh)

# Stage 2: Runtime
FROM alpine:3.21

# docker CLI + compose plugin + samba-client for AD/DC ops
RUN apk add --no-cache \
    ca-certificates \
    docker-cli \
    docker-cli-compose \
    openssl \
    samba-client

# Binaries
COPY --from=builder /polyon-core  /app/polyon-core

# Migrations (SQL embedded for reference; runner runs them via pgx)
COPY migrations/ /app/migrations/

EXPOSE 8000

# Default entrypoint is the API server.
# Setup runner is launched explicitly: /app/setup-runner
ENTRYPOINT ["/app/polyon-core"]
