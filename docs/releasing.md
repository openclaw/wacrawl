# Release process

Official releases run in GitHub Actions through the shared OpenClaw release workflow. The workflow freezes the protected `main` commit, verifies the pre-created SSH-signed version tag, builds every archive, signs and notarizes the macOS binaries with organization credentials, verifies the unpublished draft on both macOS architectures, publishes the verified bytes, and dispatches the Homebrew update.

## Dispatch

The changelog section for the version must be dated before dispatch. Run the dry run below against the release-prep commit first and require it to pass.

Then create an annotated SSH-signed tag at that exact commit using the fleet release key. Verify it against the committed allowlist before pushing; never move an existing tag:

```bash
set -e
git -c gpg.format=ssh -c user.signingkey=/path/to/fleet-release-key tag -s vX.Y.Z <release-prep-commit> -m "Release vX.Y.Z"
git -c gpg.format=ssh -c gpg.ssh.allowedSignersFile=.github/release-allowed-signers verify-tag vX.Y.Z
git push origin refs/tags/vX.Y.Z
```

Dispatch the workflow after the signed tag is pushed:

```bash
gh workflow run release-unified.yml \
  --repo openclaw/wacrawl \
  --ref main \
  -f version=X.Y.Z
```

Tag-signature verification, Developer ID and App Store Connect credentials, draft publication, artifact verification, and Homebrew handoff belong to the workflow. `make release VERSION=vX.Y.Z` deliberately refuses local publication and prints the official command.

To validate the current source and signed build pipeline without creating a tag, publishing a release, or updating Homebrew, use the prepared version (or the current released version for a pipeline-only check) with `dry_run`:

```bash
gh workflow run release-unified.yml \
  --repo openclaw/wacrawl \
  --ref main \
  -f version=0.4.4 \
  -f dry_run=true
```

Dry runs use the selected version's changelog section without requiring or creating its tag. Real releases require a new, dated version section and a valid fleet-key signature on the tag.

## Local verification

Local builds and snapshots require no signing credentials:

```bash
make build
make snapshot
make verify-release VERSION=vX.Y.Z
```

They are suitable for development and cross-platform CI, but they are not official signed macOS artifacts.

The release publishes archives for:

```text
darwin/amd64
darwin/arm64
linux/amd64
linux/arm64
windows/amd64
windows/arm64
```

Starting with v0.3.10, macOS binaries require macOS 13 or newer. v0.3.9 is the last release supporting macOS 12.

The Homebrew formula lives in the [`openclaw/homebrew-tap`](https://github.com/openclaw/homebrew-tap/blob/main/Formula/wacrawl.rb) repository.
