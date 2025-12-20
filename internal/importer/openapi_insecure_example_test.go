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

// Ensure remote examples fetched over self-signed TLS are honored when --insecure is set.
func TestImportOpenAPIExternalExampleInsecure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"hello":"world","id":1}`))
	}))
	defer srv.Close()

	spec := `openapi: 3.0.0
info:
  title: remote-ex
  version: "1.0"
paths:
  /hello:
    post:
      requestBody:
        required: true
        content:
          application/json:
            examples:
              sample:
                externalValue: ` + srv.URL + `/example.json
      responses:
        '200':
          description: ok
`

	tmp := t.TempDir()
	specPath := filepath.Join(tmp, "spec.yaml")
	outDir := filepath.Join(tmp, "out")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	opts := Options{
		Source:          specPath,
		OutputDir:       outDir,
		Insecure:        true,
		AllowRemoteRefs: true,
	}
	if err := ImportOpenAPI(context.Background(), opts); err != nil {
		t.Fatalf("import insecure: %v", err)
	}

	var bruPath string
	_ = filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".bru") && !strings.Contains(path, string(filepath.Separator)+"environments"+string(filepath.Separator)) {
			bruPath = path
		}
		return nil
	})
	if bruPath == "" {
		t.Fatalf("no bru file produced")
	}

	body := string(mustRead(t, bruPath))
	if !strings.Contains(body, `"hello":"world"`) {
		t.Fatalf("expected remote example body to be embedded, got:\n%s", body)
	}
}
