## [ ] Implement tests/run_tests.sh, tests/docker.sh, tests/incus.sh
Read: AI.md PART 28

## [ ] Create docs/ ReadTheDocs structure (index.md, installation.md, configuration.md, api.md, cli.md, security.md, integrations.md, development.md, stylesheets/, requirements.txt, mkdocs.yml, .readthedocs.yaml)
Read: AI.md PART 29

## [ ] Populate .github/workflows/ (release.yml, beta.yml, daily.yml, docker.yml) plus security-only workflows first
Read: AI.md PART 27

## [ ] Add Jenkinsfile
Read: AI.md PART 27

## [ ] Create src/client/ for gitmessages-cli binary
Read: AI.md PART 32

## [ ] Reconcile duplicate docker-compose.yml — root-level file conflicts with docker/docker-compose.yml (port 64080 vs 64580, different volume paths, stray DB_TYPE=sqlite contradicting IDEA.md's no-DB design); only docker/docker-compose.yml is spec-mandated
Read: AI.md PART 26

## [ ] Fix src/paths/paths.go macOS branch — currently routed through Linux XDG paths instead of ~/Library/Application Support, ~/Library/Caches, ~/Library/Logs (user and root)
Read: AI.md PART 4

## [ ] Fix src/paths/paths.go container paths — uses flat /config, /data, /logs instead of spec's /config/{project_name}/, /data/{project_name}/, /data/log/{project_name}/
Read: AI.md PART 4

## [ ] Add independent Debug flag/state to src/mode/mode.go, decoupled from the Mode enum (currently debug endpoints are tied directly to Development mode); add devel and debug shortcuts to ParseMode
Read: AI.md PART 6

## [ ] Populate .github/ policy files (CODEOWNERS, SECURITY.md, ISSUE_TEMPLATE/*, PULL_REQUEST_TEMPLATE) — .github/ currently empty
Read: AI.md PART 1
