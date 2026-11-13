# Installation

## Download a release

Download the server and `gitmessages-cli` binary for your operating system from [GitHub Releases](https://github.com/apimgr/gitmessages/releases). The release includes Linux, macOS, Windows, and FreeBSD builds for amd64 and arm64.

Run the server on a local interface:

```sh
./gitmessages --address 127.0.0.1 --port 8080
```

The server saves an available port in the 64000–64999 range on first run and binds to all interfaces unless overridden. `--port` and `--address` select values for the current run. Place it behind a TLS terminating reverse proxy before exposing it publicly.

## Docker

```sh
docker run --rm -p 127.0.0.1:8080:80 ghcr.io/apimgr/gitmessages:latest
```

The container runs as UID/GID 10001 and listens on port 80. Configuration is stored under `/config/gitmessages`; data and cache are under `/data/gitmessages`; logs are under `/data/log/gitmessages`. The catalog itself is embedded and read-only. The Compose examples persist `/config` and `/data`; create those host directories with ownership UID/GID 10001 before startup.

## Build from source

Install Docker, then use the repository Makefile. Go toolchain commands run inside `casjaysdev/go:latest` containers.

```sh
make local
```
