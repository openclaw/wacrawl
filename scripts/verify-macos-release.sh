#!/usr/bin/env bash
set -euo pipefail

version=${1:-}
shift || true
identifier=org.openclaw.wacrawl
expected_authority='Developer ID Application: OpenClaw Foundation (FWJYW4S8P8)'
team_id=FWJYW4S8P8
requirement="identifier \"$identifier\" and anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = \"$team_id\""

verify_checksum() {
  local archive_path=$1 checksum_path=$2 expected_hash expected_name extra actual_hash matches
  matches=$(awk -v name="$(basename "$archive_path")" '$2 == name { print }' "$checksum_path")
  [[ -n "$matches" && "$(wc -l <<<"$matches" | tr -d ' ')" == 1 ]] || {
    echo "missing or duplicate checksum for $(basename "$archive_path")" >&2
    return 1
  }
  read -r expected_hash expected_name extra <<<"$matches"
  [[ "$expected_hash" =~ ^[[:xdigit:]]{64}$ && "$expected_name" == "$(basename "$archive_path")" && -z "${extra:-}" ]] || {
    echo "invalid checksum record for $(basename "$archive_path")" >&2
    return 1
  }
  actual_hash=$(shasum -a 256 "$archive_path" | awk '{print $1}')
  [[ "$actual_hash" == "$expected_hash" ]] || {
    echo "checksum mismatch for $(basename "$archive_path")" >&2
    return 1
  }
}

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ || "$#" -eq 0 ]]; then
  echo "usage: $0 vX.Y.Z wacrawl_X.Y.Z_darwin_ARCH.tar.gz [...]" >&2
  exit 2
fi
if [[ "$(uname -s)" != Darwin ]]; then
  echo "macOS release verification must run on macOS" >&2
  exit 1
fi

release_version=${version#v}
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/wacrawl-verify.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT

for archive in "$@"; do
  archive=$(cd "$(dirname "$archive")" && pwd)/$(basename "$archive")
  checksum=$(dirname "$archive")/checksums.txt
  [[ -f "$archive" && -f "$checksum" ]] || {
    echo "missing artifact or checksums.txt: $archive" >&2
    exit 1
  }
  case "$(basename "$archive")" in
    "wacrawl_${release_version}_darwin_arm64.tar.gz") expected_arch=arm64 ;;
    "wacrawl_${release_version}_darwin_amd64.tar.gz") expected_arch=x86_64 ;;
    *)
      echo "unexpected macOS release artifact: $(basename "$archive")" >&2
      exit 1
      ;;
  esac

  verify_checksum "$archive" "$checksum"
  expected_entries=$'CHANGELOG.md\nLICENSE\nREADME.md\nwacrawl'
  actual_entries=$(tar -tzf "$archive" | LC_ALL=C sort)
  [[ "$actual_entries" == "$expected_entries" ]] || {
    echo "unexpected archive contents: $(basename "$archive")" >&2
    exit 1
  }

  stage="$work_dir/$expected_arch"
  mkdir -p "$stage"
  binary="$stage/wacrawl"
  tar -xOf "$archive" wacrawl > "$binary"
  chmod 0755 "$binary"

  codesign --verify --strict -R="$requirement" --verbose=2 "$binary"
  signature=$(codesign -dvvv "$binary" 2>&1)
  grep -Fx "Identifier=$identifier" <<<"$signature" >/dev/null
  grep -Fx "TeamIdentifier=$team_id" <<<"$signature" >/dev/null
  grep -Fx "Authority=$expected_authority" <<<"$signature" >/dev/null
  if grep -Fx "Signature=adhoc" <<<"$signature" >/dev/null; then
    echo "macOS release binary is ad-hoc signed: $(basename "$archive")" >&2
    exit 1
  fi
  lipo -archs "$binary" | tr ' ' '\n' | grep -Fx "$expected_arch" >/dev/null
  [[ "$("$binary" --version)" == "$release_version" ]]
done
