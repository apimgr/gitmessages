# Variables
PROJECTNAME = gitmessages
PROJECTORG = apimgr
VERSION = $(shell cat release.txt || echo "0.0.1")
COMMIT = $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE = $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -ldflags "-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)"

# Go parameters
GOCMD = go
GOBUILD = $(GOCMD) build
GOCLEAN = $(GOCMD) clean
GOTEST = $(GOCMD) test
GOGET = $(GOCMD) get
GOMOD = $(GOCMD) mod

# Binary names
BINARY_DIR = binaries
BINARY_NAME = $(PROJECTNAME)

# Build targets
.PHONY: all build release test docker dev clean help

all: build

help:
	@echo "Available targets:"
	@echo "  build        - Build for all platforms"
	@echo "  release      - Create GitHub release and upload binaries"
	@echo "  test         - Run all tests"
	@echo "  docker       - Build and push Docker image"
	@echo "  dev          - Build development version with hot reload"
	@echo "  clean        - Clean build artifacts"

build:
	@echo "Building $(PROJECTNAME) v$(VERSION) ($(COMMIT))..."
	@mkdir -p $(BINARY_DIR)

	# Increment version
	@echo "$(VERSION)" | awk -F. '{$$NF = $$NF + 1;} 1' | sed 's/ /./g' > release.txt

	# Linux AMD64
	@echo "Building for Linux AMD64..."
	@GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)-linux-amd64 ./src

	# Linux ARM64
	@echo "Building for Linux ARM64..."
	@GOOS=linux GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)-linux-arm64 ./src

	# Windows AMD64
	@echo "Building for Windows AMD64..."
	@GOOS=windows GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)-windows-amd64.exe ./src

	# Windows ARM64
	@echo "Building for Windows ARM64..."
	@GOOS=windows GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)-windows-arm64.exe ./src

	# macOS AMD64
	@echo "Building for macOS AMD64..."
	@GOOS=darwin GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)-macos-amd64 ./src

	# macOS ARM64
	@echo "Building for macOS ARM64..."
	@GOOS=darwin GOARCH=arm64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)-macos-arm64 ./src

	# BSD AMD64
	@echo "Building for BSD AMD64..."
	@GOOS=freebsd GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME)-bsd-amd64 ./src

	# Host system binary
	@echo "Building for host system..."
	@$(GOBUILD) $(LDFLAGS) -o $(BINARY_DIR)/$(BINARY_NAME) ./src

	@echo "Build complete! Binaries in $(BINARY_DIR)/"

release:
	@echo "Creating release for v$(VERSION)..."
	@if command -v gh >/dev/null 2>&1; then \
		gh release delete v$(VERSION) -y 2>/dev/null || true; \
		git tag -d v$(VERSION) 2>/dev/null || true; \
		git push origin :refs/tags/v$(VERSION) 2>/dev/null || true; \
		gh release create v$(VERSION) --title "Release v$(VERSION)" --notes "Release $(VERSION)" \
			$(BINARY_DIR)/$(BINARY_NAME)-* || echo "Release creation failed"; \
	else \
		echo "GitHub CLI (gh) not found. Please install it to create releases."; \
	fi

test:
	@echo "Running tests..."
	@$(GOTEST) -v ./tests/unit/...
	@$(GOTEST) -v ./tests/integration/...
	@$(GOTEST) -bench=. -benchmem ./...

docker:
	@echo "Building Docker image..."
	@docker build -t $(PROJECTNAME):dev .
	@docker tag $(PROJECTNAME):dev ghcr.io/$(PROJECTORG)/$(PROJECTNAME):latest
	@docker tag $(PROJECTNAME):dev ghcr.io/$(PROJECTORG)/$(PROJECTNAME):$(VERSION)
	@echo "Pushing to GitHub Container Registry..."
	@docker push ghcr.io/$(PROJECTORG)/$(PROJECTNAME):latest
	@docker push ghcr.io/$(PROJECTORG)/$(PROJECTNAME):$(VERSION)

dev:
	@echo "Building development version..."
	@mkdir -p $(BINARY_DIR)
	@$(GOBUILD) -tags development -o $(BINARY_DIR)/$(BINARY_NAME)-dev ./src
	@echo "Development build complete: $(BINARY_DIR)/$(BINARY_NAME)-dev"

run-dev: dev
	@echo "Running in development mode with hot reload..."
	@$(BINARY_DIR)/$(BINARY_NAME)-dev --dev

test-watch:
	@echo "Running tests in watch mode..."
	@while true; do \
		$(GOTEST) -v ./tests/unit/...; \
		inotifywait -qre close_write ./src ./tests; \
	done

mock-data:
	@echo "Generating mock data..."
	@$(BINARY_DIR)/$(BINARY_NAME) --dev --generate-mock-data || echo "Binary not built yet. Run 'make dev' first."

reset-dev:
	@echo "Resetting development environment..."
	@rm -f *.db *.sqlite *.sqlite3
	@echo "Development environment reset complete."

clean:
	@echo "Cleaning..."
	@$(GOCLEAN)
	@rm -rf $(BINARY_DIR)
	@rm -rf release
	@echo "Clean complete."

# Dependencies
deps:
	@echo "Downloading dependencies..."
	@$(GOMOD) download
	@$(GOMOD) tidy

# Format code
fmt:
	@echo "Formatting code..."
	@$(GOCMD) fmt ./...

# Lint
lint:
	@echo "Linting..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found. Install it from https://golangci-lint.run/"; \
	fi
