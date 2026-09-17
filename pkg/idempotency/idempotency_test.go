package idempotency

import (
	"errors"
	"testing"
)

func TestValidateRejectsBlank(t *testing.T) {
	cases := []string{"", "  ", "\t"}
	for _, key := range cases {
		if err := Validate(key); !errors.Is(err, ErrKeyRequired) {
			t.Errorf("Validate(%q) = %v, want ErrKeyRequired", key, err)
		}
	}

	if err := Validate("some-key"); err != nil {
		t.Errorf("Validate(%q) = %v, want nil", "some-key", err)
	}
}

func TestDeriveIsDeterministicAndPartsSensitive(t *testing.T) {
	a := Derive("property-1", "unit-1", "2026-01", "rent", "10000")
	b := Derive("property-1", "unit-1", "2026-01", "rent", "10000")
	if a != b {
		t.Fatalf("Derive is not deterministic: %q != %q", a, b)
	}

	c := Derive("property-1", "unit-1", "2026-02", "rent", "10000")
	if a == c {
		t.Fatal("Derive produced the same key for different periods")
	}
}
