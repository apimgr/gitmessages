# Infer the project identity from its Git remote, then fall back to its directory.
PROJECT_NAME := $(shell remote=$$(git remote get-url origin 2>/dev/null); if [ -n "$$remote" ]; then basename "$$remote" .git; else basename "$(PWD)"; fi)
PROJECT_ORG := $(shell remote=$$(git remote get-url origin 2>/dev/null); if [ -n "$$remote" ]; then printf "%s\n" "$$remote" | sed -E 's|.*/([^/]+)/[^/]+(\.git)?$$|\1|'; else basename "$(dir $(PWD))"; fi)
INTERNAL_NAME := gitmessages
VERSION ?= $(shell version=; [ -r release.txt ] && version=$$(tr -d '[:space:]' < release.txt); if [ -n "$$version" ]; then printf '%s\n' "$$version"; else git rev-parse --short=7 HEAD 2>/dev/null || printf 'devel\n'; fi)
COMMIT_ID := $(shell git rev-parse --short=7 HEAD 2>/dev/null || printf 'N/A\n')
BUILD_EPOCH := $(shell date -u +%s)
BUILD_DATE := $(shell date -u -d @$(BUILD_EPOCH) +"%Y-%m-%dT%H:%M:%SZ")
OFFICIAL_SITE := $(shell [ -f site.txt ] && cat site.txt || printf '%s' "${OFFICIAL_SITE:-}")
BINDIR := binaries
RELDIR := releases
GO_CACHE ?= $(HOME)/go/pkg/mod
GO_BUILD ?= $(HOME)/.cache/go-build/$(PROJECT_NAME)
PLATFORMS ?= linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64,freebsd/amd64,freebsd/arm64
GUI_PLATFORMS ?= linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64
DOCKER_MEM ?= 4g
DOCKER_CPUS ?= 2
DOCKER_TIMEOUT ?= 3600
REGISTRY ?= ghcr.io/$(PROJECT_ORG)/$(PROJECT_NAME)
SERVER_LDFLAGS := -s -w -X 'main.Version=$(VERSION)' -X 'main.CommitID=$(COMMIT_ID)' -X 'main.BuildEpoch=$(BUILD_EPOCH)' -X 'main.OfficialSite=$(OFFICIAL_SITE)'
CLIENT_LDFLAGS := -s -w -X 'main.Version=$(VERSION)' -X 'main.CommitID=$(COMMIT_ID)' -X 'main.BuildEpoch=$(BUILD_EPOCH)'
GO_DOCKER_RUN := timeout -k 30 $(DOCKER_TIMEOUT) docker run --rm --no-healthcheck --name $(PROJECT_NAME)-$$(od -An -N4 -tx4 /dev/urandom | tr -d '[:space:]') --memory=$(DOCKER_MEM) --cpus=$(DOCKER_CPUS) -v $(PWD):/app -v $(GO_CACHE):/usr/local/share/go/pkg/mod -v $(GO_BUILD):/usr/local/share/go/cache -w /app -e CGO_ENABLED=0 -e GOFLAGS=-buildvcs=false
GO_DOCKER := $(GO_DOCKER_RUN) casjaysdev/go:latest

.PHONY: dev local build test release docker clean

dev:
	@mkdir -p "$(GO_CACHE)" "$(GO_BUILD)" "$${TMPDIR:-/tmp}/$(PROJECT_ORG)"
	@build_dir=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(INTERNAL_NAME)-XXXXXX") && \
	  $(GO_DOCKER_RUN) -v "$$build_dir:/build" casjaysdev/go:latest sh -c 'set -e; go mod tidy; go generate ./src/swagger; go build -buildvcs=false -trimpath -o /build/$(PROJECT_NAME) ./src; if [ -d src/client ]; then go build -buildvcs=false -trimpath -o /build/$(PROJECT_NAME)-cli ./src/client; fi' && \
	  echo "Development binaries: $$build_dir/$(PROJECT_NAME) and $$build_dir/$(PROJECT_NAME)-cli"

local: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER_RUN) -e SERVER_LDFLAGS="$(SERVER_LDFLAGS)" -e CLIENT_LDFLAGS="$(CLIENT_LDFLAGS)" casjaysdev/go:latest sh -c 'set -e; go mod tidy; go mod download; go generate ./src/swagger; go build -buildvcs=false -trimpath -ldflags "$$SERVER_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME) ./src; if [ -d src/client ]; then go build -buildvcs=false -trimpath -ldflags "$$CLIENT_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli ./src/client; fi'

