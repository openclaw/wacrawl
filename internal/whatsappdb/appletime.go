package whatsappdb

import (
	"database/sql"
	"math"
	"time"
)

const (
	appleEpoch                  = 978307200
	maxJSONUnixSecond           = 253402300799
	maxJSONAppleSecondExclusive = maxJSONUnixSecond - appleEpoch + 1
)

func appleNullTime(v sql.NullFloat64) time.Time {
	seconds := v.Float64
	if !v.Valid || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds >= maxJSONAppleSecondExclusive {
		return time.Time{}
	}
	return appleTime(seconds)
}

func appleTime(seconds float64) time.Time {
	whole := int64(seconds)
	nano := int64((seconds - float64(whole)) * 1e9)
	return time.Unix(whole+appleEpoch, nano).UTC()
}
