package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Ensure the XML helper and generated WSDL schema tests run inside Bru's VM2.
func TestWSDLGeneratedTestsPassInBru(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		op := extractSoapOperation(bodyBytes)
		if op == "" {
			op = "Operation"
		}
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockSoapResponse(op)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tmp := t.TempDir()
	spec := filepath.Join("..", "..", "sampledata", "wsdl", "schema_sample.wsdl")
	if err := ImportWSDL(context.Background(), Options{Source: spec, OutputDir: tmp, GenerateTests: true}); err != nil {
		t.Fatalf("import wsdl: %v", err)
	}

	envDir := filepath.Join(tmp, "environments")
	_ = os.MkdirAll(envDir, 0o755)
	envPath := filepath.Join(envDir, "local.bru")
	if err := os.WriteFile(envPath, []byte("vars {\n  baseUrl: "+srv.URL+"\n}\n"), 0o644); err != nil {
		t.Fatalf("write env: %v", err)
	}

	report := filepath.Join(tmp, "bru.wsdl.json")
	cmd := exec.Command("bru", "run", ".", "-r", "--env-file", filepath.Join("environments", "local.bru"), "--reporter-json", report)
	cmd.Dir = tmp
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bru run failed: %v output=%s", err, out)
	}

	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var payload []map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if len(payload) == 0 {
		t.Fatalf("empty reporter payload")
	}

	failures := 0
	for _, iter := range payload {
		results, _ := iter["results"].([]any)
		for _, r := range results {
			rm, ok := r.(map[string]any)
			if !ok {
				continue
			}
			if bruResultFailed(rm) {
				failures++
			}
		}
	}
	if failures > 0 {
		t.Fatalf("bru reported %d failing results; output follows:\n%s", failures, string(out))
	}
}

func bruResultFailed(r map[string]any) bool {
	status := strings.ToLower(fmt.Sprint(r["status"]))
	if status == "fail" || status == "error" {
		return true
	}
	if trs, ok := r["testResults"].([]any); ok {
		for _, tr := range trs {
			if trm, ok := tr.(map[string]any); ok {
				ts := strings.ToLower(fmt.Sprint(trm["status"]))
				if ts == "fail" || ts == "error" {
					return true
				}
			}
		}
	}
	if ars, ok := r["assertionResults"].([]any); ok {
		for _, ar := range ars {
			if arm, ok := ar.(map[string]any); ok {
				ts := strings.ToLower(fmt.Sprint(arm["status"]))
				if ts == "fail" || ts == "error" {
					return true
				}
			}
		}
	}
	return false
}
