package httpapi

import (
	"context"
	"testing"
)

func TestOIDCVerifierRejectsAnEmptyToken(t *testing.T) {
	v := NewOIDCVerifier("https://ingest.example", "push@example.iam.gserviceaccount.com")

	if err := v.Verify(context.Background(), ""); err == nil {
		t.Fatal("expected an error for a missing token")
	}
}

func TestOIDCVerifierRejectsGarbage(t *testing.T) {
	v := NewOIDCVerifier("https://ingest.example", "push@example.iam.gserviceaccount.com")

	if err := v.Verify(context.Background(), "not-a-jwt"); err == nil {
		t.Fatal("expected an error for a malformed token")
	}
}
