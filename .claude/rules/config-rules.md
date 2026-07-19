# Configuration Rules (PART 4, 5)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never name the config file `server.yaml` — it is always `server.yml`
- Never use inline YAML comments — comments go ABOVE the setting, never same-line
- Never mix Docker paths (`/config`, `/data`) with native OS paths in the same code path
- Never store user accounts or operator-editable config in the database

## CRITICAL - ALWAYS DO
- Use OS-specific paths per platform: Linux (`/etc`, `/var/lib`, `/var/log` root;
  `~/.config`, `~/.local/share` user), macOS (`/Library/Application Support`,
  `/Library/Logs` root; `~/Library/Application Support` user — NOT XDG paths),
  BSD (`/usr/local/etc`, `/var/db`), Windows (`%ProgramData%`, `%AppData%`)
  — never collapse macOS into the Linux/BSD default branch
  - **Known gap in this project**: `src/paths/paths.go` currently routes macOS
    through the Linux XDG branch instead of `Library/Application Support`
- Docker/container paths are always `/config/{project_name}/` and `/data/{project_name}/`
  (never flat `/config`, `/data`, `/logs`) with logs at `/data/log/{project_name}/`
- `server.yml` is the sole source of truth for configuration; database stores
  resource state/tokens/audit log only
- Validate and normalize all paths (CLI flags, API params, HTTP request paths)
  through `SafePath`/`validatePath` before use — block `..` traversal

## Summary
Config file is always `server.yml`. Path security functions must guard every
user-controlled path. macOS and Docker each need their own path branch —
never reuse Linux/BSD or flat container paths for them.

For complete details, see AI.md PART 4, 5.
