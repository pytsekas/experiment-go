package quarantine

import (
	"strings"
	"testing"
	"time"
)

func TestObjectNameFormat(t *testing.T) {
	now := time.Date(2026, 9, 19, 14, 30, 45, 123456789, time.UTC)
	got := ObjectName(now, "msg-1")

	// Expected format: YYYY/MM/DD/HHmmss.nnnnnnnnn-safe.xml
	want := "2026/09/19/143045.123456789-msg-1.xml"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestObjectNameAdversarialClasses(t *testing.T) {
	now := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)
	datePrefix := "2026/09/19/000000.000000000-"

	tests := []struct {
		name string
		id   string
		// want is a suffix of the full path (after the timestamp prefix)
		want string
	}{
		// Empty and whitespace
		{"empty", "", "unknown.xml"},
		{"whitespace only", "   ", "unknown.xml"},
		{"tab", "\t", "unknown.xml"},

		// Dots-only adversarial class (regression: previously collided)
		{"single dot", ".", "unknown.xml"},
		{"double dot", "..", "unknown.xml"},
		{"triple dot", "...", "unknown.xml"},
		{"many dots", "......", "unknown.xml"},

		// Dots with special chars (previously collided)
		{"dots and slashes", "..../", "unknown.xml"},
		{"dots and double slash", "....//", "unknown.xml"},

		// Trailing slashes (previously collided)
		{"msg ending with slash", "msg-1/", "msg-1.xml"},
		{"only slashes", "////", "unknown.xml"},

		// Path traversal attempts
		{"path traversal", "../../etc/passwd", "etc_passwd.xml"},
		{"backslashes", "..\\..\\etc\\passwd", "etc_passwd.xml"},

		// Control characters
		{"embedded newline", "abc\ndef", "abc_def.xml"},
		{"embedded carriage return", "msg\r1", "msg_1.xml"},

		// Very long id (cap at 128)
		{"long id", strings.Repeat("a", 200), strings.Repeat("a", 128) + ".xml"},

		// Non-ASCII
		{"non-ASCII", "café", "caf.xml"},

		// Plain well-formed id
		{"well-formed id", "msg-2026-001", "msg-2026-001.xml"},

		// Collision regression test: ids that previously collided
		{"msg with trailing slash", "msg-1/", "msg-1.xml"},
		{"empty id", "", "unknown.xml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ObjectName(now, tt.id)
			want := datePrefix + tt.want
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestObjectNameNoCollisions(t *testing.T) {
	// Regression test: nanosecond timestamp prevents collisions.
	// IDs that reduce to the same safe name must still be differentiated by timestamp.
	now1 := time.Date(2026, 9, 19, 0, 0, 0, 1, time.UTC) // 1 nanosecond
	now2 := time.Date(2026, 9, 19, 0, 0, 0, 2, time.UTC) // 2 nanoseconds

	// These two IDs both reduce to "unknown" but should have different keys due to timestamp
	key1 := ObjectName(now1, "..")
	key2 := ObjectName(now2, "..")

	if key1 == key2 {
		t.Fatalf("same id at different times produced the same key: %q", key1)
	}

	// Verify the keys are different only in the timestamp, not the safe name
	if !strings.HasSuffix(key1, "-unknown.xml") || !strings.HasSuffix(key2, "-unknown.xml") {
		t.Fatalf("expected both to have -unknown.xml suffix")
	}

	// Different safe names at the same timestamp should produce different keys
	key3 := ObjectName(now1, "msg-1")
	key4 := ObjectName(now1, "msg-2")

	if key3 == key4 {
		t.Fatalf("different ids at same time produced the same key")
	}

	if !strings.HasSuffix(key3, "-msg-1.xml") || !strings.HasSuffix(key4, "-msg-2.xml") {
		t.Fatalf("expected different safe names")
	}
}

func TestSanitizeID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"msg-1", "msg-1"},
		{"", "unknown"},
		{"   ", "unknown"},
		{".", "unknown"},
		{"..", "unknown"},
		{"...msg", "msg"},
		{"msg_name", "msg_name"},
		{"msg.name", "msg.name"},
		{"msg-name", "msg-name"},
		{"msg@name", "msg_name"},
		{"msg/name", "msg_name"},
		{"msg\\name", "msg_name"},
		{"/etc/passwd", "etc_passwd"},
		{"../../etc/passwd", "etc_passwd"},
		{strings.Repeat("a", 200), strings.Repeat("a", 128)},
		{"café", "caf"},
		{"msg-1/", "msg-1"},
		{"....", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeID(tt.input)
			if got != tt.want {
				t.Fatalf("sanitizeID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeMetadata(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"plain text", "plain text"},
		{"with\nnewline", "with_newline"},
		{"with\rcarriage return", "with_carriage return"},
		{"with\ttab", "with\ttab"},
		{"msg-1", "msg-1"},
		{"café", "caf_"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeMetadata(tt.input)
			if got != tt.want {
				t.Fatalf("sanitizeMetadata(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
