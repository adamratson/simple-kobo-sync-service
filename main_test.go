package main

import (
	"encoding/hex"
	"os"
	"testing"
)

func TestEnvOr_envSet(t *testing.T) {
	t.Setenv("TEST_KOBO_VAR", "fromenv")
	if got := envOr("TEST_KOBO_VAR", "fallback"); got != "fromenv" {
		t.Errorf("want %q, got %q", "fromenv", got)
	}
}

func TestEnvOr_envUnset(t *testing.T) {
	os.Unsetenv("TEST_KOBO_VAR_UNSET")
	if got := envOr("TEST_KOBO_VAR_UNSET", "fallback"); got != "fallback" {
		t.Errorf("want %q, got %q", "fallback", got)
	}
}

func TestEnvOr_emptyEnvUseFallback(t *testing.T) {
	// Empty string counts as unset — fallback wins.
	t.Setenv("TEST_KOBO_VAR_EMPTY", "")
	if got := envOr("TEST_KOBO_VAR_EMPTY", "fallback"); got != "fallback" {
		t.Errorf("want %q, got %q", "fallback", got)
	}
}

func TestRandomToken_isHex(t *testing.T) {
	tok := randomToken()
	if len(tok) != 16 {
		t.Errorf("want 16 chars, got %d: %q", len(tok), tok)
	}
	if _, err := hex.DecodeString(tok); err != nil {
		t.Errorf("token %q is not valid hex: %v", tok, err)
	}
}

func TestRandomToken_unique(t *testing.T) {
	seen := make(map[string]bool, 20)
	for range 20 {
		tok := randomToken()
		if seen[tok] {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = true
	}
}

