package gruno

import (
	"context"
	"path/filepath"
	"testing"

	"pkt.systems/gruno/internal/parser"
)

func TestParseVoucherFile(t *testing.T) {
	path := filepath.Join("sampledata", "Users", "create-user.bru")
	pf, err := parser.ParseFile(context.Background(), path)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if pf.Meta.Name != "Create User" {
		t.Fatalf("name mismatch: %s", pf.Meta.Name)
	}
	if pf.Request.Verb != "POST" {
		t.Fatalf("verb %s", pf.Request.Verb)
	}
	if pf.Request.URL == "" {
		t.Fatalf("url empty")
	}
	if len(pf.Request.Headers) != 2 {
		t.Fatalf("headers parsed: %d", len(pf.Request.Headers))
	}
	if !pf.Request.Body.Present || pf.Request.Body.Raw == "" {
		t.Fatalf("body missing")
	}
	if pf.TestsRaw == "" {
		t.Fatalf("tests missing")
	}
}
