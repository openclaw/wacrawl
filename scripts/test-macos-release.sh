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
printf 'codesign %s\n' "$*" >> "${MOCK_CODESIGN_LOG:?}"
case " $* " in
  *' --sign '*)
    printf '\n# mock signed\n' >> "${!#}"
    ;;
  *' --check-notarization '*)
    [[ "${MOCK_NOTARY_TICKET:-accepted}" == accepted ]]
    ;;
  *' -dvvv '*)
    {
      if [[ "${MOCK_CODESIGN_RUNTIME:-present}" == present ]]; then
        echo 'CodeDirectory v=20500 size=512 flags=0x10000(runtime) hashes=1+0 location=embedded'
      else
        echo 'CodeDirectory v=20500 size=512 flags=0x0(none) hashes=1+0 location=embedded'
      fi
      echo 'Identifier=org.openclaw.wacrawl'
      echo "Authority=${MOCK_CODESIGN_AUTHORITY:-Developer ID Application: OpenClaw Foundation (FWJYW4S8P8)}"
      echo 'TeamIdentifier=FWJYW4S8P8'
    } >&2
    ;;
esac
EOF

cat > "$fake_bin/ditto" <<'EOF'
#!/usr/bin/env bash
printf 'ditto %s\n' "$*" >> "${MOCK_DITTO_LOG:?}"
while (( $# > 2 )); do
  shift
done
cp "$1" "$2"
EOF

cat > "$fake_bin/xcrun" <<'EOF'
#!/usr/bin/env bash
printf 'xcrun %s\n' "$*" >> "${MOCK_XCRUN_LOG:?}"
printf '{"id":"%s","status":"%s"}\n' \
  "${MOCK_NOTARY_ID:-12345678-1234-1234-1234-123456789abc}" \
  "${MOCK_NOTARY_STATUS:-Accepted}"
EOF

cat > "$fake_bin/plutil" <<'EOF'
#!/usr/bin/env bash
cat >/dev/null
case "${2:-}" in
  status) printf '%s\n' "${MOCK_NOTARY_STATUS:-Accepted}" ;;
  id) printf '%s\n' "${MOCK_NOTARY_ID:-12345678-1234-1234-1234-123456789abc}" ;;
  *) exit 2 ;;
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
unset NOTARYTOOL_KEYCHAIN_PROFILE
export MOCK_CODESIGN_LOG="$work_dir/codesign.log"
export MOCK_DITTO_LOG="$work_dir/ditto.log"
export MOCK_XCRUN_LOG="$work_dir/xcrun.log"
: > "$MOCK_CODESIGN_LOG"
: > "$MOCK_DITTO_LOG"
: > "$MOCK_XCRUN_LOG"

probe_seed="$work_dir/probe-seed"
probe="$work_dir/probe"
printf '#!/usr/bin/env bash\nexit 0\n' > "$probe_seed"
chmod 0755 "$probe_seed"
cp "$probe_seed" "$probe"
if WACRAWL_REQUIRE_CODESIGN=1 WACRAWL_CODESIGN_IDENTITY="$expected_authority" \
  "$root/scripts/codesign-macos.sh" "$probe" >/dev/null 2>&1; then
  fail "missing notarytool profile was accepted"
fi
cmp "$probe_seed" "$probe" || fail "missing profile changed the release binary"
if WACRAWL_REQUIRE_CODESIGN=1 \
  WACRAWL_CODESIGN_IDENTITY='Developer ID Application: Peter Steinberger (Y5PE65HELJ)' \
  "$root/scripts/codesign-macos.sh" "$probe" >/dev/null 2>&1; then
  fail "personal signing identity was accepted"
fi
cp "$probe_seed" "$probe"
if MOCK_NOTARY_STATUS=Rejected WACRAWL_REQUIRE_CODESIGN=1 \
  WACRAWL_CODESIGN_IDENTITY="$expected_authority" NOTARYTOOL_KEYCHAIN_PROFILE=test-profile \
  "$root/scripts/codesign-macos.sh" "$probe" >/dev/null 2>&1; then
  fail "rejected notarization was accepted"
