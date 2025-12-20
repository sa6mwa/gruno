package importer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportOpenAPIDirectory(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join("testdata", "openapi.yaml")

	opts := Options{Source: src, OutputDir: tmp, GroupBy: "tags", CollectionName: "Fixture"}
	if err := ImportOpenAPI(context.Background(), opts); err != nil {
		t.Fatalf("import openapi: %v", err)
	}

	mustExist(t, filepath.Join(tmp, "bruno.json"))
	envBytes := mustRead(t, filepath.Join(tmp, "environments", "local.bru"))
	if !strings.Contains(string(envBytes), "https://api.sample.test") {
		t.Fatalf("env missing base url: %s", envBytes)
	}
	mustMatch(t, tmp, "Ping")
	mustSomeBru(t, filepath.Join(tmp, "users"))
}

func TestImportOpenAPIOutputFileOnly(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "summary.json")
	src := filepath.Join("testdata", "openapi.yaml")

	if err := ImportOpenAPI(context.Background(), Options{Source: src, OutputFile: out}); err != nil {
		t.Fatalf("import openapi summary: %v", err)
	}

	data := mustRead(t, out)
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if payload["format"] != "bruno" {
		t.Fatalf("unexpected summary format: %#v", payload)
	}
}

func TestImportWSDLDirectory(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join("testdata", "service.wsdl")

	if err := ImportWSDL(context.Background(), Options{Source: src, OutputDir: tmp, CollectionName: "Demo"}); err != nil {
		t.Fatalf("import wsdl: %v", err)
	}

	mustExist(t, filepath.Join(tmp, "bruno.json"))
	env := string(mustRead(t, filepath.Join(tmp, "environments", "local.bru")))
	if !strings.Contains(env, "http://example.com/soap") {
		t.Fatalf("env missing soap address: %s", env)
	}
	mustExist(t, filepath.Join(tmp, "DemoBinding", "GetDemo.bru"))
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file %s: %v", path, err)
	}
}

func mustMatch(t *testing.T, dir string, contains string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), contains) {
			return
		}
	}
	t.Fatalf("no file in %s containing %q", dir, contains)
}

func mustSomeBru(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasSuffix(strings.ToLower(e.Name()), ".bru") {
			return
		}
	}
	t.Fatalf("no .bru files in %s", dir)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
