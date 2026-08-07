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

Full Disk Access on the exact binary that does the reading suppresses the app-data check entirely. TCC identifies bare command-line binaries by **absolute path**, which creates one pitfall: the Homebrew Cellar path changes on every upgrade (`/opt/homebrew/Cellar/wacrawl/<version>/bin/wacrawl`), taking the grant with it.

Instead, run the scheduled job from a copy at a path that never changes:

```bash
mkdir -p ~/wacrawl-scheduled
cp -p "$(brew --prefix)/Cellar/wacrawl/$(wacrawl --version | awk '{print $NF}')/bin/wacrawl" \
      ~/wacrawl-scheduled/wacrawl
```

Point your launchd job's `ProgramArguments` at `~/wacrawl-scheduled/wacrawl`, then add that file to **System Settings → Privacy & Security → Full Disk Access** (press <kbd>⌘⇧G</kbd> in the file dialog to type a path; pick a non-hidden folder so it is reachable there).

After each `brew upgrade openclaw/tap/wacrawl`, refresh the copy:

```bash
cp -p "$(brew --prefix)/bin/wacrawl" ~/wacrawl-scheduled/wacrawl
```

Same path plus the same Developer ID signing identity means the Full Disk Access grant keeps validating — no new prompts.

A side benefit: with Full Disk Access, macOS also skips per-file adjudication of the container's media directory, so imports that crawl for many minutes under the prompt regime complete in seconds.

## If the job dies with OS_REASON_CODESIGNING

launchd records a lightweight code requirement for a job's binary when the job is registered. If the binary's signing *identity* later changes at the same path (for example, replacing a pre-0.3.6 ad-hoc build with a current Developer ID release), the kernel kills the new binary at spawn. `launchctl print gui/$UID/<label>` shows:

```text
last exit reason = OS_REASON_CODESIGNING
```

Re-register the job once and it clears:

```bash
launchctl bootout gui/$UID/<label>
launchctl bootstrap gui/$UID ~/Library/LaunchAgents/<label>.plist
```

Routine upgrades that keep the same signing identity do not trip this.
