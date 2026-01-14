package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"version"})
	cmd.SetContext(context.Background())

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version cmd: %v", err)
	}
	out := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(out, "pkt.systems/gruno ") {
		t.Fatalf("expected module prefix, got %q", out)
	}
	parts := strings.Split(out, " ")
	if len(parts) != 2 || !strings.HasPrefix(parts[1], "v") {
		t.Fatalf("expected module + version, got %q", out)
	}
}
