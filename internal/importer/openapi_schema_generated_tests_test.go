package importer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"pkt.systems/gruno/internal/runner"
)

// Ensure generated schema-based tests execute successfully against a real server.
func TestOpenAPIGeneratedSchemaTestsPass(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/schema/format", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
            "email": "a@test.com",
            "uuid": "123e4567-e89b-12d3-a456-426614174000",
            "website": "https://example.com",
            "birthday": "2024-12-01",
            "createdAt": "2024-12-01T12:34:56Z",
            "ipv4": "192.168.0.1",
            "ipv6": "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
            "hostname": "api.example.com",
            "cidr": "192.168.0.0/24",
            "ipv6Cidr": "2001:db8::/64",
            "data": "QUJDRA==",
            "code": "ABC",
            "count": 3,
            "tags": ["alpha", "beta"],
            "meta": {"foo": "bar"}
        }`))
	})
	mux.HandleFunc("/schema/variant", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"kind":"alpha","alpha":1}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "gru-oas-schema-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	spec := filepath.Join("..", "..", "sampledata", "openapi", "schema_tests.json")
	if err := ImportOpenAPI(context.Background(), Options{Source: spec, OutputDir: tmp, GenerateTests: true}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Overwrite env with test server URL to keep run deterministic.
	envDir := filepath.Join(tmp, "environments")
	_ = os.MkdirAll(envDir, 0o755)
	envPath := filepath.Join(envDir, "local.bru")
	if err := os.WriteFile(envPath, []byte("vars {\n  baseUrl: "+srv.URL+"\n}\n"), 0o644); err != nil {
		t.Fatalf("write env: %v", err)
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
