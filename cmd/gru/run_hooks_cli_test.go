package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIExecutesExternalHooksAndLogs(t *testing.T) {
	t.Setenv("LOG_LEVEL", "") // ensure flag drives level

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	hookOut := filepath.Join(tmp, "hook.out")
	bru := fmt.Sprintf(`meta { name: CLI Hook }

get {
  url: %s
}

tests {
  test("ok", function(){ expect(res.status).to.equal(200); });
}
`, srv.URL)
	bruPath := filepath.Join(tmp, "hook.bru")
	if err := os.WriteFile(bruPath, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(tmp, "hook.sh")
	body := fmt.Sprintf("#!/bin/sh\necho HOOK-$GRU_HOOK_PHASE >>%s\necho HOOK-$GRU_HOOK_PHASE\n", hookOut)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := newRunCmd()
	args := []string{
		"--structured",
		"--log-level", "debug",
		"--run-pre-request", script,
		"--run-post-request", script,
		bruPath,
	}
	cmd.SetArgs(args)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if !cmd.Flags().Lookup("log-level").Changed {
		t.Fatalf("log-level flag not marked as changed")
	}
	cmd.SetContext(context.Background())

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	outCh := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		outCh <- buf.String()
	}()

	err = cmd.Execute()
	w.Close()
	os.Stdout = origStdout
	if err != nil {
		t.Fatalf("run cmd: %v", err)
	}
	out := <-outCh

	data, err := os.ReadFile(hookOut)
	if err != nil {
		t.Fatalf("read hook output: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "HOOK-pre") || !strings.Contains(content, "HOOK-post") {
		t.Fatalf("hooks did not run, file content: %q", content)
	}

	if !strings.Contains(out, "HOOK-pre") || !strings.Contains(out, "HOOK-post") {
		t.Fatalf("expected hook output in logs, got %q", out)
	}
	if !strings.Contains(out, "\"msg\":\"hook\"") {
		t.Fatalf("expected hook log line, got %q", out)
	}
}
