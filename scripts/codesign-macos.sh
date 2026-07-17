#!/usr/bin/env bash
set -euo pipefail

binary=${1:-}
require_signing=${WACRAWL_REQUIRE_CODESIGN:-0}
identity=${WACRAWL_CODESIGN_IDENTITY:-${CODESIGN_IDENTITY:-}}
identifier=org.openclaw.wacrawl
expected_authority='Developer ID Application: OpenClaw Foundation (FWJYW4S8P8)'
team_id=FWJYW4S8P8
requirement="identifier \"$identifier\" and anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = \"$team_id\""

if [[ -z "$binary" ]]; then
  echo "usage: $0 <path-to-binary>" >&2
  exit 2
fi
if [[ "$require_signing" != 0 && "$require_signing" != 1 ]]; then
  echo "WACRAWL_REQUIRE_CODESIGN must be 0 or 1" >&2
  exit 2
fi
if [[ "$require_signing" != 1 ]]; then
  exit 0
fi
if [[ "$(uname -s)" != Darwin ]]; then
  echo "official macOS release signing must run on macOS" >&2
  exit 1
fi
if [[ -z "$identity" ]]; then
  echo "official macOS release signing requires WACRAWL_CODESIGN_IDENTITY" >&2
  exit 1
fi
if [[ "$identity" != "$expected_authority" ]]; then
  echo "official macOS releases require $expected_authority" >&2
  exit 1
fi
if [[ -z "${NOTARYTOOL_KEYCHAIN_PROFILE:-}" ]]; then
  echo "official macOS release signing requires NOTARYTOOL_KEYCHAIN_PROFILE" >&2
  exit 1
fi
if [[ ! -f "$binary" ]]; then
  echo "macOS release binary not found: $binary" >&2
  exit 1
fi

for tool in codesign cp ditto mktemp mv plutil xcrun; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "missing required tool: $tool" >&2
    exit 1
  }
done

binary_dir=$(cd "$(dirname "$binary")" && pwd)
binary_name=$(basename "$binary")
work_dir=$(mktemp -d "$binary_dir/.wacrawl-notary.XXXXXX")
candidate="$work_dir/$binary_name"
submission="$work_dir/$binary_name.zip"
trap 'rm -rf "$work_dir"' EXIT

# Do not replace the release artifact until signing, notarization, and Apple's
# assessment have all succeeded.
cp -p "$binary" "$candidate"
codesign --force --sign "$identity" --timestamp --options runtime \
  --identifier "$identifier" "$candidate"

ditto -c -k --sequesterRsrc --keepParent "$candidate" "$submission"
notary_result=$(xcrun notarytool submit "$submission" \
  --keychain-profile "$NOTARYTOOL_KEYCHAIN_PROFILE" \
  --no-s3-acceleration \
  --wait \
  --output-format json)
notary_status=$(plutil -extract status raw -o - - <<<"$notary_result")
notary_id=$(plutil -extract id raw -o - - <<<"$notary_result")
if [[ "$notary_status" != Accepted ]]; then
  echo "macOS release notarization status is ${notary_status:-missing}, expected Accepted" >&2
  exit 1
fi
if [[ ! "$notary_id" =~ ^[[:xdigit:]]{8}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{4}-[[:xdigit:]]{12}$ ]]; then
  echo "macOS release notarization response has an invalid submission id" >&2
  exit 1
fi

codesign --verify --strict -R="$requirement" --verbose=2 "$candidate"

signature=$(codesign -dvvv "$candidate" 2>&1)
grep -Fx "Identifier=$identifier" <<<"$signature" >/dev/null
grep -Fx "TeamIdentifier=$team_id" <<<"$signature" >/dev/null
grep -Fx "Authority=$expected_authority" <<<"$signature" >/dev/null
grep -Eq '^CodeDirectory .*flags=.*\([^)]*runtime[^)]*\)' <<<"$signature" || {
  echo "macOS release binary is missing the hardened runtime" >&2
  exit 1
}
if grep -Fx "Signature=adhoc" <<<"$signature" >/dev/null; then
  echo "macOS release binary is ad-hoc signed" >&2
  exit 1
fi
codesign --verify --strict --check-notarization -R=notarized "$candidate"

mv -f "$candidate" "$binary"
