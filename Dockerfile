# Build stage
#
# Third place the Go version is written down, and the one that cannot read
# go.mod for itself, so it has to be kept at or above the `go` directive by
# hand. A newer toolchain builds an older directive, so running ahead is safe
# and running behind is the failure: "go.mod requires go >= 1.25.0 (running go
# 1.21.13)" is what a dependency bump looked like before this was raised.
FROM golang:1.25-alpine AS builder
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
