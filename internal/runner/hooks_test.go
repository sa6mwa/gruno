package runner

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"pkt.systems/gruno/internal/parser"
	"pkt.systems/pslog"
)

func TestPrePostHooksInvoked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	bru := `meta { name: Hooked }

get {
  url: ` + srv.URL + `/ping
}

tests {
  test("ok", function(){ expect(res.status).to.equal(200); });
}
`
	bruPath := filepath.Join(tmp, "req.bru")
	if err := os.WriteFile(bruPath, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(context.Background(), bruPath)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Request.URL == "" {
		t.Fatalf("parsed URL empty")
	}
	exp := newExpander(map[string]string{"baseUrl": srv.URL})
	if req, err := buildHTTPRequest(parsed, exp); err != nil || req.URL.String() == "" {
		t.Fatalf("build req: %v url=%v", err, req)
	}

	var preCalled, postCalled bool

	g, err := New(context.Background(), WithPreRequestHook(func(ctx context.Context, info HookInfo, req *http.Request, logger pslog.Base) error {
		preCalled = true
		if info.Name == "" || info.FilePath == "" || info.URL == "" || info.Method == "" {
			t.Fatalf("missing hook info: %+v", info)
		}
		req.Header.Set("X-From-Pre", "1")
		return nil
	}), WithPostRequestHook(func(ctx context.Context, info HookInfo, res CaseResult, logger pslog.Base) error {
		postCalled = true
		if !res.Passed {
			t.Fatalf("unexpected failure")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	sum, err := g.RunFolder(context.Background(), tmp, RunOptions{})
	if err != nil {
		t.Fatalf("runfolder: %v", err)
	}
	if sum.Failed != 0 || sum.Passed != 1 {
		t.Fatalf("expected 1 pass, got %+v", sum)
	}
	if !preCalled || !postCalled {
		t.Fatalf("hooks not invoked pre=%v post=%v", preCalled, postCalled)
	}
}

func TestExternalHooksRunAndLog(t *testing.T) {
	tmp := t.TempDir()
	hookOut := filepath.Join(tmp, "hook.out")
	script := writeRunnerHookScript(t, tmp, hookOut)

	buf := &bytes.Buffer{}
	r := &runner{logger: pslog.NewStructured(buf)}
	file := parser.ParsedFile{
		FilePath: "case.bru",
		Meta:     parser.MetaBlock{Name: "HookCase", Tags: []string{"hook"}},
		Request:  parser.RequestBlock{Verb: "GET", URL: "http://example.com"},
	}
	res := CaseResult{Passed: true, Status: 200, Duration: time.Millisecond}

	if err := r.runExternalHook(context.Background(), "pre", hookCommand(script), file, nil); err != nil {
		t.Fatalf("pre hook: %v", err)
	}
	if err := r.runExternalHook(context.Background(), "post", hookCommand(script), file, &res); err != nil {
		t.Fatalf("post hook: %v", err)
	}

	data, err := os.ReadFile(hookOut)
	if err != nil {
		t.Fatalf("read hook out: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "pre ") || !strings.Contains(content, "post 200") {
		t.Fatalf("hook output missing info: %q", content)
	}
}

func TestExternalHookFailureAborts(t *testing.T) {
	tmp := t.TempDir()
	bruPath := filepath.Join(tmp, "req.bru")
	bru := `meta { name: FailHook }

get { url: http://127.0.0.1:0 }
`
	if err := os.WriteFile(bruPath, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	script := writeFailHookScript(t, tmp)

	g, err := New(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.RunFolder(context.Background(), tmp, RunOptions{PreHookCmd: hookCommand(script)})
	if err == nil {
		t.Fatalf("expected hook error")
	}
}

func writeRunnerHookScript(t *testing.T, dir, hookOut string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		script := filepath.Join(dir, "hook.cmd")
		body := "@echo off\r\n" +
			"echo %GRU_HOOK_PHASE% %GRU_STATUS% %GRU_FILE% >>" + hookOut + "\r\n"
		if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return script
	}
	script := filepath.Join(dir, "hook.sh")
	body := "#!/bin/sh\necho \"$GRU_HOOK_PHASE $GRU_STATUS $GRU_FILE\" >>" + hookOut + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func writeFailHookScript(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		script := filepath.Join(dir, "fail.cmd")
		body := "@echo off\r\nexit /b 2\r\n"
		if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return script
	}
	script := filepath.Join(dir, "fail.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func hookCommand(script string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/C", script}
	}
	return []string{script}
}

func TestHookInfoFallbackSeqTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	bru := `meta {
  seq: 7
  tags: [alpha, beta]
}

get {
  url: {{baseUrl}}/ping
}
`
	bruPath := filepath.Join(tmp, "req.bru")
	if err := os.WriteFile(bruPath, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	parsed, err := parser.ParseFile(context.Background(), bruPath)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Meta.Seq != 7 {
		t.Fatalf("parsed seq mismatch: %v", parsed.Meta.Seq)
	}
	if parsed.Request.URL == "" {
		t.Fatalf("parsed url empty")
	}

	var seen HookInfo
	g, err := New(context.Background(), WithPreRequestHook(func(ctx context.Context, info HookInfo, req *http.Request, logger pslog.Base) error {
		seen = info
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	_, err = g.RunFolder(context.Background(), tmp, RunOptions{
		Vars: map[string]string{"baseUrl": srv.URL},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if seen.Name != "req.bru" {
		t.Fatalf("name fallback mismatch: %s", seen.Name)
	}
	if seen.Seq != 7 {
		t.Fatalf("seq mismatch: %v", seen.Seq)
	}
	if len(seen.Tags) != 2 || seen.Tags[0] != "alpha" || seen.Tags[1] != "beta" {
		t.Fatalf("tags mismatch: %#v", seen.Tags)
	}
	if seen.Method != "GET" {
		t.Fatalf("method mismatch: %s", seen.Method)
	}
	wantURL := srv.URL + "/ping"
	if seen.URL != wantURL {
		t.Fatalf("url mismatch: %s != %s", seen.URL, wantURL)
	}
}

func TestHookInfoReflectsPreRequestScriptMutation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	bru := `meta { name: JSChanges }

get { url: {{baseUrl}}/orig }

script:pre-request {
  req.url = env("baseUrl") + "/mut";
}
`
	bruPath := filepath.Join(tmp, "mut.bru")
	if err := os.WriteFile(bruPath, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	var postURL string
	g, err := New(context.Background(), WithPostRequestHook(func(ctx context.Context, info HookInfo, res CaseResult, logger pslog.Base) error {
		postURL = info.URL
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := g.RunFolder(context.Background(), tmp, RunOptions{
		Vars: map[string]string{"baseUrl": srv.URL},
	}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.HasSuffix(postURL, "/mut") {
		t.Fatalf("expected mutated url, got %s", postURL)
	}
}

func TestHookLoggerSameInstance(t *testing.T) {
	tmp := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	bru := `meta { name: LogHook }

get {
  url: ` + srv.URL + `
}
`
	bruPath := filepath.Join(tmp, "log.bru")
	if err := os.WriteFile(bruPath, []byte(bru), 0o644); err != nil {
		t.Fatal(err)
	}

	if pf, err := parser.ParseFile(context.Background(), bruPath); err != nil {
		t.Fatalf("parse: %v", err)
	} else if pf.Request.URL == "" {
		t.Fatalf("parsed url empty")
	}

	buf := &bytes.Buffer{}
	baseLogger := pslog.NewStructured(buf).With("marker", "hook")

	baseLogger.Info("before-run")

	var preCalled bool
	g, err := New(context.Background(),
		WithLogger(baseLogger),
		WithPreRequestHook(func(ctx context.Context, info HookInfo, req *http.Request, logger pslog.Base) error {
			preCalled = true
			logger.Info("hook-log")
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	sum, err := g.RunFolder(context.Background(), tmp, RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if sum.Total != 1 {
		t.Fatalf("expected one case, got %+v", sum)
	}

	if !preCalled {
		t.Fatalf("pre hook not called")
	}

	out := buf.String()
	if !strings.Contains(out, "\"marker\":\"hook\"") || !strings.Contains(out, "\"msg\":\"hook-log\"") || !strings.Contains(out, "\"msg\":\"before-run\"") {
		t.Fatalf("hook logger output missing, got %q", out)
	}
}
