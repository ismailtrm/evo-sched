package measurement

import (
	"testing"
	"time"
)

func TestTimestampRoundTripPreservesNanoseconds(t *testing.T) {
	want := time.Date(2026, time.August, 27, 7, 30, 15, 987654321, time.FixedZone("test", 3*60*60))

	encoded := FormatTimestamp(want)
	got, err := ParseTimestamp(encoded)
	if err != nil {
		t.Fatalf("ParseTimestamp() error = %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("round trip = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
	if got.Location() != time.UTC {
		t.Fatalf("round trip location = %v, want UTC", got.Location())
	}
}

func TestParseTimestampRejectsInvalidValue(t *testing.T) {
	if _, err := ParseTimestamp("not-a-timestamp"); err == nil {
		t.Fatal("ParseTimestamp() error = nil, want non-nil")
	}
}
