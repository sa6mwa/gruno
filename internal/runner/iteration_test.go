package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunFolderCSVIterations(t *testing.T) {
	srv := suiteServer()
	defer srv.Close()

	tmp := t.TempDir()
	csvPath := filepath.Join(tmp, "data.csv")
	if err := os.WriteFile(csvPath, []byte("user_id,role\nalpha,admin\nbeta,viewer\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bru := `meta {
  name: CSV Iteration
  seq: 1
}

post {
  url: {{baseUrl}}/echo
}

headers {
  Content-Type: application/json
}

body:json {
  {
    "user_id": "{{user_id}}",
    "role": "{{role}}"
  }
}

tests {
  test("iteration vars available", function() {
    expect(res.body.user_id).to.equal(env("user_id"));
    expect(res.body.role).to.equal(bru.getVar("role"));
    expect(bru.runner.iterationData.has("role")).to.equal(true);
    expect(bru.runner.totalIterations).to.equal(2);
  });
}
`
	bruPath := filepath.Join(tmp, "iter.bru")
	if err := os.WriteFile(bruPath, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := New(context.Background())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	opts := RunOptions{
		Vars:         map[string]string{"baseUrl": srv.URL},
		CSVFilePath:  csvPath,
		Parallel:     false,
		RecursiveSet: true,
		Recursive:    false,
	}
	sum, err := g.RunFolder(context.Background(), tmp, opts)
	if err != nil {
		t.Fatalf("runfolder: %v", err)
	}
	if sum.Total != 2 || sum.Passed != 2 {
		for _, c := range sum.Cases {
			t.Logf("case %s passed=%v skipped=%v err=%s failures=%v", c.Name, c.Passed, c.Skipped, c.ErrorText, c.Failures)
		}
		t.Fatalf("expected 2 passes got total=%d passed=%d failed=%d", sum.Total, sum.Passed, sum.Failed)
	}
}

func TestRunFolderJSONIterations(t *testing.T) {
	srv := suiteServer()
	defer srv.Close()

	tmp := t.TempDir()
	jsonPath := filepath.Join(tmp, "data.json")
	if err := os.WriteFile(jsonPath, []byte(`[{"term":"alpha","limit":1},{"term":"beta","limit":2}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	bru := `meta {
  name: JSON Iteration
  seq: 1
}

get {
  url: {{baseUrl}}/echo-query?term={{term}}&limit={{limit}}
}

tests {
  test("query reflects iteration data", function() {
    expect(res.body.term).to.equal(env("term"));
    expect(res.body.limit).to.equal(parseInt(env("limit")));
    bru.runner.iterationData.set("term", "patched");
    expect(bru.getVar("term")).to.equal("patched");
    expect(bru.runner.iterationIndex < bru.runner.totalIterations).to.equal(true);
  });
}
`
	bruPath := filepath.Join(tmp, "iter-json.bru")
	if err := os.WriteFile(bruPath, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := New(context.Background())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	opts := RunOptions{
		Vars:         map[string]string{"baseUrl": srv.URL},
		JSONFilePath: jsonPath,
		RecursiveSet: true,
		Recursive:    false,
	}
	sum, err := g.RunFolder(context.Background(), tmp, opts)
	if err != nil {
		t.Fatalf("runfolder: %v", err)
	}
	if sum.Total != 2 || sum.Failed != 0 {
		t.Fatalf("expected 2 passes got total=%d passed=%d failed=%d", sum.Total, sum.Passed, sum.Failed)
	}
}

func TestRunFolderIterationCount(t *testing.T) {
	srv := suiteServer()
	defer srv.Close()

	tmp := t.TempDir()
	bru := `meta {
  name: Count Iteration
  seq: 1
}

get {
  url: {{baseUrl}}/get
}

tests {
  test("iteration counts propagate", function() {
    expect(bru.runner.totalIterations).to.equal(3);
  });
}
`
	bruPath := filepath.Join(tmp, "count.bru")
	if err := os.WriteFile(bruPath, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := New(context.Background())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	opts := RunOptions{
		Vars:                   map[string]string{"baseUrl": srv.URL},
		IterationCount:         3,
		RecursiveSet:           true,
		Recursive:              false,
		ReporterJSON:           "",
		ReporterJUnit:          "",
		ReporterHTML:           "",
		ReporterSkipAllHeaders: false,
	}
	sum, err := g.RunFolder(context.Background(), tmp, opts)
	if err != nil {
		t.Fatalf("runfolder: %v", err)
	}
	if sum.Total != 3 || sum.Passed != 3 {
		t.Fatalf("expected 3 passes got total=%d passed=%d failed=%d", sum.Total, sum.Passed, sum.Failed)
	}
}

func TestRunFolderParallel(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer slow.Close()

	tmp := t.TempDir()
	bru := `meta { name: Slow One }

get { url: {{baseUrl}}/slow }

tests {
  test("ok", function() { expect(res.status).to.equal(200); });
}
`
	if err := os.WriteFile(filepath.Join(tmp, "a.bru"), []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "b.bru"), []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := New(context.Background())
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	opts := RunOptions{
		Vars:         map[string]string{"baseUrl": slow.URL},
		Parallel:     true,
		RecursiveSet: true,
		Recursive:    false,
	}
	start := time.Now()
	sum, err := g.RunFolder(context.Background(), tmp, opts)
	if err != nil {
		t.Fatalf("runfolder: %v", err)
	}
	elapsed := time.Since(start)
	if sum.Total != 2 || sum.Failed != 0 {
		t.Fatalf("expected 2 passes got total=%d passed=%d failed=%d", sum.Total, sum.Passed, sum.Failed)
	}
	if elapsed >= 420*time.Millisecond {
		t.Fatalf("parallel run took too long: %v", elapsed)
	}
}
