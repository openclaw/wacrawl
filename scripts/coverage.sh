#!/usr/bin/env sh
set -eu

threshold="${1:-85.0}"
profile="${COVERAGE_PROFILE:-coverage.out}"

awk -v threshold="$threshold" 'BEGIN {
	if (threshold !~ /^[0-9]+([.][0-9]+)?$/) {
		printf "invalid coverage threshold: %s\n", threshold > "/dev/stderr"
		exit 2
	}
}'

raw_profile="$(mktemp)"
trap 'rm -f "$raw_profile"' 0

go test -count=1 ./... -coverprofile="$raw_profile" -covermode=atomic
grep -v '/internal/store/storedb/' "$raw_profile" > "$profile"
total="$(go tool cover -func="$profile" | awk '/^total:/ { sub(/%/, "", $3); print $3 }')"
if [ -z "$total" ]; then
	echo "could not parse total coverage from $profile" >&2
	exit 1
fi

awk -v total="$total" -v threshold="$threshold" 'BEGIN {
	if (total + 0 < threshold + 0) {
		printf "coverage %.1f%% below threshold %.1f%%\n", total, threshold
		exit 1
	}
	printf "coverage %.1f%% >= %.1f%%\n", total, threshold
}'
