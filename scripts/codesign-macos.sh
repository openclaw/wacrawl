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
if [[ ! -f "$binary" ]]; then
  echo "macOS release binary not found: $binary" >&2
  exit 1
fi

codesign --force --options runtime --timestamp \
  --identifier "$identifier" --sign "$identity" "$binary"
codesign --verify --strict -R="$requirement" --verbose=2 "$binary"

signature=$(codesign -dvvv "$binary" 2>&1)
grep -Fx "Identifier=$identifier" <<<"$signature" >/dev/null
grep -Fx "TeamIdentifier=$team_id" <<<"$signature" >/dev/null
grep -Fx "Authority=$expected_authority" <<<"$signature" >/dev/null
if grep -Fx "Signature=adhoc" <<<"$signature" >/dev/null; then
  echo "macOS release binary is ad-hoc signed" >&2
  exit 1
fi
