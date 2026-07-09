#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
version=${1:-}
expected_authority='Developer ID Application: OpenClaw Foundation (FWJYW4S8P8)'

usage() {
  echo "usage: $0 vX.Y.Z" >&2
  exit 2
}

[[ "$#" -eq 1 && "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] || usage
[[ "$(uname -s)" == Darwin ]] || {
  echo "official wacrawl release packaging must run on macOS" >&2
  exit 1
}
[[ "$(uname -m)" == arm64 ]] || {
  echo "official wacrawl release packaging requires Apple Silicon with Rosetta" >&2
  exit 1
}
[[ "${CODESIGN_IDENTITY:-}" == "$expected_authority" ]] || {
  echo "official wacrawl releases require $expected_authority" >&2
  exit 1
}

for tool in codesign git go goreleaser lipo shasum tar; do
  command -v "$tool" >/dev/null || {
    echo "missing required tool: $tool" >&2
    exit 1
  }
done

head_commit=$(git -C "$root" rev-parse HEAD)
tag_commit=$(git -C "$root" rev-parse "refs/tags/$version^{commit}" 2>/dev/null) || {
  echo "release tag does not exist locally: $version" >&2
  exit 1
}
[[ "$head_commit" == "$tag_commit" ]] || {
  echo "HEAD does not match release tag $version" >&2
  exit 1
}
[[ -z "$(git -C "$root" status --porcelain --untracked-files=normal)" ]] || {
  echo "release checkout is not clean" >&2
  exit 1
}
git -C "$root" tag -v "$version" >/dev/null 2>&1 || {
  echo "release tag is not signed by a trusted git signing key: $version" >&2
  exit 1
}

(
  cd "$root"
  WACRAWL_REQUIRE_CODESIGN=1 \
    WACRAWL_CODESIGN_IDENTITY="$CODESIGN_IDENTITY" \
    goreleaser release --clean --skip=publish
)

release_version=${version#v}
arm64="$root/dist/wacrawl_${release_version}_darwin_arm64.tar.gz"
amd64="$root/dist/wacrawl_${release_version}_darwin_amd64.tar.gz"
"$root/scripts/verify-macos-release.sh" "$version" "$arm64" "$amd64"
