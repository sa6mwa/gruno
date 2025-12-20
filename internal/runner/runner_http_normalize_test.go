package runner

import (
	"strings"
	"testing"
)

// Ensure normalizeJSONBody does not fail when JS evaluates but JSON marshaling
// rejects unsupported values (e.g. functions). We should fall back to the raw
// payload so callers don't get a marshal error.
func TestNormalizeJSONBodyMarshalFallback(t *testing.T) {
	raw := `{ foo: function(){ return 1; }, bar: "ok" }`
	got, err := normalizeJSONBody(raw)
	if err != nil {
		t.Fatalf("normalizeJSONBody returned error: %v", err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(raw) {
		t.Fatalf("expected fallback raw body, got %s", string(got))
	}
}
