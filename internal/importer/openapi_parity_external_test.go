//go:build external_import_parity

package importer

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This test is opt-in (build tag external_import_parity) and also requires
// OPENAPI_PARITY_URLS env to be set (comma-separated). It runs bru import and
// gru import on each URL and compares basic outputs (report presence and count
// of generated .bru files). No external specs are stored in the repo.
func TestExternalOpenAPIImportParity(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	urlsEnv := os.Getenv("OPENAPI_PARITY_URLS")
	if urlsEnv == "" {
		t.Skip("set OPENAPI_PARITY_URLS to run")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	urls := strings.Split(urlsEnv, ",")

	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		t.Run(u, func(t *testing.T) {
			tmp, err := os.MkdirTemp("", "gru-oas-parity-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmp)

			specPath := filepath.Join(tmp, "spec.json")
			if err := downloadToFile(u, specPath); err != nil {
				t.Fatalf("download: %v", err)
			}

			bruOut := filepath.Join(tmp, "bru")
			gruOut := filepath.Join(tmp, "gru")
			bruCmd := exec.Command("bru", "import", "openapi", "--source", specPath, "--output", bruOut)
			if out, err := bruCmd.CombinedOutput(); err != nil {
				t.Fatalf("bru import failed: %v output=%s", err, out)
			}

			gruBin := filepath.Join(tmp, "gru-bin")
			build := exec.Command("go", "build", "-o", gruBin, "./cmd/gru")
			build.Dir = filepath.Join("..", "..")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build gru: %v output=%s", err, out)
			}
			gruCmd := exec.Command(gruBin, "import", "openapi", "--source", specPath, "--output", gruOut, "--disable-test-generation")
			if out, err := gruCmd.CombinedOutput(); err != nil {
				t.Fatalf("gru import failed: %v output=%s", err, out)
			}

			bruOps := extractOps(t, bruOut)
			gruOps := extractOps(t, gruOut)
			if missing := diffOps(bruOps, gruOps); len(missing) > 0 {
				t.Fatalf("missing ops in gru: %v", missing)
			}
		})
	}
}

func downloadToFile(url, path string) error {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func countBruFiles(t *testing.T, dir string) int {
	t.Helper()
	count := 0
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := strings.ToLower(d.Name())
		if !d.IsDir() && strings.HasSuffix(name, ".bru") && name != "folder.bru" {
			count++
		}
		return nil
	})
	return count
}
