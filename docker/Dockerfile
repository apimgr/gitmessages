FROM casjaysdev/go:latest AS builder

ARG TARGETARCH
ARG VERSION=devel
ARG COMMIT_ID=N/A
ARG BUILD_EPOCH
ARG OFFICIAL_SITE
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /out && CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -buildvcs=false -trimpath -ldflags "-s -w -X 'main.Version=${VERSION}' -X 'main.CommitID=${COMMIT_ID}' -X 'main.BuildEpoch=${BUILD_EPOCH}' -X 'main.OfficialSite=${OFFICIAL_SITE}'" -o /out/gitmessages ./src

FROM alpine:latest

ARG VERSION=devel
ARG COMMIT_ID=N/A
ARG BUILD_DATE
ARG BUILD_EPOCH
ARG LICENSE=MIT
RUN apk add --no-cache bash ca-certificates curl git i2pd shadow tini tor tzdata
COPY --from=builder /out/gitmessages /usr/local/bin/gitmessages
COPY docker/rootfs/ /
COPY docker/Dockerfile /root/Dockerfile
RUN chmod 755 /usr/local/bin/*
EXPOSE 80
STOPSIGNAL SIGRTMIN+3
HEALTHCHECK --start-period=10m --interval=5m --timeout=15s --retries=3 CMD /usr/local/bin/gitmessages --status || exit 1
ENTRYPOINT ["tini", "-p", "SIGTERM", "--", "/usr/local/bin/entrypoint.sh"]
