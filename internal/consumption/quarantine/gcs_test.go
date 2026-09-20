package quarantine

import (
	"strings"
	"testing"
	"time"
)

func TestObjectNameFormat(t *testing.T) {
	now := time.Date(2026, 9, 19, 14, 30, 45, 123456789, time.UTC)
	id := "msg-1"
	got := ObjectName(now, id)

	// Expected format: YYYY/MM/DD/HHmmss.nnnnnnnnn-<digest>-<safe>.xml
	// Digest is first 8 hex chars of SHA-256(id)
	digest := messageIDDigest(id)
	want := "2026/09/19/143045.123456789-" + digest + "-msg-1.xml"
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
		// want is the safe name portion (digest will be computed)
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
			digest := messageIDDigest(tt.id)
			want := datePrefix + digest + "-" + tt.want
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestObjectNameDistinctIDsProduceDifferentKeys(t *testing.T) {
	// Regression test: distinct message IDs always produce distinct keys,
	// even if they reduce to the same safe name, because the digest
	// distinguishes them at the same timestamp.
	now := time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)

	// IDs that previously collided (all reduce to "unknown")
	collidingIDs := []string{
		"",
		"   ",
		".",
		"..",
		"....",
		"////",
		"msg-1/",
	}

	keys := make(map[string][]string)
	for _, id := range collidingIDs {
		got := ObjectName(now, id)
		keys[got] = append(keys[got], id)
	}

	// Verify no collisions: each key maps to exactly one ID
	for key, ids := range keys {
		if len(ids) > 1 {
			t.Fatalf("collision: ids %v produced the same key %q", ids, key)
		}
	}
}

func TestObjectNameIdempotence(t *testing.T) {
	// Same ID at the same instant produces the same key, so a redelivery
	// that fails identically overwrites itself rather than accumulating duplicates.
	now := time.Date(2026, 9, 19, 12, 34, 56, 123456789, time.UTC)
	id := "my-message-id"

	key1 := ObjectName(now, id)
	key2 := ObjectName(now, id)

	if key1 != key2 {
		t.Fatalf("same id at same time produced different keys: %q vs %q", key1, key2)
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
