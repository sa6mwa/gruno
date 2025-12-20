package importer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ensure remote external refs are blocked unless explicitly allowed.
func TestOpenAPIRemoteRefsRequireOptIn(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/schemas/ping.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"object","properties":{"msg":{"type":"string"}}}`))
	})
	rootSpec := `{
      "openapi": "3.0.3",
      "info": {"title": "RemoteRef", "version": "1.0"},
      "paths": {
        "/ping": {
          "get": {
            "responses": {
              "200": {
                "description": "ok",
                "content": {
                  "application/json": {
                    "schema": {"$ref": "{{SCHEMA}}"}
                  }
                }
              }
            }
          }
        }
      }
    }`
	mux.HandleFunc("/root.json", func(w http.ResponseWriter, r *http.Request) {
		ref := "http://" + r.Host + "/schemas/ping.json"
		_, _ = w.Write([]byte(strings.ReplaceAll(rootSpec, "{{SCHEMA}}", ref)))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	spec := []byte(strings.ReplaceAll(rootSpec, "{{SCHEMA}}", srv.URL+"/schemas/ping.json"))

	tmp, err := os.MkdirTemp("", "gru-remote-ref-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	specPath := filepath.Join(tmp, "spec.json")
	if err := os.WriteFile(specPath, spec, 0o644); err != nil {
		t.Fatal(err)
	}

	// Block cross-origin by default
	err = ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: filepath.Join(tmp, "out"), GenerateTests: true})
	if err == nil {
		t.Fatalf("expected remote ref to be blocked")
	}

	// Allow when opted-in
	if err := ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: filepath.Join(tmp, "out2"), GenerateTests: true, AllowRemoteRefs: true}); err != nil {
		t.Fatalf("allow remote refs failed: %v", err)
	}

	// Allow same-origin without opt-in
	sameOriginSpec := []byte(`{
      "openapi": "3.0.3",
      "info": {"title": "RemoteRef", "version": "1.0"},
      "paths": {
        "/ping": {"get": {"responses": {"200": {"description": "ok","content": {"application/json": {"schema": {"$ref": "` + srv.URL + `/schemas/ping.json"}}}}}}}
      }
    }`)
	specPath2 := filepath.Join(tmp, "spec2.json")
	if err := os.WriteFile(specPath2, sameOriginSpec, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ImportOpenAPI(context.Background(), Options{Source: srv.URL + "/root.json", OutputDir: filepath.Join(tmp, "out3"), GenerateTests: true}); err != nil {
		t.Fatalf("expected same-origin ref to pass: %v", err)
	}
}

// Malicious file ref from remote spec should be blocked.
func TestOpenAPIRemoteFileRefBlocked(t *testing.T) {
	tmp, err := os.MkdirTemp("", "gru-bad-ref-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	secret := filepath.Join(tmp, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"top":"secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/spec.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
  "openapi": "3.0.3",
  "info": {"title": "Bad", "version": "1.0"},
  "paths": {"/bad": {"get": {"responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "file://` + secret + `"}}}}}}}}
}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	err = ImportOpenAPI(context.Background(), Options{Source: srv.URL + "/spec.json", OutputDir: filepath.Join(tmp, "out"), GenerateTests: true})
	if err == nil {
		t.Fatalf("expected remote file ref to be blocked")
	}

	if err := ImportOpenAPI(context.Background(), Options{Source: srv.URL + "/spec.json", OutputDir: filepath.Join(tmp, "out2"), GenerateTests: true, AllowFileRefs: true, AllowRemoteRefs: true}); err != nil {
		t.Fatalf("expected opt-in to allow file ref: %v", err)
	}
}

// Local spec may include sibling file but not escape the base directory.
func TestOpenAPILocalFileRefEscapesBlocked(t *testing.T) {
	base := filepath.Join("..", "..", "sampledata", "openapi", "external")

	tmp, err := os.MkdirTemp("", "gru-local-ref-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	escape := []byte(`{
  "openapi": "3.0.3",
  "info": {"title": "Esc", "version": "1.0"},
  "paths": {"/bad": {"get": {"responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "../secrets.json"}}}}}}}}
}`)

	specPath := filepath.Join(tmp, "esc.json")
	if err := os.WriteFile(specPath, escape, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: filepath.Join(tmp, "out"), GenerateTests: true}); err == nil {
		t.Fatalf("expected escaping file ref to be blocked")
	}

	specGood := filepath.Join(base, "root.json")
	if err := ImportOpenAPI(context.Background(), Options{Source: specGood, OutputDir: filepath.Join(tmp, "out2"), GenerateTests: true}); err != nil {
		t.Fatalf("expected sibling ref to pass: %v", err)
	}
}

// Ensure local relative refs resolve without opt-in.
func TestOpenAPIRelativeFileRefs(t *testing.T) {
	base := filepath.Join("..", "..", "sampledata", "openapi", "external")
	spec := filepath.Join(base, "root.json")

	tmp, err := os.MkdirTemp("", "gru-rel-ref-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	if err := ImportOpenAPI(context.Background(), Options{Source: spec, OutputDir: tmp}); err != nil {
		t.Fatalf("import relative ref: %v", err)
	}

	// ensure a request file was generated for the ref path
	found := false
	filepath.Walk(tmp, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(info.Name(), ".bru") && strings.Contains(info.Name(), "ext") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatalf("expected generated .bru file for relative ref path")
	}
}
