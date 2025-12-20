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

// Ensure apiKey security schemes become headers/query plus env vars.
func TestOpenAPIAuthApiKeyHeaderGeneratesHeadersAndEnv(t *testing.T) {
	spec := `
openapi: 3.0.3
info: {title: auth, version: 1.0.0}
servers: [{url: http://example.com}]
components:
  securitySchemes:
    ApiKey:
      type: apiKey
      in: header
      name: X-API-Key
security:
  - ApiKey: []
paths:
  /ping:
    get:
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  msg: {type: string}
`
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "out")
	if err := ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: out, GenerateTests: false}); err != nil {
		t.Fatalf("import: %v", err)
	}

	files, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	var bruPath string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".bru") {
			bruPath = filepath.Join(out, f.Name())
			break
		}
	}
	if bruPath == "" {
		t.Fatalf("no bru file generated")
	}
	content, err := os.ReadFile(bruPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "headers {\n  X-API-Key: {{apikey}}\n}") {
		t.Fatalf("expected api key header in bru, got:\n%s", content)
	}

	env, err := os.ReadFile(filepath.Join(out, "environments", "local.bru"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(string(env)), "apikey: changeme") {
		t.Fatalf("expected apiKey var in env, got:\n%s", env)
	}
}

// Ensure exampleValue URLs are fetched and used.
func TestOpenAPIExampleValueHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"foo":"bar"}`))
	}))
	defer srv.Close()

	spec := `
openapi: 3.0.3
info: {title: ex, version: 1.0.0}
paths:
  /demo:
    post:
      requestBody:
        required: true
        content:
          application/json:
            exampleValue: ` + srv.URL + `/ex.json
      responses:
        '200':
          description: ok
`
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := ImportOpenAPI(context.Background(), Options{
		Source:          specPath,
		OutputDir:       out,
		Insecure:        true,
		AllowRemoteRefs: true,
		GenerateTests:   false,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	bruPath := findFirstBru(t, out)
	bru, err := os.ReadFile(bruPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bru), `"foo": "bar"`) {
		t.Fatalf("expected fetched example payload, got:\n%s", bru)
	}
}

// Ensure exampleValue file:// is loaded.
func TestOpenAPIExampleValueFile(t *testing.T) {
	dir := t.TempDir()
	exPath := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(exPath, []byte(`{"name":"file"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := `
openapi: 3.0.3
info: {title: ex-file, version: 1.0.0}
paths:
  /demo:
    post:
      requestBody:
        required: true
        content:
          application/json:
            exampleValue: file://` + exPath + `
      responses:
        '200':
          description: ok
`
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: out, GenerateTests: false}); err != nil {
		t.Fatalf("import: %v", err)
	}

	bruPath := findFirstBru(t, out)
	bru, err := os.ReadFile(bruPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bru), `"name": "file"`) {
		t.Fatalf("expected file example payload, got:\n%s", bru)
	}
}

// Relative JSON exampleValue resolves from the spec directory without flags.
func TestOpenAPIExampleValueRelativeJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "payload.json"), []byte(`{"rel":"ok"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := `
openapi: 3.0.3
info: {title: ex-rel, version: 1.0.0}
paths:
  /demo:
    post:
      requestBody:
        required: true
        content:
          application/json:
            exampleValue: payload.json
      responses:
        '200':
          description: ok
`
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: out, GenerateTests: false}); err != nil {
		t.Fatalf("import: %v", err)
	}
	bru := readFirstBru(t, out)
	if !strings.Contains(bru, `"rel": "ok"`) {
		t.Fatalf("expected relative JSON example payload, got:\n%s", bru)
	}
}

// Relative XML exampleValue resolves from the spec directory without flags.
func TestOpenAPIExampleValueRelativeXML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "payload.xml"), []byte(`<hello><who>xml</who></hello>`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := `
openapi: 3.0.3
info: {title: ex-rel-xml, version: 1.0.0}
paths:
  /demo:
    post:
      requestBody:
        required: true
        content:
          application/xml:
            exampleValue: payload.xml
      responses:
        '200':
          description: ok
`
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: out, GenerateTests: false}); err != nil {
		t.Fatalf("import: %v", err)
	}
	bru := readFirstBru(t, out)
	if !strings.Contains(bru, "body:xml") || !strings.Contains(bru, "<hello><who>xml</who></hello>") {
		t.Fatalf("expected relative XML example payload, got:\n%s", bru)
	}
}

// Ensure XML exampleValue is emitted as body:xml.
func TestOpenAPIExampleValueXML(t *testing.T) {
	spec := `
openapi: 3.0.3
info: {title: ex-xml, version: 1.0.0}
paths:
  /demo:
    post:
      requestBody:
        required: true
        content:
          application/xml:
            exampleValue: |
              <voucher><id>1</id><amount>10</amount></voucher>
      responses:
        '200':
          description: ok
`
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: out, GenerateTests: false}); err != nil {
		t.Fatalf("import: %v", err)
	}
	bru := readFirstBru(t, out)
	if !strings.Contains(bru, "body:xml") || !strings.Contains(bru, "<voucher><id>1</id><amount>10</amount></voucher>") {
		t.Fatalf("expected xml body payload, got:\n%s", bru)
	}
}

// Remote examples blocked without allow-remote-refs when spec is file-based.
func TestOpenAPIExampleValueHTTPBlockedWithoutFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"foo":"bar"}`))
	}))
	defer srv.Close()

	spec := `
openapi: 3.0.3
info: {title: ex-block, version: 1.0.0}
paths:
  /demo:
    post:
      requestBody:
        required: true
        content:
          application/json:
            exampleValue: ` + srv.URL + `
      responses:
        '200': {description: ok}
`
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: out, GenerateTests: false}); err != nil {
		t.Fatalf("import: %v", err)
	}
	bru := readFirstBru(t, out)
	if strings.Contains(bru, `"foo":"bar"`) || strings.Contains(bru, `"foo": "bar"`) {
		t.Fatalf("expected remote example to be skipped without allow-remote-refs, got:\n%s", bru)
	}
}

