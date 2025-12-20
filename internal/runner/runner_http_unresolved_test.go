package runner

import (
	"strings"
	"testing"
)

// If required variables are missing, we should fail fast with a clear message
// instead of attempting a request with an empty scheme.
func TestBuildHTTPRequestUnresolvedVars(t *testing.T) {
	p := parsedFile{
		Request: requestBlock{
			Verb: "POST",
			URL:  "{{baseUrl}}/foo",
		},
	}
	_, err := buildHTTPRequest(p, newExpander(nil))
	if err == nil {
		t.Fatal("expected error for unresolved vars")
	}
	if got := err.Error(); got == "" || !containsAll(got, []string{"baseUrl", "unresolved"}) {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
