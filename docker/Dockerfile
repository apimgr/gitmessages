FROM casjaysdev/go:latest AS builder

ARG TARGETARCH
ARG VERSION=devel
ARG COMMIT_ID=N/A
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out && CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -buildvcs=false -trimpath -ldflags "-s -w -X main.Version=${VERSION} -X main.CommitID=${COMMIT_ID}" -o /out/gitmessages ./src

FROM alpine:3.22

ARG VERSION=devel
ARG COMMIT_ID=N/A
ARG BUILD_DATE

LABEL org.opencontainers.image.vendor="apimgr" \
      org.opencontainers.image.title="Git Messages" \
      org.opencontainers.image.description="Curated git commit message conventions and builder" \
      org.opencontainers.image.url="https://github.com/apimgr/gitmessages" \
      org.opencontainers.image.source="https://github.com/apimgr/gitmessages" \
      org.opencontainers.image.documentation="https://github.com/apimgr/gitmessages" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT_ID}" \
      org.opencontainers.image.created="${BUILD_DATE}"

RUN apk add --no-cache ca-certificates tini \
    && addgroup -S -g 10001 gitmessages \
    && adduser -S -D -H -u 10001 -G gitmessages gitmessages \
    && mkdir -p /config /data /logs \
    && chown -R gitmessages:gitmessages /config /data /logs
COPY --from=builder /out/gitmessages /usr/local/bin/gitmessages
COPY docker/rootfs/ /
RUN chmod 755 /usr/local/bin/gitmessages /usr/local/bin/entrypoint.sh
USER 10001:10001
EXPOSE 8080
STOPSIGNAL SIGRTMIN+3
HEALTHCHECK --start-period=10m --interval=5m --timeout=15s --retries=3 CMD ["/usr/local/bin/gitmessages", "--port", "8080", "--status"]
ENTRYPOINT ["tini", "-p", "SIGTERM", "--", "/usr/local/bin/entrypoint.sh"]
