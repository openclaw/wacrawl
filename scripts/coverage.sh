#!/usr/bin/env sh
set -eu

threshold="${1:-85.0}"
profile="${COVERAGE_PROFILE:-coverage.out}"
raw_profile="${profile}.raw"

go test ./... -coverprofile="$raw_profile" -covermode=atomic
grep -v '/internal/store/storedb/' "$raw_profile" > "$profile"
rm -f "$raw_profile"
total="$(go tool cover -func="$profile" | awk '/^total:/ { sub(/%/, "", $3); print $3 }')"

awk -v total="$total" -v threshold="$threshold" 'BEGIN {
	if (total + 0 < threshold + 0) {
		printf "coverage %.1f%% below threshold %.1f%%\n", total, threshold
		exit 1
	}
	printf "coverage %.1f%% >= %.1f%%\n", total, threshold
}'
