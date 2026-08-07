# Scheduled imports on macOS

Running `wacrawl import` on a schedule (launchd, cron) keeps the archive fresh without thinking about it. On macOS 15.2 and newer, one platform behavior gets in the way: TCC's app-container protection.

## The prompt loop

WhatsApp Desktop's data lives in another app's container:

```text
~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared
```

The first time a scheduled import reads it, macOS shows:

> **"wacrawl" would like to access data from other apps.**

Clicking **Allow** does not persist for background processes. The consent this dialog grants is session-scoped, and a short-lived launchd process has no session to keep it in — the next scheduled run prompts again, indefinitely. While the dialog waits, the import hangs and the archive goes stale. This is macOS design (`kTCCServiceSystemPolicyAppData`), not something `wacrawl` can change; interactive terminal use is mostly unaffected because the terminal app holds the session.

## The fix: Full Disk Access on a stable path

Full Disk Access on the exact binary that does the reading suppresses the app-data check. TCC identifies bare command-line binaries by **absolute path**, which creates one pitfall: the Homebrew Cellar path changes on every upgrade (`/opt/homebrew/Cellar/wacrawl/<version>/bin/wacrawl`), taking the grant with it.

Instead, run the scheduled job from a copy at a path that never changes. `cp -p` follows the Homebrew symlink and lands the real signed binary:

```bash
mkdir -p "$HOME/wacrawl-scheduled"
cp -p "$(brew --prefix)/bin/wacrawl" "$HOME/wacrawl-scheduled/wacrawl"
```

Add that copy to **System Settings → Privacy & Security → Full Disk Access** (press <kbd>⌘⇧G</kbd> in the file dialog to type a path; a non-hidden folder like the one above is reachable there).

After each `brew upgrade openclaw/tap/wacrawl`, refresh the copy with the same command:

```bash
cp -p "$(brew --prefix)/bin/wacrawl" "$HOME/wacrawl-scheduled/wacrawl"
```

Same path plus the same Developer ID signing identity means the Full Disk Access grant keeps validating — no new prompts.

A side benefit: with Full Disk Access, macOS also skips per-file adjudication of the container's media directory, so imports that crawl for many minutes under the prompt regime complete in seconds.

### Keep the grant narrow

Full Disk Access is much broader than the WhatsApp container, so scope it deliberately:

- Grant it to the dedicated copy only — not to `$(brew --prefix)/bin/wacrawl`, which general shell use and other tooling touch. The privileged binary then exists solely for the scheduled job.
- Confirm what you granted is the signed release build: `codesign -dv "$HOME/wacrawl-scheduled/wacrawl"` should report `Identifier=org.openclaw.wacrawl` and `TeamIdentifier=FWJYW4S8P8`.
- If you stop scheduling imports, remove the entry from Full Disk Access and delete the copy.

## The launchd job

launchd performs **no shell expansion**: a `~` inside `ProgramArguments` is passed literally and the job dies before it starts. Use absolute paths. A minimal `~/Library/LaunchAgents/com.example.wacrawl-import.plist` (replace `USERNAME`):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.example.wacrawl-import</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Users/USERNAME/wacrawl-scheduled/wacrawl</string>
		<string>import</string>
	</array>
	<key>StartInterval</key>
	<integer>7200</integer>
	<key>StandardOutPath</key>
	<string>/tmp/wacrawl-import.log</string>
	<key>StandardErrorPath</key>
	<string>/tmp/wacrawl-import.log</string>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
```

Load it once (shell expansion is fine here — this runs in your shell, not in launchd):

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.wacrawl-import.plist
```

Check the last run and exit status at any time:

```bash
launchctl print gui/$(id -u)/com.example.wacrawl-import
tail /tmp/wacrawl-import.log
```

## If the job dies with OS_REASON_CODESIGNING

launchd records a lightweight code requirement for a job's binary when the job is registered. If the binary's signing *identity* later changes at the same path (for example, replacing a pre-0.3.6 ad-hoc build with a current Developer ID release), the kernel kills the new binary at spawn. `launchctl print gui/$(id -u)/<label>` shows:

```text
last exit reason = OS_REASON_CODESIGNING
```

Re-register the job once and it clears:

```bash
launchctl bootout gui/$(id -u)/com.example.wacrawl-import
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.wacrawl-import.plist
```

Routine upgrades that keep the same signing identity do not trip this.
