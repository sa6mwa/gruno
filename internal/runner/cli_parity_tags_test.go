package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Parity on tag filtering (include/exclude) between bru and gru.
func TestCLITagsParity(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	srv := suiteServer()
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "gru-parity-tags-")
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
	// Remove third-party sample that Bru may parse differently
	_ = os.RemoveAll(filepath.Join(tmp, "GitHub"))

	// build gru once
	gruBin := filepath.Join(tmp, "gru")
	build := exec.Command("go", "build", "-o", gruBin, "./cmd/gru")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build gru: %v output=%s", err, out)
	}

	compatDir := filepath.Join(tmp, "Compat")

	// include tag smoke
	bruReqs, bruFails, err := runBruWith(t, compatDir, envPath, []string{"--tags", "smoke"})
	if err != nil {
		t.Skipf("bru run failed (skipping parity): %v", err)
	}
	gruReqs, gruFails := runGruWith(t, gruBin, compatDir, envPath, []string{"--tags", "smoke"})
	if bruReqs != gruReqs || bruFails != gruFails {
		t.Fatalf("tags parity mismatch (include smoke) bru req/fail=%d/%d gru=%d/%d", bruReqs, bruFails, gruReqs, gruFails)
	}

	// exclude tag slow
	bruReqs, bruFails, err = runBruWith(t, compatDir, envPath, []string{"--exclude-tags", "slow"})
	if err != nil {
		t.Skipf("bru run failed (skipping parity): %v", err)
	}
	gruReqs, gruFails = runGruWith(t, gruBin, compatDir, envPath, []string{"--exclude-tags", "slow"})
	if bruReqs != gruReqs || bruFails != gruFails {
		t.Fatalf("tags parity mismatch (exclude slow) bru req/fail=%d/%d gru=%d/%d", bruReqs, bruFails, gruReqs, gruFails)
	}
}

func runBruWith(t *testing.T, targetDir, envPath string, extra []string) (reqs, fails int, err error) {
	root := filepath.Dir(filepath.Dir(envPath))
	report := filepath.Join(root, "bru-tags.json")
	targetArg := filepath.Base(targetDir)
	args := append([]string{"run", targetArg, "-r", "--env-file", envPath, "--reporter-json", report}, extra...)
	cmd := exec.Command("bru", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, 0, fmt.Errorf("bru run failed: %v output=%s", err, out)
	}
	reqs, fails = parseBruReport(t, report)
	return reqs, fails, nil
}

func runGruWith(t *testing.T, gruBin, targetDir, envPath string, extra []string) (reqs, fails int) {
	root := filepath.Dir(filepath.Dir(envPath))
	report := filepath.Join(root, "gru-tags.json")
	targetArg := filepath.Base(targetDir)
	args := append([]string{"run", targetArg, "-r", "--env", envPath, "--reporter-json", report}, extra...)
	cmd := exec.Command(gruBin, args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gru run failed: %v output=%s", err, out)
	}
	reqs, fails = parseGruReport(t, report)
	return
}
