# GitMessages

A universal server template for managing git commit messages and conventions.

## Quick Installation

### Linux/macOS

```bash
# Download and run installer
curl -fsSL https://raw.githubusercontent.com/apimgr/gitmessages/main/scripts/install.sh | bash
```

### Windows

```powershell
# Download and run installer (PowerShell as Administrator)
iwr -useb https://raw.githubusercontent.com/apimgr/gitmessages/main/scripts/windows.ps1 | iex
```

### Docker

```bash
# Using docker-compose
docker-compose up -d

# Or using docker directly
docker run -d \
  --name gitmessages \
  -p 80:80 \
  -v ./data:/data \
  -v ./config:/config \
  ghcr.io/apimgr/gitmessages:latest
```

## Usage

After installation, the server will automatically start and be accessible at:
- **HTTP**: `http://your-server-ip:port`
- **HTTPS**: `https://your-server-ip:port` (if configured)

### First Run Setup

1. Open your browser to the server address
2. Create your user account (this will be the owner)
3. Create the administrator account (username: "administrator")
4. Configure server settings in the admin panel

### CLI Commands

```bash
# Show version
gitmessages --version

# Show status
gitmessages --status

# Show help
gitmessages --help

# Set custom port
gitmessages --port 8080

# Set dual ports (HTTP and HTTPS)
gitmessages --port "8080,8443"

# Development mode
gitmessages --dev
```

## Features

- ✅ Single binary with all assets embedded
- ✅ Multiple database support (SQLite, MySQL, PostgreSQL, MSSQL)
- ✅ Automatic Let's Encrypt SSL/TLS certificates
- ✅ Web-based administration interface
- ✅ RESTful API with Swagger documentation
- ✅ GraphQL endpoint
- ✅ Built-in scheduler for automated tasks
- ✅ Comprehensive health monitoring
- ✅ Automatic backups
- ✅ PWA support
- ✅ Dark/Light theme
- ✅ Mobile responsive
- ✅ Multi-platform support (Linux, macOS, Windows, BSD)

## API Documentation

Interactive API documentation is available at:
- **Swagger UI**: `http://your-server:port/api/docs`
- **GraphQL Playground**: `http://your-server:port/api/graphql`

## Health Check

Monitor server health at:
- **JSON**: `http://your-server:port/healthz`
- **API**: `http://your-server:port/api/v1/health`
- **Text**: `http://your-server:port/api/v1/health.txt`

## Configuration

All configuration is stored in the database and managed through the web interface. No configuration files are needed.

### Database Options

- **SQLite** (default): No setup required, works out of the box
- **MySQL/MariaDB**: Configure in admin settings
- **PostgreSQL**: Configure in admin settings
- **MSSQL**: Configure in admin settings

## Backup & Restore

Automatic backups run daily at 3:00 AM. Backups include:
- Database (daily, weekly, monthly rotation)
- User data and uploaded files
- System settings

To restore from backup, use the admin interface or CLI:

```bash
./scripts/restore.sh /path/to/backup.tar.gz
```

## Security

- All inputs are validated and sanitized
- Secure session management (30-day persistent sessions)
- Rate limiting on all endpoints
- API token authentication
- Comprehensive audit logging
- Security headers on all responses
- Optional 2FA support

## Development

### Build from Source

```bash
# Clone repository
git clone https://github.com/apimgr/gitmessages.git
cd gitmessages

# Build all platforms
make build

# Build for development
make dev

# Run tests
make test

# Run in development mode with hot reload
make run-dev
```

### Project Structure

```
.
├── src/              # All source code
├── scripts/          # Installation scripts
├── tests/            # Test files
├── binaries/         # Built binaries
├── rootfs/           # Docker volumes
├── Makefile          # Build automation
├── Dockerfile        # Container definition
└── docker-compose.yml
```

### Requirements

- Go 1.21 or later
- Make
- Docker (optional)

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

MIT License - see [LICENSE.md](LICENSE.md) for details.

## Support

- **Issues**: https://github.com/apimgr/gitmessages/issues
- **Documentation**: https://github.com/apimgr/gitmessages/wiki

---

Built with ❤️ following the [Universal Server Template Specification v1.0](CLAUDE.md)
