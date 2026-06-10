package whatsappdb

import (
	"database/sql"
	"math"
	"testing"
	"time"
)

func TestAppleNullTimeNormalizesSentinel(t *testing.T) {
	if got := appleNullTime(sql.NullFloat64{Float64: 300000000000, Valid: true}); !got.IsZero() {
		t.Fatalf("sentinel should normalize to zero, got %v (year %d)", got, got.Year())
	}
	got := appleNullTime(sql.NullFloat64{Float64: 802396800, Valid: true})
	want := time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) || got.Location().String() != "UTC" {
		t.Fatalf("valid timestamp = %s (%s), want %s UTC", got, got.Location(), want)
	}
}

func TestAppleNullTimeJSONBounds(t *testing.T) {
	got := appleNullTime(sql.NullFloat64{Float64: maxJSONAppleSecondExclusive - 1, Valid: true})
	want := time.Unix(maxJSONUnixSecond, 0).UTC()
	if !got.Equal(want) {
		t.Fatalf("max JSON-safe Apple timestamp = %s, want %s", got, want)
	}
	for _, value := range []float64{
		maxJSONAppleSecondExclusive,
		math.Inf(1),
		math.NaN(),
		0,
		-1,
	} {
		if got := appleNullTime(sql.NullFloat64{Float64: value, Valid: true}); !got.IsZero() {
			t.Fatalf("invalid Apple timestamp %v should normalize to zero, got %v", value, got)
		}
	}
	if got := appleNullTime(sql.NullFloat64{Valid: false}); !got.IsZero() {
		t.Fatalf("invalid should be zero, got %v", got)
	}
}
