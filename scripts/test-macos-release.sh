#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
expected_authority='Developer ID Application: OpenClaw Foundation (FWJYW4S8P8)'

fail() {
  echo "macOS release script test failed: $*" >&2
  exit 1
}

for script in codesign-macos.sh download-release-assets.sh package-wacrawl-release.sh verify-macos-release.sh; do
  bash -n "$root/scripts/$script"
done
grep -F "github.event_name == 'release' ||" "$root/.github/workflows/release.yml" >/dev/null
grep -F 'persist-credentials: false' "$root/.github/workflows/release.yml" >/dev/null
grep -F 'path: trusted' "$root/.github/workflows/release.yml" >/dev/null

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/wacrawl-release-test.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT
fake_bin="$work_dir/bin"
mkdir -p "$fake_bin"

cat > "$fake_bin/uname" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in
  -s) echo Darwin ;;
  -m) echo arm64 ;;
  *) echo Darwin ;;
esac
EOF

cat > "$fake_bin/codesign" <<'EOF'
#!/usr/bin/env bash
case " $* " in
  *' -dvvv '*)
    {
      echo 'Identifier=org.openclaw.wacrawl'
      echo "Authority=${MOCK_CODESIGN_AUTHORITY:-Developer ID Application: OpenClaw Foundation (FWJYW4S8P8)}"
      echo 'TeamIdentifier=FWJYW4S8P8'
    } >&2
    ;;
esac
EOF

cat > "$fake_bin/lipo" <<'EOF'
#!/usr/bin/env bash
case "${2:-}" in
  */x86_64/*) echo x86_64 ;;
  *) echo arm64 ;;
esac
EOF

cat > "$fake_bin/gh" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == api ]] || exit 2
shift
if [[ "${1:-}" == --paginate ]]; then
  shift
fi
endpoint=${1:-}
case "$endpoint" in
  repos/*/releases\?per_page=100) cat "${MOCK_GH_RELEASES_JSON:?}" ;;
  repos/*/releases/*/assets\?per_page=100) cat "${MOCK_GH_ASSETS_JSON:?}" ;;
  https://api.github.com/repos/*/releases/assets/*) cat "${MOCK_GH_ASSET_DIR:?}/${endpoint##*/}" ;;
  *) exit 2 ;;
esac
EOF

chmod 0755 "$fake_bin"/*
export PATH="$fake_bin:$PATH"

probe="$work_dir/probe"
printf '#!/usr/bin/env bash\nexit 0\n' > "$probe"
chmod 0755 "$probe"
WACRAWL_REQUIRE_CODESIGN=1 WACRAWL_CODESIGN_IDENTITY="$expected_authority" \
  "$root/scripts/codesign-macos.sh" "$probe"
if WACRAWL_REQUIRE_CODESIGN=1 \
  WACRAWL_CODESIGN_IDENTITY='Developer ID Application: Peter Steinberger (Y5PE65HELJ)' \
  "$root/scripts/codesign-macos.sh" "$probe" >/dev/null 2>&1; then
  fail "personal signing identity was accepted"
fi
if CODESIGN_IDENTITY='Developer ID Application: Peter Steinberger (Y5PE65HELJ)' \
  "$root/scripts/package-wacrawl-release.sh" v0.3.2 >/dev/null 2>&1; then
  fail "personal signing identity reached release packaging"
fi

assets="$work_dir/assets"
mkdir -p "$assets"
for arch in arm64 amd64; do
  stage="$work_dir/stage-$arch"
  mkdir -p "$stage"
  cp "$root/CHANGELOG.md" "$root/LICENSE" "$root/README.md" "$stage/"
  cat > "$stage/wacrawl" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == --version ]] || exit 2
echo 0.3.2
EOF
  chmod 0755 "$stage/wacrawl"
  archive="$assets/wacrawl_0.3.2_darwin_${arch}.tar.gz"
  tar -czf "$archive" -C "$stage" CHANGELOG.md LICENSE README.md wacrawl
  (
    cd "$assets"
    shasum -a 256 "$(basename "$archive")" >> checksums.txt
  )
done

"$root/scripts/verify-macos-release.sh" v0.3.2 \
  "$assets/wacrawl_0.3.2_darwin_arm64.tar.gz" \
  "$assets/wacrawl_0.3.2_darwin_amd64.tar.gz"
if MOCK_CODESIGN_AUTHORITY='Developer ID Application: Peter Steinberger (Y5PE65HELJ)' \
  "$root/scripts/verify-macos-release.sh" v0.3.2 \
  "$assets/wacrawl_0.3.2_darwin_arm64.tar.gz" >/dev/null 2>&1; then
  fail "personal signature was accepted"
fi

mock_assets="$work_dir/gh-assets"
mkdir -p "$mock_assets"
cp "$assets/checksums.txt" "$mock_assets/1"
cp "$assets/wacrawl_0.3.2_darwin_arm64.tar.gz" "$mock_assets/2"
cp "$assets/wacrawl_0.3.2_darwin_amd64.tar.gz" "$mock_assets/3"
for id in 4 5 6 7; do
  printf 'fixture %s\n' "$id" > "$mock_assets/$id"
done
mock_releases="$work_dir/releases.json"
mock_asset_list="$work_dir/release-assets.json"
cat > "$mock_releases" <<'EOF'
[{"id":42,"tag_name":"v0.3.2","draft":true}]
EOF
cat > "$mock_asset_list" <<'EOF'
[
  {"name":"checksums.txt","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/1"},
  {"name":"wacrawl_0.3.2_darwin_arm64.tar.gz","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/2"},
  {"name":"wacrawl_0.3.2_darwin_amd64.tar.gz","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/3"},
  {"name":"wacrawl_0.3.2_linux_arm64.tar.gz","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/4"},
  {"name":"wacrawl_0.3.2_linux_amd64.tar.gz","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/5"},
  {"name":"wacrawl_0.3.2_windows_arm64.zip","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/6"},
  {"name":"wacrawl_0.3.2_windows_amd64.zip","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/7"}
]
EOF
export MOCK_GH_ASSET_DIR="$mock_assets"
export MOCK_GH_RELEASES_JSON="$mock_releases"
export MOCK_GH_ASSETS_JSON="$mock_asset_list"
api_download="$work_dir/api-download"
GITHUB_REPOSITORY=openclaw/wacrawl GH_TOKEN=test \
  "$root/scripts/download-release-assets.sh" v0.3.2 arm64 true "$api_download"
cmp "$assets/checksums.txt" "$api_download/checksums.txt"
cmp "$assets/wacrawl_0.3.2_darwin_arm64.tar.gz" \
  "$api_download/wacrawl_0.3.2_darwin_arm64.tar.gz"
if GITHUB_REPOSITORY=openclaw/wacrawl GH_TOKEN=test \
  "$root/scripts/download-release-assets.sh" v0.3.2 arm64 false "$work_dir/wrong-draft" \
    >/dev/null 2>&1; then
  fail "draft release matched published-release lookup"
fi
invalid_assets="$work_dir/invalid-release-assets.json"
jq 'map(if .name == "wacrawl_0.3.2_linux_amd64.tar.gz" then .url = "https://example.invalid/5" else . end)' \
  "$mock_asset_list" > "$invalid_assets"
if MOCK_GH_ASSETS_JSON="$invalid_assets" GITHUB_REPOSITORY=openclaw/wacrawl GH_TOKEN=test \
  "$root/scripts/download-release-assets.sh" v0.3.2 arm64 true "$work_dir/invalid-url" \
    >/dev/null 2>&1; then
  fail "invalid release asset API URL was accepted"
fi

echo "macOS release script tests passed"
