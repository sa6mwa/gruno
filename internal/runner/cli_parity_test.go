package runner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Parity check: bru vs gru on sampledata with reporter outputs.
func TestCLIBruParity(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	srv := suiteServer()
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "gru-parity-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	base := findSampledata(t)
	if err := copyTree(base, tmp); err != nil {
		t.Fatalf("copy sampledata: %v", err)
	}
	envPath := filepath.Join(tmp, "environments", "local.bru")
	replaceInFile(envPath, "http://127.0.0.1:0", srv.URL)
	replaceInFile(envPath, "http://127.0.0.1:0/graphql", srv.URL+"/graphql")

	bruReport := filepath.Join(tmp, "bru-report.json")
	gruReport := filepath.Join(tmp, "gru-report.json")

	// bru run (recursive to mirror our default runner discovery)
	bruCmd := exec.Command("bru", "run", ".", "-r", "--env-file", envPath, "--reporter-json", bruReport)
	bruCmd.Dir = tmp
	if out, err := bruCmd.CombinedOutput(); err != nil {
		t.Fatalf("bru run failed: %v output=%s", err, out)
	}

	// build gru binary
	gruBin := filepath.Join(tmp, "gru")
	build := exec.Command("go", "build", "-o", gruBin, "./cmd/gru")
	build.Dir = filepath.Join("..", "..") // from internal/runner
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build gru: %v output=%s", err, out)
	}

	gruCmd := exec.Command(gruBin, "run", tmp, "-r", "--env", envPath, "--reporter-json", gruReport)
	if out, err := gruCmd.CombinedOutput(); err != nil {
		t.Fatalf("gru run failed: %v output=%s", err, out)
	}

	bruReqs, bruFails := parseBruReport(t, bruReport)
	gruReqs, gruFails := parseGruReport(t, gruReport)

	if bruReqs != gruReqs {
		t.Fatalf("request count mismatch bru=%d gru=%d", bruReqs, gruReqs)
	}
	if bruFails != gruFails {
		t.Fatalf("failure count mismatch bru=%d gru=%d", bruFails, gruFails)
	}
}

func parseBruReport(t *testing.T, path string) (reqs, fails int) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("read bru report: %v", err)
	}
	defer f.Close()
	var payload []struct {
		Results []struct {
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("parse bru report: %v", err)
	}
	for _, iter := range payload {
		reqs += len(iter.Results)
		for _, r := range iter.Results {
			if r.Status == "fail" {
				fails++
			}
		}
	}
	return
}

func parseGruReport(t *testing.T, path string) (reqs, fails int) {
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("read gru report: %v", err)
	}
	defer f.Close()
	var sum RunSummary
	if err := json.NewDecoder(f).Decode(&sum); err != nil {
		t.Fatalf("parse gru report: %v", err)
	}
	reqs = len(sum.Cases)
	fails = sum.Failed
	return
}

// ensure imported helper isn't optimized away in this package
var _ = context.Background
