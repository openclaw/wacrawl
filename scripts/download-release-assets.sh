#!/usr/bin/env bash
set -euo pipefail

tag=${1:-}
arch=${2:-}
expected_draft=${3:-}
out_dir=${4:-}
repository=${GITHUB_REPOSITORY:-}

usage() {
  echo "usage: $0 vX.Y.Z arm64|amd64 true|false output-directory" >&2
  exit 2
}

[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]] || usage
[[ "$arch" == arm64 || "$arch" == amd64 ]] || usage
[[ "$expected_draft" == true || "$expected_draft" == false ]] || usage
[[ -n "$out_dir" && "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || usage
[[ -n "${GH_TOKEN:-}" ]] || {
  echo "GH_TOKEN is required" >&2
  exit 1
}

for tool in gh jq; do
  command -v "$tool" >/dev/null || {
    echo "missing required tool: $tool" >&2
    exit 1
  }
done

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/wacrawl-release-download.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT

gh api --paginate "repos/$repository/releases?per_page=100" > "$work_dir/release-pages.json"
release=$(
  jq -cs --arg tag "$tag" --argjson draft "$expected_draft" \
    '[.[][] | select(.tag_name == $tag and .draft == $draft)]' \
    "$work_dir/release-pages.json"
)
[[ "$(jq 'length' <<<"$release")" == 1 ]] || {
  echo "expected exactly one release for $tag with draft=$expected_draft" >&2
  exit 1
}
release_id=$(jq -r '.[0].id' <<<"$release")
[[ "$release_id" =~ ^[0-9]+$ ]] || {
  echo "release has an invalid API id" >&2
  exit 1
}

gh api --paginate "repos/$repository/releases/$release_id/assets?per_page=100" > "$work_dir/asset-pages.json"
assets=$(jq -cs '[.[][]]' "$work_dir/asset-pages.json")
version=${tag#v}
expected_names=(
  checksums.txt
  "wacrawl_${version}_darwin_amd64.tar.gz"
  "wacrawl_${version}_darwin_arm64.tar.gz"
  "wacrawl_${version}_linux_amd64.tar.gz"
  "wacrawl_${version}_linux_arm64.tar.gz"
  "wacrawl_${version}_windows_amd64.zip"
  "wacrawl_${version}_windows_arm64.zip"
)
[[ "$(jq 'length' <<<"$assets")" == "${#expected_names[@]}" ]] || {
  echo "release must contain exactly seven wacrawl assets" >&2
  exit 1
}

mkdir -p "$out_dir"
api_prefix="https://api.github.com/repos/$repository/releases/assets/"
for name in "${expected_names[@]}"; do
  matches=$(jq -c --arg name "$name" '[.[] | select(.name == $name)]' <<<"$assets")
  [[ "$(jq 'length' <<<"$matches")" == 1 ]] || {
    echo "release asset missing or duplicated: $name" >&2
    exit 1
  }
  api_url=$(jq -r '.[0].url' <<<"$matches")
  asset_id=${api_url#"$api_prefix"}
  [[ "$api_url" == "$api_prefix"* && "$asset_id" =~ ^[0-9]+$ ]] || {
    echo "release asset has an invalid API URL: $name" >&2
    exit 1
  }
  [[ "$name" == checksums.txt || "$name" == "wacrawl_${version}_darwin_${arch}.tar.gz" ]] || continue
  gh api "$api_url" -H "Accept: application/octet-stream" > "$work_dir/$name"
  [[ -s "$work_dir/$name" ]] || {
    echo "downloaded release asset is empty: $name" >&2
    exit 1
  }
  mv "$work_dir/$name" "$out_dir/$name"
done
