package store

import (
	"fmt"
	"time"
)

const maxJSONUnixSecond = 253402300799 // 9999-12-31T23:59:59Z, the JSON time limit.

func formatSyncTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func messageUnix(message Message) int64 {
	if message.storedUnix != 0 || message.Timestamp.IsZero() {
		return message.storedUnix
	}
	return unix(message.Timestamp)
}

func nullableUnix(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return unix(t)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

func fromUnix(v int64) time.Time {
	if !validUnixTimestamp(v) {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}

func validUnixTimestamp(v int64) bool {
	return v > 0 && v <= maxJSONUnixSecond
}

func validUnixPredicate(column string) string {
	return fmt.Sprintf("%s > 0 and %s <= %d", column, column, maxJSONUnixSecond)
}

func invalidUnixPredicate(column string) string {
	return fmt.Sprintf("(%s <= 0 or %s > %d)", column, column, maxJSONUnixSecond)
}
