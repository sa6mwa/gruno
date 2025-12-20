package runner

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Helper to spin up a simple server for control-flow tests.
func controlTestServer(t *testing.T) (url string, hits *int32, closeFn func()) {
	srv := suiteServer()
	hits = new(int32)

	orig := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		orig.ServeHTTP(w, r)
	})

	return srv.URL, hits, srv.Close
}

func TestRunFolderTestsOnlySkipsNonTestCases(t *testing.T) {
	base := t.TempDir()
	url, _, closeSrv := controlTestServer(t)
	t.Cleanup(closeSrv)

	envPath := filepath.Join(base, "environments")
	_ = os.MkdirAll(envPath, 0o755)
	envFile := filepath.Join(envPath, "local.bru")
	_ = os.WriteFile(envFile, []byte("vars {\n  baseUrl: "+url+"\n}\n"), 0o644)

	withTests := `meta {
  name: With Tests
}
get {
  url: {{baseUrl}}/status/200
}

tests {
  test("ok", function() { expect(res.status).to.equal(200); });
}
`
	withoutTests := `meta {
  name: No Tests
}
get {
  url: {{baseUrl}}/status/200
}
`
	_ = os.MkdirAll(filepath.Join(base, "cases"), 0o755)
	_ = os.WriteFile(filepath.Join(base, "cases", "with-tests.bru"), []byte(withTests), 0o644)
	_ = os.WriteFile(filepath.Join(base, "cases", "without-tests.bru"), []byte(withoutTests), 0o644)

	g, err := New(context.Background())
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	sum, err := g.RunFolder(context.Background(), filepath.Join(base, "cases"), RunOptions{EnvPath: envFile, TestsOnly: true})
	if err != nil {
		t.Fatalf("runfolder: %v", err)
	}
	if sum.Total != 2 || sum.Passed != 1 || sum.Skipped != 1 || sum.Failed != 0 {
		t.Fatalf("unexpected summary %+v", sum)
	}
}

func TestRunFolderDelayBetweenCases(t *testing.T) {
	base := t.TempDir()
	url, _, closeSrv := controlTestServer(t)
	t.Cleanup(closeSrv)

	envPath := filepath.Join(base, "environments")
	_ = os.MkdirAll(envPath, 0o755)
	envFile := filepath.Join(envPath, "local.bru")
	_ = os.WriteFile(envFile, []byte("vars {\n  baseUrl: "+url+"\n}\n"), 0o644)

	bru := `meta {
  name: Case %d
}
get {
  url: {{baseUrl}}/status/200
}

tests {
  test("ok", function() { expect(res.status).to.equal(200); });
}
`
	_ = os.MkdirAll(filepath.Join(base, "cases"), 0o755)
	_ = os.WriteFile(filepath.Join(base, "cases", "one.bru"), fmt.Appendf(nil, bru, 1), 0o644)
	_ = os.WriteFile(filepath.Join(base, "cases", "two.bru"), fmt.Appendf(nil, bru, 2), 0o644)

	g, err := New(context.Background())
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	delay := 80 * time.Millisecond
	start := time.Now()
	sum, err := g.RunFolder(context.Background(), filepath.Join(base, "cases"), RunOptions{EnvPath: envFile, Delay: delay})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("runfolder: %v", err)
	}
	if sum.Passed != 2 || sum.Failed != 0 {
		t.Fatalf("unexpected summary %+v", sum)
	}
	if elapsed < delay {
		t.Fatalf("expected at least %v elapsed, got %v", delay, elapsed)
	}
}

func TestRunFolderBailStopsAfterFirstFailure(t *testing.T) {
	base := t.TempDir()
	url, hits, closeSrv := controlTestServer(t)
	t.Cleanup(closeSrv)

	envPath := filepath.Join(base, "environments")
	_ = os.MkdirAll(envPath, 0o755)
	envFile := filepath.Join(envPath, "local.bru")
	_ = os.WriteFile(envFile, []byte("vars {\n  baseUrl: "+url+"\n}\n"), 0o644)

	failing := `meta {
  name: Fail
}
get {
  url: {{baseUrl}}/status/500
}

tests {
  test("status 500", function() { expect(res.status).to.equal(200); });
}
`
	passing := `meta {
  name: Pass
}
get {
  url: {{baseUrl}}/status/200
}

tests {
  test("ok", function() { expect(res.status).to.equal(200); });
}
`
	_ = os.MkdirAll(filepath.Join(base, "cases"), 0o755)
	_ = os.WriteFile(filepath.Join(base, "cases", "fail.bru"), []byte(failing), 0o644)
	_ = os.WriteFile(filepath.Join(base, "cases", "pass.bru"), []byte(passing), 0o644)

	g, err := New(context.Background())
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	sum, err := g.RunFolder(context.Background(), filepath.Join(base, "cases"), RunOptions{EnvPath: envFile, Bail: true})
	if err != nil {
		t.Fatalf("runfolder: %v", err)
	}
	if len(sum.Cases) != 1 {
		t.Fatalf("expected 1 executed case, got %d", len(sum.Cases))
	}
	if sum.Failed != 1 || sum.Passed != 0 {
		t.Fatalf("unexpected summary %+v", sum)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("expected 1 request, got %d", got)
	}
}

func TestRunFolderRecursiveToggle(t *testing.T) {
	base := t.TempDir()
	url, _, closeSrv := controlTestServer(t)
	t.Cleanup(closeSrv)

	envPath := filepath.Join(base, "environments")
	_ = os.MkdirAll(envPath, 0o755)
	envFile := filepath.Join(envPath, "local.bru")
	_ = os.WriteFile(envFile, []byte("vars {\n  baseUrl: "+url+"\n}\n"), 0o644)

	common := `meta {
  name: %s
}
get {
  url: {{baseUrl}}/status/200
}

tests {
  test("ok", function() { expect(res.status).to.equal(200); });
}
`
	rootDir := filepath.Join(base, "cases")
	subDir := filepath.Join(rootDir, "nested")
	_ = os.MkdirAll(subDir, 0o755)
	_ = os.WriteFile(filepath.Join(rootDir, "root.bru"), fmt.Appendf(nil, common, "root"), 0o644)
	_ = os.WriteFile(filepath.Join(subDir, "child.bru"), fmt.Appendf(nil, common, "child"), 0o644)

	g, err := New(context.Background())
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	sum, err := g.RunFolder(context.Background(), rootDir, RunOptions{EnvPath: envFile, Recursive: false, RecursiveSet: true})
	if err != nil {
		t.Fatalf("runfolder non-recursive: %v", err)
	}
	if len(sum.Cases) != 1 || sum.Total != 1 {
		t.Fatalf("expected only root case, got %+v", sum)
	}

	sum, err = g.RunFolder(context.Background(), rootDir, RunOptions{EnvPath: envFile, Recursive: true, RecursiveSet: true})
	if err != nil {
		t.Fatalf("runfolder recursive: %v", err)
	}
	if len(sum.Cases) != 2 || sum.Total != 2 {
		t.Fatalf("expected two cases with recursion, got %+v", sum)
	}

	// default (RecursiveSet false) should recurse for backwards compatibility
	sum, err = g.RunFolder(context.Background(), rootDir, RunOptions{EnvPath: envFile})
	if err != nil {
		t.Fatalf("runfolder default recurse: %v", err)
	}
	if len(sum.Cases) != 2 {
		t.Fatalf("expected default recursion to include nested case, got %+v", sum)
	}
}