fi
cmp "$probe_seed" "$probe" || fail "rejected notarization changed the release binary"
cp "$probe_seed" "$probe"
if MOCK_NOTARY_TICKET=rejected WACRAWL_REQUIRE_CODESIGN=1 \
  WACRAWL_CODESIGN_IDENTITY="$expected_authority" NOTARYTOOL_KEYCHAIN_PROFILE=test-profile \
  "$root/scripts/codesign-macos.sh" "$probe" >/dev/null 2>&1; then
  fail "failed Apple notarization assessment was accepted"
fi
cmp "$probe_seed" "$probe" || fail "failed assessment changed the release binary"
cp "$probe_seed" "$probe"
if MOCK_CODESIGN_RUNTIME=missing WACRAWL_REQUIRE_CODESIGN=1 \
  WACRAWL_CODESIGN_IDENTITY="$expected_authority" NOTARYTOOL_KEYCHAIN_PROFILE=test-profile \
  "$root/scripts/codesign-macos.sh" "$probe" >/dev/null 2>&1; then
  fail "missing hardened runtime was accepted"
fi
cmp "$probe_seed" "$probe" || fail "runtime verification failure changed the release binary"
cp "$probe_seed" "$probe"
WACRAWL_REQUIRE_CODESIGN=1 WACRAWL_CODESIGN_IDENTITY="$expected_authority" \
  NOTARYTOOL_KEYCHAIN_PROFILE=test-profile \
  "$root/scripts/codesign-macos.sh" "$probe"
cmp "$probe_seed" "$probe" >/dev/null 2>&1 && fail "successful signing did not replace the binary"
grep -F 'notarytool submit ' "$MOCK_XCRUN_LOG" >/dev/null
grep -F -- '--keychain-profile test-profile' "$MOCK_XCRUN_LOG" >/dev/null
grep -F -- '--wait --output-format json' "$MOCK_XCRUN_LOG" >/dev/null
grep -F -- '--check-notarization -R=notarized' "$MOCK_CODESIGN_LOG" >/dev/null
if find "$work_dir" -maxdepth 1 -name '.wacrawl-notary.*' -print -quit | grep -q .; then
  fail "notarization scratch directory was not removed"
fi
if CODESIGN_IDENTITY='Developer ID Application: Peter Steinberger (Y5PE65HELJ)' \
  "$root/scripts/package-wacrawl-release.sh" v0.3.4 >/dev/null 2>&1; then
  fail "personal signing identity reached release packaging"
fi
if package_output=$(CODESIGN_IDENTITY="$expected_authority" \
  "$root/scripts/package-wacrawl-release.sh" v0.3.4 2>&1); then
  fail "release packaging accepted a missing notarytool profile"
fi
grep -F 'require NOTARYTOOL_KEYCHAIN_PROFILE' <<<"$package_output" >/dev/null || \
  fail "release packaging did not explain the missing notarytool profile"

assets="$work_dir/assets"
mkdir -p "$assets"
for arch in arm64 amd64; do
  stage="$work_dir/stage-$arch"
  mkdir -p "$stage"
  cp "$root/CHANGELOG.md" "$root/LICENSE" "$root/README.md" "$stage/"
  cat > "$stage/wacrawl" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == --version ]] || exit 2
echo 0.3.4
EOF
  chmod 0755 "$stage/wacrawl"
  archive="$assets/wacrawl_0.3.4_darwin_${arch}.tar.gz"
  tar -czf "$archive" -C "$stage" CHANGELOG.md LICENSE README.md wacrawl
  (
    cd "$assets"
    shasum -a 256 "$(basename "$archive")" >> checksums.txt
  )
done

: > "$MOCK_CODESIGN_LOG"
"$root/scripts/verify-macos-release.sh" v0.3.4 \
  "$assets/wacrawl_0.3.4_darwin_arm64.tar.gz" \
  "$assets/wacrawl_0.3.4_darwin_amd64.tar.gz"