// Absolute file:// example is blocked unless --allow-file-refs.
func TestOpenAPIExampleValueAbsoluteFileBlockedWithoutFlag(t *testing.T) {
	baseDir := t.TempDir()
	outsideDir := t.TempDir()
	exPath := filepath.Join(outsideDir, "payload.json")
	if err := os.WriteFile(exPath, []byte(`{"blocked":"nope"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := `
openapi: 3.0.3
info: {title: ex-abs, version: 1.0.0}
paths:
  /demo:
    post:
      requestBody:
        required: true
        content:
          application/json:
            exampleValue: file://` + exPath + `
      responses:
        '200': {description: ok}
`
	specPath := filepath.Join(baseDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(baseDir, "out")
	if err := ImportOpenAPI(context.Background(), Options{Source: specPath, OutputDir: out, GenerateTests: false}); err != nil {
		t.Fatalf("import: %v", err)
	}
	bru := readFirstBru(t, out)
	if strings.Contains(bru, `"blocked":"nope"`) || strings.Contains(bru, `"blocked": "nope"`) {
		t.Fatalf("expected absolute file example to be blocked without allow-file-refs, got:\n%s", bru)
	}
}

// Absolute file:// example allowed when allow-file-refs is true.
func TestOpenAPIExampleValueAbsoluteFileAllowedWithFlag(t *testing.T) {
	baseDir := t.TempDir()
	outsideDir := t.TempDir()
	exPath := filepath.Join(outsideDir, "payload.json")
	if err := os.WriteFile(exPath, []byte(`{"allowed":"yes"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := `
openapi: 3.0.3
info: {title: ex-abs-allow, version: 1.0.0}
paths:
  /demo:
    post:
      requestBody:
        required: true
        content:
          application/json:
            exampleValue: file://` + exPath + `
      responses:
        '200': {description: ok}
`
	specPath := filepath.Join(baseDir, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(baseDir, "out")
	if err := ImportOpenAPI(context.Background(), Options{
		Source:        specPath,
		OutputDir:     out,
		AllowFileRefs: true,
		GenerateTests: false,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}
	bru := readFirstBru(t, out)
	if !strings.Contains(bru, `"allowed": "yes"`) {
		t.Fatalf("expected absolute file example to be loaded with allow-file-refs, got:\n%s", bru)
	}
}

// Remote examples allowed when same-origin (spec fetched from that origin).
func TestOpenAPIExampleValueHTTPSameOriginAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spec.yaml":
			w.Write([]byte(`
openapi: 3.0.3
info: {title: ex-same, version: 1.0.0}
paths:
  /demo:
    post:
      requestBody:
        required: true
        content:
          application/json:
            exampleValue: /payload.json
      responses:
        '200': {description: ok}
`))
		case "/payload.json":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"foo":"bar"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "out")
	if err := ImportOpenAPI(context.Background(), Options{Source: srv.URL + "/spec.yaml", OutputDir: out, GenerateTests: false}); err != nil {
		t.Fatalf("import: %v", err)
	}
	bru := readFirstBru(t, out)
	if !strings.Contains(bru, `"foo": "bar"`) {
		t.Fatalf("expected same-origin remote example to be fetched, got:\n%s", bru)
	}
}

func readFirstBru(t *testing.T, root string) string {
	t.Helper()
	path := findFirstBru(t, root)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func findFirstBru(t *testing.T, root string) string {
	t.Helper()
	var found string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".bru") && found == "" {
			found = path
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no .bru file found in %s", root)
	}
	return found
}
