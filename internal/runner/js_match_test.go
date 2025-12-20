package runner

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

// Ensure .match works on res.body.message after JSON parsing.
func TestBodyMessageMatch(t *testing.T) {
	body := `{"code":"forbidden","message":"Operation not permitted","trace_id":"abc"}`
	resp := &http.Response{
		StatusCode: 403,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}

	p := parsedFile{
		Meta:    metaBlock{Name: "match"},
		Request: requestBlock{Verb: "GET", URL: "http://x"},
		TestsRaw: `
      test("match works", function() {
        expect(res.body.message.match(/Operation/)[0]).to.equal("Operation");
      });
    `,
	}
	g, _ := New(context.Background())
	r := g.(*runner)
	exp := newExpander(nil)
	res, err := executeTests(context.Background(), p, resp, 0, exp, r.logger, "", iterationInfo{total: 1, data: map[string]any{}, exp: exp})
	if err != nil {
		t.Fatalf("executeTests error: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass, got failures %v", res.Failures)
	}
}

// Regression: ensure the exact assertion pattern from the Auth suite works
// (res.body.message.match(/access denied|company|operation/i)).
func TestBodyMessageMatchRegexRegression(t *testing.T) {
	body := `{"code":"forbidden","message":"Operation not permitted","trace_id":"abc"}`
	resp := &http.Response{
		StatusCode: 403,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}

	p := parsedFile{
		Meta:    metaBlock{Name: "match-regression"},
		Request: requestBlock{Verb: "GET", URL: "http://x"},
		TestsRaw: `
      test("message regex works", function() {
        var m = res.body.message.match(/access denied|company|operation/i);
        expect(m[0].toLowerCase()).to.equal("operation");
      });
    `,
	}
	g, _ := New(context.Background())
	r := g.(*runner)
	exp := newExpander(nil)
	res, err := executeTests(context.Background(), p, resp, 0, exp, r.logger, "", iterationInfo{total: 1, data: map[string]any{}, exp: exp})
	if err != nil {
		t.Fatalf("executeTests error: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass, got failures %v", res.Failures)
	}
}
