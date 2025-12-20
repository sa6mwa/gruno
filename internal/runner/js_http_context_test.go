package runner

import (
	"bytes"
	"context"
	"github.com/dop251/goja"
	"io"
	"net/http"
	"strings"
	"testing"
)

// When JS assertions explode, we should include HTTP status/body to aid debugging.
func TestWithHTTPContextOnJSError(t *testing.T) {
	bru := parsedFile{
		FilePath: "bad.bru",
		Meta:     metaBlock{Name: "Bad body"},
		Request:  requestBlock{Verb: "GET", URL: "http://example.com"},
		TestsRaw: `test("access trace", function() { expect(res.body.a).to.equal(1); });`,
	}

	resp := &http.Response{
		StatusCode: 503,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       io.NopCloser(bytes.NewBufferString("service down")),
	}

	vmExp := newExpander(nil)
	res, err := executeTests(context.Background(), bru, resp, 0, vmExp, nil, "", iterationInfo{total: 1, data: map[string]any{}, exp: vmExp})
	if err != nil {
		t.Fatalf("executeTests returned error: %v", err)
	}
	if res.Passed || len(res.Failures) != 1 {
		t.Fatalf("expected single failure")
	}
	msg := res.Failures[0].Message
	if !contains(msg, "status=503") || !contains(msg, "service down") {
		t.Fatalf("expected status/body context, got %q", msg)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// Ensure JS string methods are available on res.body fields.
func TestStringMatchAvailable(t *testing.T) {
	vm := goja.New()
	body := `{"message":"access denied"}`
	val, err := vm.RunString("JSON.parse('" + body + "')")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res := vm.NewObject()
	res.Set("body", val)
	vm.Set("res", res)
	if _, err := vm.RunString(`res.body.message.match(/access/)[0];`); err != nil {
		t.Fatalf("string match failed: %v", err)
	}
}
