package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRunCLIDefaultsToCurrentDir(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	envDir := filepath.Join(tmp, "environments")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	env := "vars {\n  baseUrl: " + srv.URL + "\n}\n"
	if err := os.WriteFile(filepath.Join(envDir, "local.bru"), []byte(env), 0o644); err != nil {
		t.Fatalf("write env: %v", err)
	}
	bru := `meta { name: Default Dir }

get {
  url: {{baseUrl}}/ping
}

tests {
  test("ok", function(){ expect(res.status).to.equal(200); });
}
`
	if err := os.WriteFile(filepath.Join(tmp, "ping.bru"), []byte(bru), 0o644); err != nil {
		t.Fatalf("write bru: %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWD)
	}()

	cmd := newRunCmd()
	args := []string{"--env", "local"}
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("run cmd: %v", err)
	}
}
