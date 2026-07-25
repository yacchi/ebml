package kvs

import (
	"testing"
	"time"
)

func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		input string
		want  time.Time
		ok    bool
	}{
		{"1700000000", time.Unix(1700000000, 0).UTC(), true},
		{"1700000000.512", time.Unix(1700000000, 512000000).UTC(), true},
		{"1700000000.123456789", time.Unix(1700000000, 123456789).UTC(), true},
		{"1700000000.1234567891", time.Unix(1700000000, 123456789).UTC(), true},
		{"", time.Time{}, false},
		{"abc", time.Time{}, false},
		{"1700000000.xyz", time.Time{}, false},
	}
	for _, test := range tests {
		got, err := ParseTimestamp(test.input)
		if test.ok {
			if err != nil || !got.Equal(test.want) {
				t.Errorf("ParseTimestamp(%q) = %v, %v; want %v", test.input, got, err, test.want)
			}
		} else if err == nil {
			t.Errorf("ParseTimestamp(%q) succeeded", test.input)
		}
	}
}

func TestDescribeErrorID(t *testing.T) {
	for id, want := range map[int]string{
		3002: "Error writing to the stream",
		4000: "Requested fragment is not found",
		4500: "Access denied for the stream's KMS key",
		4501: "Stream's KMS key is disabled",
		4502: "Validation error on the stream's KMS key",
		4503: "KMS key specified in the stream is unavailable",
		4504: "Invalid usage of the KMS key specified in the stream",
		4505: "Invalid state of the KMS key specified in the stream",
		4506: "Unable to find the KMS key specified in the stream",
		5000: "Internal error",
	} {
		if got := DescribeErrorID(id); got != want {
			t.Errorf("DescribeErrorID(%d) = %q, want %q", id, got, want)
		}
	}
	if got := DescribeErrorID(9999); got != "" {
		t.Errorf("DescribeErrorID(9999) = %q, want empty", got)
	}
}

func TestMetadataErr(t *testing.T) {
	m := Metadata{Tags: map[string]string{TagErrorCode: "Requested fragment is not found", TagErrorID: "4000"}}
	err, ok := m.Err().(*StreamError)
	if !ok || err.Code != "Requested fragment is not found" || err.ID != 4000 {
		t.Fatalf("Err() = %#v, want typed 4000 error", m.Err())
	}
	m = Metadata{Tags: map[string]string{TagErrorCode: "failure"}}
	err, ok = m.Err().(*StreamError)
	if !ok || err.ID != 0 {
		t.Fatalf("Err() = %#v, want ID 0", m.Err())
	}
	if (Metadata{Tags: map[string]string{}}).Err() != nil {
		t.Fatal("Err() without code tag is non-nil")
	}
}
