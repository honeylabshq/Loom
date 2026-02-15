# Build stage
FROM golang:1.21-alpine AS builder
RUN apk add --no-cache ca-certificates
WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o loom ./cmd/loom

# Runtime stage: minimal image, non-root user
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 1000 -g loom loom
USER loom
WORKDIR /app

COPY --from=builder /build/loom /app/loom

# Mount config at /etc/loom/loom.toml (or override with -config)
EXPOSE 8443 9080

ENTRYPOINT ["/app/loom"]
CMD ["-config", "/etc/loom/loom.toml"]