build: clean
	@mkdir -p $(BINDIR) $(GO_CACHE) $(GO_BUILD)
	@$(GO_DOCKER_RUN) -e SERVER_LDFLAGS="$(SERVER_LDFLAGS)" -e CLIENT_LDFLAGS="$(CLIENT_LDFLAGS)" -e PLATFORMS="$(PLATFORMS)" -e GUI_PLATFORMS="$(GUI_PLATFORMS)" casjaysdev/go:latest sh -c 'set -e; go mod tidy; go mod download; go generate ./src/swagger; go build -buildvcs=false -trimpath -ldflags "$$SERVER_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME) ./src; if [ -d src/client ]; then go build -buildvcs=false -trimpath -ldflags "$$CLIENT_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli ./src/client; fi; for platform in $$(echo "$$PLATFORMS" | tr "," " "); do os=$${platform%/*}; arch=$${platform#*/}; ext=; [ "$$os" = windows ] && ext=.exe; GOOS=$$os GOARCH=$$arch go build -buildvcs=false -trimpath -ldflags "$$SERVER_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-$$os-$$arch$$ext ./src; if [ -d src/client ]; then GOOS=$$os GOARCH=$$arch go build -buildvcs=false -trimpath -ldflags "$$CLIENT_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli-$$os-$$arch$$ext ./src/client; fi; done; for platform in $$(echo "$$GUI_PLATFORMS" | tr "," " "); do os=$${platform%/*}; arch=$${platform#*/}; ext=; [ "$$os" = windows ] && ext=.exe; GOOS=$$os GOARCH=$$arch go build -tags gui -buildvcs=false -trimpath -ldflags "$$CLIENT_LDFLAGS" -o $(BINDIR)/$(PROJECT_NAME)-cli-gui-$$os-$$arch$$ext ./src/client; done'

test:
	@mkdir -p $(GO_CACHE) $(GO_BUILD) "$${TMPDIR:-/tmp}/$(PROJECT_ORG)"
	@$(GO_DOCKER) sh -c 'set -e; mkdir -p "$${TMPDIR:-/tmp}/$(PROJECT_ORG)"; task_tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/$(PROJECT_ORG)/$(INTERNAL_NAME)-XXXXXX"); trap "rm -rf $$task_tmp" EXIT; export TMPDIR="$$task_tmp"; go mod download; go generate ./src/swagger; go vet ./...; govulncheck ./...; go test -timeout 5m -covermode=atomic -coverprofile=$$task_tmp/coverage.out ./...; total=$$(go tool cover -func=$$task_tmp/coverage.out | awk "/^total:/ {gsub(/%/, \"\", \$$3); print \$$3}"); echo "Coverage: $$total%"; awk -v coverage="$$total" "BEGIN { if (coverage+0 < 60) exit 1 }"'

release: build
	@echo "$(VERSION)" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$$' || { echo "release requires stable SemVer (X.Y.Z), got $(VERSION)" >&2; exit 1; }
	@mkdir -p $(RELDIR)
	@echo "$(VERSION)" > $(RELDIR)/version.txt
	@for file in $(BINDIR)/$(PROJECT_NAME)-*; do [ -f "$$file" ] || continue; strip "$$file" 2>/dev/null || true; cp "$$file" $(RELDIR)/; done
	@tar --exclude='.git' --exclude='.github' --exclude='.gitea' --exclude='.forgejo' --exclude='binaries' --exclude='releases' --exclude='*.tar.gz' -czf $(RELDIR)/$(PROJECT_NAME)-$(VERSION)-source.tar.gz .
	@cd $(RELDIR) && files="$$(ls)" && sha256sum $$files > sha256.txt && sha512sum $$files > sha512.txt
	@gh release delete v$(VERSION) --yes 2>/dev/null || true
	@git tag -d v$(VERSION) 2>/dev/null || true
	@git push origin :refs/tags/v$(VERSION) 2>/dev/null || true
	@gh release create v$(VERSION) $(RELDIR)/* --title "v$(VERSION)" --notes "Release v$(VERSION)" --latest

docker:
	@docker buildx version >/dev/null 2>&1 || { echo "docker buildx required" >&2; exit 1; }
	@docker buildx create --name multiarch-builder --use 2>/dev/null || docker buildx use multiarch-builder
	@docker buildx build --platform linux/amd64,linux/arm64 --file docker/Dockerfile --build-arg VERSION="$(VERSION)" --build-arg BUILD_DATE="$(BUILD_DATE)" --build-arg BUILD_EPOCH="$(BUILD_EPOCH)" --build-arg COMMIT_ID="$(COMMIT_ID)" --build-arg OFFICIAL_SITE="$(OFFICIAL_SITE)" --tag $(REGISTRY):$(VERSION) --tag $(REGISTRY):latest --push .

clean:
	@rm -rf $(BINDIR) $(RELDIR)
