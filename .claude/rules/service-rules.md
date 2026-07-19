# Service Rules (PART 33-36, general)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never write PID files or service unit files to the wrong OS-specific path
- Never assume systemd is present — check platform (systemd/rc.d/LaunchDaemons/Windows Service)

## CRITICAL - ALWAYS DO
- Linux service: `/etc/systemd/system/{internal_name}.service`
- macOS service: `/Library/LaunchDaemons/{plist_name}.plist` (root) or
  `~/Library/LaunchAgents/{plist_name}.plist` (user)
- BSD service: `/usr/local/etc/rc.d/{internal_name}`
- Windows service: Windows Service Manager

## Summary
Service install/uninstall logic must branch per-OS using the same path
conventions defined in PART 4.

For complete details, see AI.md PART 33-36.