grep -F -- '--check-notarization -R=notarized' "$MOCK_CODESIGN_LOG" >/dev/null || \
  fail "release verification skipped the Apple notarization assessment"
if MOCK_NOTARY_TICKET=rejected \
  "$root/scripts/verify-macos-release.sh" v0.3.4 \
  "$assets/wacrawl_0.3.4_darwin_arm64.tar.gz" >/dev/null 2>&1; then
  fail "release verification accepted an unnotarized binary"
fi
if MOCK_CODESIGN_AUTHORITY='Developer ID Application: Peter Steinberger (Y5PE65HELJ)' \
  "$root/scripts/verify-macos-release.sh" v0.3.4 \
  "$assets/wacrawl_0.3.4_darwin_arm64.tar.gz" >/dev/null 2>&1; then
  fail "personal signature was accepted"
fi

mock_assets="$work_dir/gh-assets"
mkdir -p "$mock_assets"
cp "$assets/checksums.txt" "$mock_assets/1"
cp "$assets/wacrawl_0.3.4_darwin_arm64.tar.gz" "$mock_assets/2"
cp "$assets/wacrawl_0.3.4_darwin_amd64.tar.gz" "$mock_assets/3"
for id in 4 5 6 7; do
  printf 'fixture %s\n' "$id" > "$mock_assets/$id"
done
mock_releases="$work_dir/releases.json"
mock_asset_list="$work_dir/release-assets.json"
cat > "$mock_releases" <<'EOF'
[{"id":42,"tag_name":"v0.3.4","draft":true}]
EOF
cat > "$mock_asset_list" <<'EOF'
[
  {"name":"checksums.txt","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/1"},
  {"name":"wacrawl_0.3.4_darwin_arm64.tar.gz","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/2"},
  {"name":"wacrawl_0.3.4_darwin_amd64.tar.gz","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/3"},
  {"name":"wacrawl_0.3.4_linux_arm64.tar.gz","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/4"},
  {"name":"wacrawl_0.3.4_linux_amd64.tar.gz","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/5"},
  {"name":"wacrawl_0.3.4_windows_arm64.zip","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/6"},
  {"name":"wacrawl_0.3.4_windows_amd64.zip","url":"https://api.github.com/repos/openclaw/wacrawl/releases/assets/7"}
]
EOF
export MOCK_GH_ASSET_DIR="$mock_assets"
export MOCK_GH_RELEASES_JSON="$mock_releases"
export MOCK_GH_ASSETS_JSON="$mock_asset_list"
api_download="$work_dir/api-download"
GITHUB_REPOSITORY=openclaw/wacrawl GH_TOKEN=test \
  "$root/scripts/download-release-assets.sh" v0.3.4 arm64 true "$api_download"
cmp "$assets/checksums.txt" "$api_download/checksums.txt"
cmp "$assets/wacrawl_0.3.4_darwin_arm64.tar.gz" \
  "$api_download/wacrawl_0.3.4_darwin_arm64.tar.gz"
if GITHUB_REPOSITORY=openclaw/wacrawl GH_TOKEN=test \
  "$root/scripts/download-release-assets.sh" v0.3.4 arm64 false "$work_dir/wrong-draft" \
    >/dev/null 2>&1; then
  fail "draft release matched published-release lookup"
fi
invalid_assets="$work_dir/invalid-release-assets.json"
jq 'map(if .name == "wacrawl_0.3.4_linux_amd64.tar.gz" then .url = "https://example.invalid/5" else . end)' \
  "$mock_asset_list" > "$invalid_assets"
if MOCK_GH_ASSETS_JSON="$invalid_assets" GITHUB_REPOSITORY=openclaw/wacrawl GH_TOKEN=test \
  "$root/scripts/download-release-assets.sh" v0.3.4 arm64 true "$work_dir/invalid-url" \
    >/dev/null 2>&1; then
  fail "invalid release asset API URL was accepted"
fi

echo "macOS release script tests passed"
