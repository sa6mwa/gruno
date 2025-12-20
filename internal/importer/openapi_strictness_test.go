package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAPIStrictnessDeepAssertions(t *testing.T) {
	spec := `openapi: 3.0.0
info:
  title: strictness
  version: "1.0"
paths:
  /nums:
    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [ids, matrix]
                properties:
                  ids:
                    type: array
                    items:
                      type: integer
                      enum: [1, 2, 3]
                  matrix:
                    type: array
                    items:
                      type: array
                      items:
                        type: number
                  meta:
                    type: object
                    properties:
                      version:
                        type: integer
                        minimum: 1
`

	tmp := t.TempDir()
	specPath := filepath.Join(tmp, "spec.yaml")
	outDir := filepath.Join(tmp, "out")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	opts := Options{
		Source:     specPath,
		OutputDir:  outDir,
		Strictness: "strict",
	}
	if err := ImportOpenAPI(context.Background(), opts); err != nil {
		t.Fatalf("import strict: %v", err)
	}

	var bruPath string
	_ = filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".bru") && !strings.Contains(path, string(filepath.Separator)+"environments"+string(filepath.Separator)) {
			bruPath = path
		}
		return nil
	})
	if bruPath == "" {
		t.Fatalf("no bru file emitted")
	}

	body := string(mustRead(t, bruPath))
	if !strings.Contains(body, "Number.isInteger(it)") {
		t.Fatalf("strict mode should enforce integer items: %s", body)
	}
	if !strings.Contains(body, "forEach(function(it){ expect(Array.isArray(it)).to.equal(true);") {
		t.Fatalf("strict mode should recurse into nested arrays: %s", body)
	}
	if !strings.Contains(body, "expect(Number.isInteger(res.body['meta']['version'])") {
		t.Fatalf("strict mode should assert nested object integers: %s", body)
	}
}

func TestOpenAPIDefaultRemainsStandard(t *testing.T) {
	spec := `openapi: 3.0.0
info:
  title: standard
  version: "1.0"
paths:
  /ids:
    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                type: array
                items:
                  type: integer
`
	tmp := t.TempDir()
	specPath := filepath.Join(tmp, "spec.yaml")
	outDir := filepath.Join(tmp, "out")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	if err := ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: outDir}); err != nil {
		t.Fatalf("import standard: %v", err)
	}

	var bruPath string
	_ = filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".bru") {
			bruPath = path
		}
		return nil
	})
	body := string(mustRead(t, bruPath))
	if strings.Contains(body, "Number.isInteger(it)") {
		t.Fatalf("standard mode should not emit strict integer checks")
	}
}
