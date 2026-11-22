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
RUN apk add --no-cache bash ca-certificates curl git i2pd libcap-utils tini tor \
    && addgroup -S -g 10001 gitmessages \
    && adduser -S -D -H -u 10001 -G gitmessages gitmessages \
    && mkdir -p /config /data /logs \
    && chown -R gitmessages:gitmessages /config /data /logs
COPY --from=builder /out/gitmessages /usr/local/bin/gitmessages
COPY docker/rootfs/ /
COPY docker/Dockerfile /root/Dockerfile
RUN chmod 755 /usr/local/bin/gitmessages /usr/local/bin/entrypoint.sh \
    && setcap 'cap_net_bind_service=+ep' /usr/local/bin/gitmessages
USER 10001:10001
EXPOSE 80
STOPSIGNAL SIGRTMIN+3
HEALTHCHECK --start-period=10m --interval=5m --timeout=15s --retries=3 CMD ["/usr/local/bin/gitmessages", "--status"]
ENTRYPOINT ["tini", "-p", "SIGTERM", "--", "/usr/local/bin/entrypoint.sh"]
