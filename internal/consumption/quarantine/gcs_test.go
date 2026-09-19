package quarantine

import (
	"strings"
	"testing"
	"time"
)

func TestObjectNamePartitionsByDate(t *testing.T) {
	got := ObjectName(time.Date(2026, 9, 19, 14, 30, 0, 0, time.UTC), "msg-1")
	if want := "2026/09/19/msg-1.xml"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestObjectNameSanitisesTheMessageID(t *testing.T) {
	// A message id is attacker-adjacent data; it must never escape the prefix.
	got := ObjectName(time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC), "../../etc/passwd")
	if strings.Contains(got, "..") || strings.Count(got, "/") != 3 {
		t.Fatalf("got %q, which escapes the date prefix", got)
	}
}

func TestObjectNameHandlesAnEmptyID(t *testing.T) {
	got := ObjectName(time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC), "")
	if !strings.HasSuffix(got, "/unknown.xml") {
		t.Fatalf("got %q, want an unknown.xml fallback", got)
	}
}
