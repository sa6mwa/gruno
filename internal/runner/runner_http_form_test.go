package runner

import (
	"io"
	"strings"
	"testing"

	"pkt.systems/gruno/internal/parser"
)

func TestBuildHTTPRequestFormURLEncodedOrder(t *testing.T) {
	p := parsedFile{
		Request: requestBlock{
			Verb: "POST",
			URL:  "https://example.com",
			Body: parser.BodyBlock{
				Present: true,
				Type:    "form-urlencoded",
				Raw:     "b: 2\na: 1\n",
			},
		},
	}

	req, err := buildHTTPRequest(p, newExpander(nil))
	if err != nil {
		t.Fatalf("build req: %v", err)
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	got := string(data)
	if got != "b=2&a=1" {
		t.Fatalf("expected order preserved, got %q", got)
	}
	if ct := req.Header.Get("Content-Type"); !strings.Contains(ct, "application/x-www-form-urlencoded") {
		t.Fatalf("expected form content-type, got %q", ct)
	}
}
