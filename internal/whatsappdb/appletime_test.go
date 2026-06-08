package whatsappdb

import (
	"database/sql"
	"testing"
)

func TestAppleNullTimeNormalizesSentinel(t *testing.T) {
	// A 0@status-style sentinel ZLASTMESSAGEDATE converts to an impossible year
	// (>9999); it must normalize to zero (unknown) so it never reaches JSON.
	if got := appleNullTime(sql.NullFloat64{Float64: 300000000000, Valid: true}); !got.IsZero() {
		t.Fatalf("sentinel should normalize to zero, got %v (year %d)", got, got.Year())
	}
	// A real timestamp (2026-06-06T00:00:00Z) still converts correctly.
	got := appleNullTime(sql.NullFloat64{Float64: 802396800, Valid: true})
	if got.IsZero() || got.Year() != 2026 {
		t.Fatalf("valid timestamp should convert to 2026, got %v", got)
	}
	// Invalid / non-positive stays zero (unchanged behaviour).
	if got := appleNullTime(sql.NullFloat64{Valid: false}); !got.IsZero() {
		t.Fatalf("invalid should be zero, got %v", got)
	}
}
