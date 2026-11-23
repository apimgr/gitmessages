# Installation

Git Messages is a public, read-only reference with a stateless message builder. It does not accept catalog submissions or require user accounts. The server includes the browser interface, REST and GraphQL APIs, MCP endpoint, and protocol discovery routes.

## Download a release

Download the server and CLI for your operating system and CPU architecture from [GitHub Releases](https://github.com/apimgr/gitmessages/releases). Release assets use these names:

| Platform | Server | CLI |
|---|---|---|
| Linux amd64 | `gitmessages-linux-amd64` | `gitmessages-cli-linux-amd64` |
| Linux arm64 | `gitmessages-linux-arm64` | `gitmessages-cli-linux-arm64` |
| macOS amd64 | `gitmessages-darwin-amd64` | `gitmessages-cli-darwin-amd64` |
| macOS arm64 | `gitmessages-darwin-arm64` | `gitmessages-cli-darwin-arm64` |
| Windows amd64 | `gitmessages-windows-amd64.exe` | `gitmessages-cli-windows-amd64.exe` |
| Windows arm64 | `gitmessages-windows-arm64.exe` | `gitmessages-cli-windows-arm64.exe` |
| FreeBSD amd64 | `gitmessages-freebsd-amd64` | `gitmessages-cli-freebsd-amd64` |
| FreeBSD arm64 | `gitmessages-freebsd-arm64` | `gitmessages-cli-freebsd-arm64` |

The release also includes `sha256.txt`. Verify a downloaded asset against its checksum before running it. On Linux, macOS, or FreeBSD, mark the binaries executable and start the server on a local port:

```sh
chmod 755 gitmessages-linux-amd64 gitmessages-cli-linux-amd64
./gitmessages-linux-amd64 --address 127.0.0.1 --port 8080
```

Use the asset matching your platform and architecture in place of the Linux amd64 names above. On Windows, run the `.exe` file from PowerShell. The first run saves an available host port in the 64000–64999 range; containers default to port 80. `--port`, `--address`, `--config`, and `--mode` select settings for the current run. Put public deployments behind a TLS-terminating reverse proxy and configure its trusted proxy addresses.

The default CLI is statically linked and uses the interactive terminal interface. Optional `gitmessages-cli-gui-{os}-{arch}` artifacts provide the desktop GUI on Linux, macOS, and Windows; they require the system loader used by the build toolchain and are not interchangeable with the static CLI. BSD builds use the terminal interface.

Default configuration, data, cache, and log locations vary by operating system and whether the process runs as root. See [Configuration](configuration.md). `--config DIR` or `CONFIG_DIR` selects the configuration directory.

## Docker

Run the published image with persistent configuration and data directories:

```sh
mkdir -p ./volumes/config ./volumes/data
docker run -d --name gitmessages --restart unless-stopped \
  -p 127.0.0.1:8080:80 \
  -v "$PWD/volumes/config:/config" \
  -v "$PWD/volumes/data:/data" \
  ghcr.io/apimgr/gitmessages:latest
```

The container runs as UID/GID `10001:10001` and listens on port 80. Ensure the mounted host directories are writable by that UID/GID. Configuration is stored under `/config/gitmessages`; application data and cache are under `/data/gitmessages`, and logs are under `/data/log/gitmessages`. The curated catalog is embedded and read-only. The repository's Compose examples show the same persistent mounts and publish the service on a private host interface.

## Linux systemd

Install the Linux server binary at `/usr/local/bin/gitmessages`, create a dedicated service account and configuration directory, and make the config directory private:

```sh
sudo install -m 0755 gitmessages-linux-amd64 /usr/local/bin/gitmessages
sudo useradd --system --home-dir /var/lib/gitmessages --shell /usr/sbin/nologin gitmessages
sudo install -d -o gitmessages -g gitmessages -m 0700 /etc/apimgr/gitmessages
```

Create `/etc/apimgr/gitmessages/server.yml` with a loopback listener and the desired port, for example:

```yaml
server:
  address: 127.0.0.1
  port: "8080"
  mode: production
```

Install and start the service using the server's service command:

```sh
sudo /usr/local/bin/gitmessages --config /etc/apimgr/gitmessages --service --install
```

The command writes and enables the `gitmessages.service` unit, which runs as the `gitmessages` account. Check it with `systemctl status gitmessages` and view logs with `journalctl -u gitmessages`. Configure a reverse proxy for TLS and forward the original host, scheme, and optional path prefix. Add the proxy's address or CIDR to `server.trusted_proxies.additional` before relying on forwarded headers.

To remove the installed service, run `sudo /usr/local/bin/gitmessages --config /etc/apimgr/gitmessages --service --uninstall` and confirm the prompt. This deletes the configuration, data, cache, logs, backups, and PID file for the selected paths; the binary and manually created service account remain. Delete the binary and account manually if they are no longer needed.

## Build from source

Install Docker and use the repository Makefile. Go dependencies, builds, tests, and linters run in the `casjaysdev/go:latest` container; the host does not need a Go toolchain.

```sh
make local
```

The server and CLI binaries are written to `binaries/`. `make dev` instead writes development binaries to a project-scoped temporary directory. See [Development](development.md) for the full contributor workflow.
