# Binary Rules (PART 7, 8, 32)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never run or build Go binaries on the host; use the specified container or isolated test environment.
- Never expose secrets in command-line output, diagnostics, or persistent client configuration without the specified protection.
- Never make the CLI silently choose an unsupported language, display mode, or server API version.

## CRITICAL - ALWAYS DO

- Build the server and CLI with the required static, CGO-disabled settings and platform naming.
- Keep help, version, exit codes, signals, output formats, and `NO_COLOR` behavior consistent with the CLI contract.
- Implement the client connection, discovery, and platform-specific GUI/TUI behavior required by IDEA.md and AI.md.

## Key rules

- The server CLI must validate flags and dispatch maintenance/service operations through the specified paths.
- The companion client discovers the active API version and uses documented authentication and output precedence.
- All user-facing CLI strings use the shared translation registry.

For complete details, see AI.md PART 7, 8, 32.
