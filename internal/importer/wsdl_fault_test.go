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

// Ensure generated tests fail cleanly on SOAP Fault responses.
func TestWSDLSoapFaultFails(t *testing.T) {
	spec := filepath.Join("..", "..", "sampledata", "wsdl", "schema_sample.wsdl")

	// Server always returns Fault
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><soap:Fault><faultcode>soap:Server</faultcode><faultstring>boom</faultstring></soap:Fault></soap:Body></soap:Envelope>`))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	if err := ImportWSDL(context.Background(), Options{Source: spec, OutputDir: tmp, GenerateTests: true}); err != nil {
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
	if sum.Failed == 0 {
		t.Fatalf("expected failures when SOAP Fault is returned")
	}
}
