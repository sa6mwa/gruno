package importer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"pkt.systems/gruno/internal/runner"
)

// Import YAML fixture, generate tests, and run against mock server.
func TestOpenAPIYAMLImportAndRun(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/yaml/ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"msg":"pong"}`))
	})
	mux.HandleFunc("/yaml/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "gru-yaml-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	spec := filepath.Join("..", "..", "sampledata", "openapi", "yaml_fixture.yaml")
	if err := ImportOpenAPI(context.Background(), Options{Source: spec, OutputDir: tmp}); err != nil {
		t.Fatalf("import: %v", err)
	}

	envDir := filepath.Join(tmp, "environments")
	_ = os.MkdirAll(envDir, 0o755)
	envPath := filepath.Join(envDir, "local.bru")
	if err := os.WriteFile(envPath, []byte("vars {\n  baseUrl: "+srv.URL+"\n}\n"), 0o644); err != nil {
		t.Fatalf("env: %v", err)
	}

	g, err := runner.New(context.Background())
	if err != nil {
		t.Fatalf("runner: %v", err)
	}
	sum, err := g.RunFolder(context.Background(), tmp, runner.RunOptions{EnvPath: envPath})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if sum.Failed != 0 {
		t.Fatalf("expected 0 failures, got %d", sum.Failed)
	}
}
