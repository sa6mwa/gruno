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
	"runtime"
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

	script := writeHookScript(t, tmp, hookOut)

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

	if !strings.Contains(out, "\"msg\":\"hook\"") {
		t.Fatalf("expected hook log line, got %q", out)
	}
	if !strings.Contains(out, "\"phase\":\"pre\"") {
		t.Fatalf("expected hook pre log line, got %q", out)
	}
}

func writeHookScript(t *testing.T, dir, hookOut string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		script := filepath.Join(dir, "hook.cmd")
		body := fmt.Sprintf("@echo off\r\n"+
			"echo HOOK-%%GRU_HOOK_PHASE%% >>%s\r\n"+
			"echo HOOK-%%GRU_HOOK_PHASE%%\r\n", hookOut)
		if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return script
	}
	script := filepath.Join(dir, "hook.sh")
	body := fmt.Sprintf("#!/bin/sh\necho HOOK-$GRU_HOOK_PHASE >>%s\necho HOOK-$GRU_HOOK_PHASE\n", hookOut)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}
