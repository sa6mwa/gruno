package importer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oasdiff/yaml"
)

// Parity against stored OpenAPI specs (bru import vs gru import).
func TestOpenAPILocalParity(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	specs := []struct {
		name string
		path string
	}{
		{"visma_control_edge", filepath.Join("..", "..", "sampledata", "openapi", "visma_control_edge.json")},
		{"cinode_v0_1", filepath.Join("..", "..", "sampledata", "openapi", "cinode_v0.1.json")},
		{"inyett_v3", filepath.Join("..", "..", "sampledata", "openapi", "inyett_v3.json")},
		{"lockd", filepath.Join("..", "..", "sampledata", "openapi", "lockd.json")},
		{"yaml_fixture", filepath.Join("..", "..", "sampledata", "openapi", "yaml_fixture.yaml")},
	}

	for _, spec := range specs {
		t.Run(spec.name, func(t *testing.T) {
			tmp, err := os.MkdirTemp("", "gru-oas-local-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmp)

			sanitized := filepath.Join(tmp, spec.name+"-sanitized.json")
			if err := sanitizeSpecSummaries(spec.path, sanitized); err != nil {
				t.Fatalf("sanitize: %v", err)
			}

			bruOut := filepath.Join(tmp, "bru")
			gruOut := filepath.Join(tmp, "gru")

			bruCmd := exec.Command("bru", "import", "openapi", "--source", sanitized, "--output", bruOut)
			if out, err := bruCmd.CombinedOutput(); err != nil {
				t.Fatalf("bru import failed: %v output=%s", err, out)
			}

			gruBin := filepath.Join(tmp, "gru-bin")
			build := exec.Command("go", "build", "-o", gruBin, "./cmd/gru")
			build.Dir = filepath.Join("..", "..")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build gru: %v output=%s", err, out)
			}

			gruCmd := exec.Command(gruBin, "import", "openapi", "--source", sanitized, "--output", gruOut, "--disable-test-generation")
			if out, err := gruCmd.CombinedOutput(); err != nil {
				t.Fatalf("gru import failed: %v output=%s", err, out)
			}

			bruOps := extractOps(t, bruOut)
			gruOps := extractOps(t, gruOut)
			missing := diffOps(bruOps, gruOps)
			if len(missing) > 0 {
				t.Fatalf("bru ops missing in gru: %v", missing)
			}
		})
	}
}

// sanitizeSpecSummaries truncates long summary/operationId fields to avoid filesystem limits.
func sanitizeSpecSummaries(in, out string) error {
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		// Try YAML
		if err = yaml.Unmarshal(data, &doc); err != nil {
			return err
		}
	}
	paths, _ := doc["paths"].(map[string]any)
	for _, p := range paths {
		m, _ := p.(map[string]any)
		for _, rawOp := range m {
			op, ok := rawOp.(map[string]any)
			if !ok {
				continue
			}
			if s, ok := op["summary"].(string); ok {
				op["summary"] = truncate(s, 80)
			}
			if s, ok := op["operationId"].(string); ok {
				op["operationId"] = truncate(s, 60)
			}
		}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, b, 0o644)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func extractOps(t *testing.T, dir string) map[string]struct{} {
	t.Helper()
	ops := map[string]struct{}{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := strings.ToLower(d.Name())
		if d.IsDir() || !strings.HasSuffix(name, ".bru") || name == "folder.bru" {
			return nil
		}
		method, url := parseVerbAndURL(t, p)
		if method != "" && url != "" {
			ops[method+" "+url] = struct{}{}
		}
		return nil
	})
	return ops
}

func parseVerbAndURL(t *testing.T, path string) (string, string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(string(data), "\n")
	method := ""
	url := ""
	for i, l := range lines {
		trimmed := strings.TrimSpace(strings.ToLower(l))
		switch {
		case strings.HasPrefix(trimmed, "get"):
			method = "get"
		case strings.HasPrefix(trimmed, "post"):
			method = "post"
		case strings.HasPrefix(trimmed, "put"):
			method = "put"
		case strings.HasPrefix(trimmed, "patch"):
			method = "patch"
		case strings.HasPrefix(trimmed, "delete"):
			method = "delete"
		case strings.HasPrefix(trimmed, "head"):
			method = "head"
		case strings.HasPrefix(trimmed, "options"):
			method = "options"
		case strings.HasPrefix(trimmed, "trace"):
			method = "trace"
		}
		if strings.HasPrefix(trimmed, "url:") {
			url = strings.TrimSpace(strings.TrimPrefix(l, "url:"))
			url = strings.ReplaceAll(url, "{{baseUrl}}", "")
		}
		if method != "" && url != "" {
			break
		}
		// early stop if we've passed possible url
		if method != "" && i > 20 {
			break
		}
	}
	return strings.ToUpper(method), url
}

func diffOps(a, b map[string]struct{}) []string {
	var missing []string
	for k := range a {
		if _, ok := b[k]; !ok {
			missing = append(missing, k)
		}
	}
	return missing
}
