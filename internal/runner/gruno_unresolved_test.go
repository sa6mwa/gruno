package runner

import (
	"context"
	"strings"
	"testing"
)

// Regression: unresolved vars should surface a clear error before HTTP.
func TestRunFileMissingEnvVar(t *testing.T) {
	pf := parsedFile{
		FilePath: "missing-baseurl.bru",
		Meta:     metaBlock{Name: "Missing baseUrl"},
		Request:  requestBlock{Verb: "POST", URL: "{{baseUrl}}/foo"},
	}

	g, err := New(context.Background())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	r := g.(*runner)
	_, err = r.executeParsed(context.Background(), pf, RunOptions{})
	if err == nil {
		t.Fatalf("expected error for unresolved vars")
	}
	if msg := err.Error(); msg == "" || !strings.Contains(msg, "baseUrl") {
		t.Fatalf("unexpected error: %v", err)
	}
}
