# Multi-stage build
FROM alpine:latest AS builder

# Install build dependencies
RUN apk add --no-cache bash curl make go git

# Set working directory
WORKDIR /build

# Copy source
COPY . .

# Download dependencies and build
RUN go mod download && \
    make build

# Runtime stage - scratch for minimal size
FROM scratch

# Copy CA certificates for HTTPS
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy binary
COPY --from=builder /build/binaries/gitmessages-linux-amd64 /gitmessages

# Metadata labels
LABEL org.opencontainers.image.source="https://github.com/apimgr/gitmessages"
LABEL org.opencontainers.image.description="gitmessages server"
LABEL org.opencontainers.image.licenses="MIT"

# Expose default port (informational only)
EXPOSE 80

# Run
ENTRYPOINT ["/gitmessages"]
