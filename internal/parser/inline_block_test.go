package parser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSingleLineRequestBlock(t *testing.T) {
	tmp := t.TempDir()
	bru := `meta { name: Inline Case }

get { url: https://example.com/inline }
`
	path := filepath.Join(tmp, "inline.bru")
	if err := os.WriteFile(path, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	pf, err := ParseFile(context.Background(), path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pf.Meta.Name != "Inline Case" {
		t.Fatalf("meta name mismatch: %q", pf.Meta.Name)
	}
	if pf.Request.URL != "https://example.com/inline" {
		t.Fatalf("url mismatch: %q", pf.Request.URL)
	}
}

func TestParseSingleLineVariants(t *testing.T) {
	tmp := t.TempDir()
	bru := `meta {
  name: Inline All
  type: http
  seq: 5
  tags: [a, b]
}

post { url: https://api.test/post headers { X-One: 1 } body:json { { "k": "v" } } }
put { url: https://api.test/put }
patch { url: https://api.test/patch }
delete { url: https://api.test/delete }
get { url: https://api.test/get }
`
	path := filepath.Join(tmp, "inline_all.bru")
	if err := os.WriteFile(path, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	pf, err := ParseFile(context.Background(), path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pf.Meta.Name != "Inline All" || pf.Meta.Seq != 5 || len(pf.Meta.Tags) != 2 {
		t.Fatalf("meta mismatch: %+v", pf.Meta)
	}
	if pf.Request.Verb != "GET" || pf.Request.URL != "https://api.test/get" {
		t.Fatalf("expected last request parsed, got %s %s", pf.Request.Verb, pf.Request.URL)
	}
}

func TestParseInlineBodyAndHeaders(t *testing.T) {
	tmp := t.TempDir()
	bru := `meta { name: Inline Body }

post { url: https://api.test/body headers { X-One: 1 } body:json { { "k": "v", "n": 1 } } }
`
	path := filepath.Join(tmp, "inline_body.bru")
	if err := os.WriteFile(path, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	pf, err := ParseFile(context.Background(), path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pf.Request.URL != "https://api.test/body" || pf.Request.Verb != "POST" {
		t.Fatalf("url/verb mismatch: %+v", pf.Request)
	}
	if pf.Request.Body.Raw == "" || !strings.Contains(pf.Request.Body.Raw, `"k": "v"`) {
		t.Fatalf("body not captured: %+v", pf.Request.Body)
	}
	if pf.Request.Headers["X-One"] != "1" {
		t.Fatalf("header not captured: %+v", pf.Request.Headers)
	}
	if pf.Request.Body.Type != "json" {
		t.Fatalf("body type mismatch: %s", pf.Request.Body.Type)
	}
}
