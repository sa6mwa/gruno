package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportOpenAPI_UsesExternalExamplesAndGeneratesTests(t *testing.T) {
	tmp := t.TempDir()
	example := `{"hello":"world","items":[1,2]}`
	if err := os.WriteFile(filepath.Join(tmp, "example.json"), []byte(example), 0o644); err != nil {
		t.Fatalf("write example: %v", err)
	}

	spec := `openapi: 3.0.0
info:
  title: Demo
  version: "1.0"
paths:
  /demo:
    post:
      requestBody:
        required: true
        content:
          application/json:
            examples:
              sample:
                externalValue: "./example.json"
      responses:
        '202':
          description: ok
          content:
            application/json:
              schema:
                type: object
                required: [hello]
                properties:
                  hello: { type: string }
`
	specPath := filepath.Join(tmp, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	outDir := filepath.Join(tmp, "out")
	err := ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: outDir, GenerateTests: true})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	var reqFile string
	_ = filepath.Walk(outDir, func(p string, info os.FileInfo, _ error) error {
		if info == nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".bru") && !strings.Contains(p, string(filepath.Separator)+"environments"+string(filepath.Separator)) {
			reqFile = p
		}
		return nil
	})
	if reqFile == "" {
		t.Fatalf("no request bru found under %s", outDir)
	}
	data, _ := os.ReadFile(reqFile)
	text := string(data)
	if !strings.Contains(text, `"hello":"world"`) {
		t.Fatalf("example not injected:\n%s", text)
	}
	if !strings.Contains(text, "tests {") {
		t.Fatalf("tests block missing:\n%s", text)
	}
}
