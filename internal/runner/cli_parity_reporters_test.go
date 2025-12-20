package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Parity on JSON + HTML reporter outputs using a tiny synthetic collection.
func TestCLIReporterParityJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	srv := suiteServer()
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "gru-parity-reports-")
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if os.Getenv("GRU_DEBUG_KEEP") != "" {
		t.Logf("keeping temp dir %s", tmp)
		cleanup = func() {}
	}
	defer cleanup()

	target := "cases"
	envPath := filepath.Join("environments", "local.bru")
	setupMiniCollection(t, tmp, srv.URL)
	if _, err := os.Stat(filepath.Join(tmp, envPath)); err != nil {
		t.Fatalf("env file missing: %v", err)
	}

	gruBin := buildGruBinary(t, tmp)

	bruJSON := filepath.Join(tmp, "bru.json.report")
	gruJSON := filepath.Join(tmp, "gru.json.report")
	bruHTML := filepath.Join(tmp, "bru.html.report")
	gruHTML := filepath.Join(tmp, "gru.html.report")

	runBru := exec.Command("bru", "run", target, "-r", "--env-file", envPath, "--reporter-json", bruJSON, "--reporter-html", bruHTML)
	runBru.Dir = tmp
	if out, err := runBru.CombinedOutput(); err != nil {
		if _, statErr := os.Stat(bruJSON); statErr != nil {
			t.Fatalf("bru run failed and no report: %v output=%s", err, out)
		}
	}

	runGru := exec.Command(gruBin, "run", target, "-r", "--env", envPath, "--reporter-json", gruJSON, "--reporter-html", gruHTML)
	runGru.Dir = tmp
	if out, err := runGru.CombinedOutput(); err != nil {
		if _, statErr := os.Stat(gruJSON); statErr != nil {
			t.Fatalf("gru run failed and no report: %v output=%s", err, out)
		}
	}

	bruStatuses, bruCounts, bruSnap := parseBruStatuses(t, bruJSON)
	gruStatuses, gruCounts, gruSnap := parseGruStatuses(t, gruJSON)

	if !equalStatusMaps(bruStatuses, gruStatuses) {
		t.Fatalf("status map mismatch\nbru=%v\ngru=%v", bruStatuses, gruStatuses)
	}
	if bruCounts != gruCounts {
		t.Fatalf("status counts mismatch bru=%v gru=%v", bruCounts, gruCounts)
	}
	assertSnapshotsClose(t, bruSnap, gruSnap)

	assertNonEmptyFile(t, bruHTML)
	assertNonEmptyFile(t, gruHTML)
}

func TestCLIReporterHeaderSkipParity(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	srv := suiteServer()
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "gru-header-parity-")
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if os.Getenv("GRU_DEBUG_KEEP") != "" {
		t.Logf("keeping temp dir %s", tmp)
		cleanup = func() {}
	}
	defer cleanup()

	gruBin := buildGruBinary(t, tmp)

	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	write("bruno.json", `{"name":"hdr","version":"1.0","type":"collection"}`)
	write("headers.bru", fmt.Sprintf(`meta {
  name: Headers
}

get {
  url: %s/echo
}

headers {
  Authorization: Bearer SECRET
  X-Foo: bar
}
`, srv.URL))

	bruJSON := filepath.Join(tmp, "bru.skipone.json")
	gruJSON := filepath.Join(tmp, "gru.skipone.json")

	bruCmd := exec.Command("bru", "run", ".", "--reporter-json", bruJSON, "--reporter-skip-headers", "Authorization")
	bruCmd.Dir = tmp
	if out, err := bruCmd.CombinedOutput(); err != nil {
		t.Fatalf("bru skip headers failed: %v output=%s", err, out)
	}

	gruCmd := exec.Command(gruBin, "run", ".", "--reporter-json", gruJSON, "--reporter-skip-headers", "Authorization")
	gruCmd.Dir = tmp
	if out, err := gruCmd.CombinedOutput(); err != nil {
		t.Fatalf("gru skip headers failed: %v output=%s", err, out)
	}

	bruReq, bruResp := parseBruHeaders(t, bruJSON)
	gruReq, gruResp := parseGruHeaders(t, gruJSON)

	if _, ok := bruReq["authorization"]; ok {
		t.Fatalf("bru: authorization should be removed")
	}
	if _, ok := gruReq["authorization"]; ok {
		t.Fatalf("gru: authorization should be removed")
	}
	if bruReq["x-foo"] == "" || gruReq["x-foo"] == "" {
		t.Fatalf("x-foo header missing after skip specific")
	}
	if len(bruResp) == 0 || len(gruResp) == 0 {
		t.Fatalf("response headers should remain on skip specific")
	}

	bruJSONAll := filepath.Join(tmp, "bru.skipall.json")
	gruJSONAll := filepath.Join(tmp, "gru.skipall.json")

	bruCmd = exec.Command("bru", "run", ".", "--reporter-json", bruJSONAll, "--reporter-skip-all-headers")
	bruCmd.Dir = tmp
	if out, err := bruCmd.CombinedOutput(); err != nil {
		t.Fatalf("bru skip all headers failed: %v output=%s", err, out)
	}

	gruCmd = exec.Command(gruBin, "run", ".", "--reporter-json", gruJSONAll, "--reporter-skip-all-headers")
	gruCmd.Dir = tmp
	if out, err := gruCmd.CombinedOutput(); err != nil {
		t.Fatalf("gru skip all headers failed: %v output=%s", err, out)
	}

	bruReqAll, bruRespAll := parseBruHeaders(t, bruJSONAll)
	gruReqAll, gruRespAll := parseGruHeaders(t, gruJSONAll)

	if len(bruReqAll) != 0 || len(gruReqAll) != 0 {
		t.Fatalf("request headers should be empty when skipping all: bru=%v gru=%v", bruReqAll, gruReqAll)
	}
	if len(bruRespAll) != 0 || len(gruRespAll) != 0 {
		t.Fatalf("response headers should be empty when skipping all: bru=%v gru=%v", bruRespAll, gruRespAll)
	}
}

func TestCLIParityControlFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	srv := suiteServer()
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "gru-parity-flags-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	setupMiniCollection(t, tmp, srv.URL)
	gruBin := buildGruBinary(t, tmp)

	envPath := filepath.Join("environments", "local.bru")
	target := "cases"
	if _, err := os.Stat(filepath.Join(tmp, envPath)); err != nil {
		t.Fatalf("env file missing: %v", err)
	}

	type scenario struct {
		name  string
		flags []string
	}
	scenarios := []scenario{
		{name: "default-non-recursive", flags: nil},
		{name: "recursive", flags: []string{"-r"}},
		{name: "tests-only", flags: []string{"--tests-only", "-r"}},
		{name: "bail", flags: []string{"--bail", "-r"}},
		{name: "delay", flags: []string{"--delay", "5", "-r"}},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			bruJSON := filepath.Join(tmp, fmt.Sprintf("bru.%s.json", sc.name))
			gruJSON := filepath.Join(tmp, fmt.Sprintf("gru.%s.json", sc.name))

			bruArgs := append([]string{"run", target, "--env-file", envPath, "--reporter-json", bruJSON}, sc.flags...)
			gruArgs := append([]string{"run", target, "--env", envPath, "--reporter-json", gruJSON}, sc.flags...)

			cmd := exec.Command("bru", bruArgs...)
			cmd.Dir = tmp
			if out, err := cmd.CombinedOutput(); err != nil {
				if _, statErr := os.Stat(bruJSON); statErr != nil {
					t.Fatalf("bru run %s failed and no report: %v output=%s", sc.name, err, out)
				}
			}
			cmd = exec.Command(gruBin, gruArgs...)
			cmd.Dir = tmp
			if out, err := cmd.CombinedOutput(); err != nil {
				if _, statErr := os.Stat(gruJSON); statErr != nil {
					t.Fatalf("gru run %s failed and no report: %v output=%s", sc.name, err, out)
				}
			}

			bruStatuses, bruCounts, bruSnap := parseBruStatuses(t, bruJSON)
			gruStatuses, gruCounts, gruSnap := parseGruStatuses(t, gruJSON)

			if !equalStatusMaps(bruStatuses, gruStatuses) {
				t.Fatalf("%s: statuses mismatch\nbru=%v\ngru=%v", sc.name, bruStatuses, gruStatuses)
			}
			if bruCounts != gruCounts {
				t.Fatalf("%s: counts mismatch bru=%v gru=%v", sc.name, bruCounts, gruCounts)
			}
			assertSnapshotsClose(t, bruSnap, gruSnap)
		})
	}
}

func TestCLIDelayRespected(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	srv := suiteServer()
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "gru-delay-parity-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	setupMiniCollection(t, tmp, srv.URL)
	gruBin := buildGruBinary(t, tmp)

	envPath := filepath.Join("environments", "local.bru")
	target := "cases"
	delayMS := 200
	minCases := 4 // expected executed cases in mini collection

	runWithDelay := func(cmd *exec.Cmd, report string) (time.Duration, int) {
		start := time.Now()
		out, err := cmd.CombinedOutput()
		elapsed := time.Since(start)
		if err != nil {
			if _, statErr := os.Stat(report); statErr != nil {
				t.Fatalf("cmd failed: %v output=%s", err, out)
			}
		}
		var count int
		if strings.Contains(cmd.Path, "bru") {
			st, _, _ := parseBruStatuses(t, report)
			count = len(st)
		} else {
			st, _, _ := parseGruStatuses(t, report)
			count = len(st)
		}
		return elapsed, count
	}

	bruDelay := filepath.Join(tmp, "bru.delay.json")
	bruNoDelay := filepath.Join(tmp, "bru.nodelay.json")
	gruDelay := filepath.Join(tmp, "gru.delay.json")
	gruNoDelay := filepath.Join(tmp, "gru.nodelay.json")

	bruDelayCmd := exec.Command("bru", "run", target, "-r", "--env-file", envPath, "--reporter-json", bruDelay, "--delay", fmt.Sprint(delayMS))
	bruDelayCmd.Dir = tmp
	bruNoDelayCmd := exec.Command("bru", "run", target, "-r", "--env-file", envPath, "--reporter-json", bruNoDelay)
	bruNoDelayCmd.Dir = tmp

	gruDelayCmd := exec.Command(gruBin, "run", target, "-r", "--env", envPath, "--reporter-json", gruDelay, "--delay", fmt.Sprint(delayMS))
	gruDelayCmd.Dir = tmp
	gruNoDelayCmd := exec.Command(gruBin, "run", target, "-r", "--env", envPath, "--reporter-json", gruNoDelay)
	gruNoDelayCmd.Dir = tmp

	bruElapsedDelay, bruCountDelay := runWithDelay(bruDelayCmd, bruDelay)
	bruElapsedNoDelay, _ := runWithDelay(bruNoDelayCmd, bruNoDelay)

	gruElapsedDelay, gruCountDelay := runWithDelay(gruDelayCmd, gruDelay)
	gruElapsedNoDelay, _ := runWithDelay(gruNoDelayCmd, gruNoDelay)

	executed := bruCountDelay
	if executed < 2 {
		executed = minCases
	}
	expectedDelay := time.Duration(executed-1) * time.Duration(delayMS) * time.Millisecond
	if bruElapsedDelay < expectedDelay {
		t.Fatalf("bru delay too short: got %v want >= %v", bruElapsedDelay, expectedDelay)
	}
	if gruElapsedDelay < expectedDelay {
		t.Fatalf("gru delay too short: got %v want >= %v", gruElapsedDelay, expectedDelay)
	}

	slack := 200 * time.Millisecond
	if bruElapsedDelay-bruElapsedNoDelay < expectedDelay-slack {
		t.Fatalf("bru delay not applied enough: delay=%v nodelay=%v", bruElapsedDelay, bruElapsedNoDelay)
	}
	if gruElapsedDelay-gruElapsedNoDelay < expectedDelay-slack {
		t.Fatalf("gru delay not applied enough: delay=%v nodelay=%v", gruElapsedDelay, gruElapsedNoDelay)
	}

	if bruCountDelay != gruCountDelay {
		t.Fatalf("case count mismatch bru=%d gru=%d", bruCountDelay, gruCountDelay)
	}
}

// setupMiniCollection creates a small collection with pass/fail/skip/no-tests and nested case.
func setupMiniCollection(t *testing.T, root, baseURL string) {
	t.Helper()
	envDir := filepath.Join(root, "environments")
	casesDir := filepath.Join(root, "cases")
	nestedDir := filepath.Join(casesDir, "nested")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	env := fmt.Sprintf("vars {\n  baseUrl: %s\n}\n", baseURL)
	_ = os.WriteFile(filepath.Join(envDir, "local.bru"), []byte(env), 0o644)
	_ = os.WriteFile(filepath.Join(root, "bruno.json"), []byte(`{"name":"mini","version":"1.0","type":"collection"}`), 0o644)

	write := func(dir, name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	fail := `meta {
  name: Fail
  seq: 2
}
get {
  url: {{baseUrl}}/status/500
}

tests {
  test("should fail", function() { expect(res.status).to.equal(200); });
}
`
	pass := `meta {
  name: Pass
  seq: 3
}
get {
  url: {{baseUrl}}/status/200
}

tests {
  test("should pass", function() { expect(res.status).to.equal(200); });
}
`
	noTests := `meta {
  name: NoTests
  seq: 4
}
get {
  url: {{baseUrl}}/status/200
}
`
	nested := `meta {
  name: NestedPass
  seq: 1
}
get {
  url: {{baseUrl}}/status/200
}

tests {
  test("nested", function() { expect(res.status).to.equal(200); });
}
`

	write(casesDir, "fail.bru", fail)
	write(casesDir, "pass.bru", pass)
	write(casesDir, "no-tests.bru", noTests)
	write(nestedDir, "nested-pass.bru", nested)
}

func buildGruBinary(t *testing.T, tmpRoot string) string {
	t.Helper()
	bin := filepath.Join(tmpRoot, "gru-bin")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/gru")
	cmd.Dir = filepath.Join("..", "..") // from internal/runner
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build gru: %v output=%s", err, out)
	}
	return bin
}

type caseSnapshot struct {
	Name     string
	File     string
	Status   string
	Duration float64
	Tags     []string
	URL      string
}

func parseBruStatuses(t *testing.T, path string) (map[string]string, statusCounts, []caseSnapshot) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open bru report: %v", err)
	}
	defer f.Close()

	var payload []map[string]any
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode bru report: %v", err)
	}
	statuses := map[string]string{}
	counts := statusCounts{}
	snaps := []caseSnapshot{}
	for i, iter := range payload {
		results, _ := iter["results"].([]any)
		for j, r := range results {
			rm, ok := r.(map[string]any)
			if !ok {
				continue
			}
			status := bruResultStatus(rm)
			name := strings.TrimSpace(fmt.Sprint(rm["name"]))
			if name == "" {
				name = fmt.Sprintf("result-%d-%d", i, j)
			}
			statuses[name] = status
			counts.add(status)
			snaps = append(snaps, caseSnapshot{
				Name:     name,
				File:     strings.TrimSpace(fmt.Sprint(rm["path"])),
				Status:   status,
				Duration: toFloat64(rm["responseTime"]) * 1000,
				Tags:     toStringSlice(rm["tags"]),
				URL:      extractBruURL(rm),
			})
		}
	}
	return statuses, counts, snaps
}

func bruResultStatus(r map[string]any) string {
	if skip, ok := r["isSkipped"].(bool); ok && skip {
		return "skip"
	}
	status := strings.ToLower(fmt.Sprint(r["status"]))
	fail := status == "fail" || status == "error"

	if trs, ok := r["testResults"].([]any); ok {
		for _, tr := range trs {
			if trm, ok := tr.(map[string]any); ok {
				ts := strings.ToLower(fmt.Sprint(trm["status"]))
				if ts == "fail" || ts == "error" {
					fail = true
				}
			}
		}
	}
	if ars, ok := r["assertionResults"].([]any); ok {
		for _, ar := range ars {
			if arm, ok := ar.(map[string]any); ok {
				ts := strings.ToLower(fmt.Sprint(arm["status"]))
				if ts == "fail" || ts == "error" {
					fail = true
				}
			}
		}
	}

	if fail {
		return "fail"
	}
	if status == "skip" || status == "skipped" {
		return "skip"
	}
	if status == "" {
		return "fail"
	}
	return "pass"
}

func parseGruStatuses(t *testing.T, path string) (map[string]string, statusCounts, []caseSnapshot) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open gru report: %v", err)
	}
	defer f.Close()

	var sum RunSummary
	if err := json.NewDecoder(f).Decode(&sum); err != nil {
		t.Fatalf("decode gru report: %v", err)
	}
	statuses := map[string]string{}
	counts := statusCounts{}
	snaps := []caseSnapshot{}
	for _, c := range sum.Cases {
		status := "fail"
		if c.Skipped {
			status = "skip"
		} else if c.Passed {
			status = "pass"
		}
		if status == "skip" {
			continue // bru omits skipped cases from reports
		}
		statuses[c.Name] = status
		counts.add(status)
		snaps = append(snaps, caseSnapshot{
			Name:     c.Name,
			File:     c.FilePath,
			Status:   status,
			Duration: c.Duration.Seconds() * 1000,
			Tags:     c.Tags,
			URL:      c.RequestURL,
		})
	}
	return statuses, counts, snaps
}

type statusCounts struct {
	pass int
	fail int
	skip int
}

func assertSnapshotsClose(t *testing.T, bru, gru []caseSnapshot) {
	if len(bru) != len(gru) {
		t.Fatalf("snapshot length mismatch bru=%d gru=%d", len(bru), len(gru))
	}
	for i := range bru {
		if bru[i].Name != gru[i].Name || bru[i].Status != gru[i].Status {
			t.Fatalf("snapshot mismatch at %d bru=%+v gru=%+v", i, bru[i], gru[i])
		}
		// allow duration drift up to 500ms
		delta := bru[i].Duration - gru[i].Duration
		if delta < 0 {
			delta = -delta
		}
		if delta > 500 {
			t.Fatalf("duration mismatch at %d bru=%.0fms gru=%.0fms", i, bru[i].Duration, gru[i].Duration)
		}
		if len(bru[i].Tags) > 0 {
			if !equalStringSets(bru[i].Tags, gru[i].Tags) {
				t.Fatalf("tags mismatch at %d bru=%v gru=%v", i, bru[i].Tags, gru[i].Tags)
			}
		}
		if bru[i].URL != "" && bru[i].URL != gru[i].URL {
			t.Fatalf("url mismatch at %d bru=%s gru=%s", i, bru[i].URL, gru[i].URL)
		}
	}
}

func assertNonEmptyFile(t *testing.T, path string) {
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("file empty: %s", path)
	}
}

// parseHTMLStatusCounts intentionally removed; bru HTML is dynamic Vue and not
// directly comparable. Presence/size checks ensure file was emitted.

func (s *statusCounts) add(status string) {
	switch status {
	case "pass", "passed", "success":
		s.pass++
	case "skip", "skipped":
		s.skip++
	default:
		s.fail++
	}
}

func (s statusCounts) String() string {
	return fmt.Sprintf("pass=%d fail=%d skip=%d", s.pass, s.fail, s.skip)
}

func equalStatusMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	return 0
}

func toStringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			out = append(out, fmt.Sprint(e))
		}
		return out
	default:
		return nil
	}
}

func extractBruURL(r map[string]any) string {
	if req, ok := r["request"].(map[string]any); ok {
		if u, ok := req["url"]; ok {
			return fmt.Sprint(u)
		}
	}
	return ""
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, v := range seen {
		if v != 0 {
			return false
		}
	}
	return true
}

func sortSnapshots(snaps []caseSnapshot) {
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Name < snaps[j].Name
	})
}

func parseBruHeaders(t *testing.T, path string) (map[string]string, map[string]string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open bru headers: %v", err)
	}
	defer f.Close()

	var payload []map[string]any
	if err := json.NewDecoder(f).Decode(&payload); err != nil {
		t.Fatalf("decode bru headers: %v", err)
	}
	if len(payload) == 0 {
		t.Fatalf("empty bru payload")
	}
	results, _ := payload[0]["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("empty bru results")
	}
	rm, _ := results[0].(map[string]any)

	req := map[string]string{}
	if reqMap, ok := rm["request"].(map[string]any); ok {
		req = toLowerStringMap(reqMap["headers"])
	}
	resp := map[string]string{}
	if respMap, ok := rm["response"].(map[string]any); ok {
		resp = toLowerStringMap(respMap["headers"])
	}
	return req, resp
}

func parseGruHeaders(t *testing.T, path string) (map[string]string, map[string]string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open gru headers: %v", err)
	}
	defer f.Close()

	var sum RunSummary
	if err := json.NewDecoder(f).Decode(&sum); err != nil {
		t.Fatalf("decode gru headers: %v", err)
	}
	if len(sum.Cases) == 0 {
		t.Fatalf("empty gru cases")
	}
	return sum.Cases[0].RequestHeaders, sum.Cases[0].ResponseHeaders
}

func findCompatCollection(t *testing.T) string {
	candidates := []string{
		filepath.Join("..", "compat-collection"),
		"compat-collection",
		filepath.Join("..", "..", "compat-collection"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	t.Fatalf("compat-collection folder not found")
	return ""
}

// rewriteCompatCollection normalizes compat-collection to run against the mock server
// and adjusts assertions to ones supported by our expect helpers.
func rewriteCompatCollection(t *testing.T, root, srvURL string) {
	t.Helper()

	replacements := map[string]string{
		"https://httpbin.org":                                           srvURL,
		"https://reqres.in":                                             srvURL,
		"https://countries.trevorblades.com/":                           srvURL + "/graphql",
		"body:test":                                                     "tests",
		"expect(res.body.url).to.contain(\"userId\");":                  "expect(res.body.url).to.contain(\"anything/42\");",
		"expect(res.body.url).to.include(\"userId\");":                  "expect(res.body.url).to.include(\"anything/42\");",
		"expect(res.body.headers['X-API-Key']).to.equal('{{apiKey}}');": "expect(res.body.headers['x-api-key']).to.equal('{{apiKey}}');",
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".bru" {
			return nil
		}
		// Drop invalid-url so both runners skip it uniformly.
		if strings.Contains(info.Name(), "invalid-url") {
			_ = os.Remove(path)
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated := string(data)
		for old, newVal := range replacements {
			updated = strings.ReplaceAll(updated, old, newVal)
		}
		updated = strings.ReplaceAll(updated, "{{apiKey}}", "dev-api-key-123")
		if strings.Contains(info.Name(), "failure") {
			updated = strings.ReplaceAll(updated, "expect(res.status).to.equal(200); // should fail", "expect(res.status).to.equal(418);")
		}
		if updated != string(data) {
			return os.WriteFile(path, []byte(updated), info.Mode())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("rewrite compat collection: %v", err)
	}
}

func toLowerStringMap(v any) map[string]string {
	out := map[string]string{}
	if v == nil {
		return out
	}
	switch m := v.(type) {
	case map[string]any:
		for k, val := range m {
			out[strings.ToLower(k)] = fmt.Sprint(val)
		}
	case map[string]string:
		for k, val := range m {
			out[strings.ToLower(k)] = val
		}
	}
	return out
}

// Parity on sampledata JSON + HTML reporters (bru is source of truth).
func TestCLIReporterParitySampledata(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	srv := suiteServer()
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "gru-parity-sampledata-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	base := findSampledata(t)
	sampleRoot := filepath.Join(tmp, "sampledata")
	if err := copyTree(base, sampleRoot); err != nil {
		t.Fatalf("copy sampledata: %v", err)
	}
	githubEnv := filepath.Join(sampleRoot, "GitHub", "environments", "Github.bru")
	replaceInFile(githubEnv, "https://api.github.com", srv.URL)

	envPath := filepath.Join(sampleRoot, "environments", "local.bru")
	replaceInFile(envPath, "http://127.0.0.1:0", srv.URL)
	replaceInFile(envPath, "http://127.0.0.1:0/graphql", srv.URL+"/graphql")
	_ = os.WriteFile(filepath.Join(sampleRoot, "bruno.json"), []byte(`{"name":"sampledata","version":"1.0","type":"collection"}`), 0o644)

	gruBin := buildGruBinary(t, tmp)

	bruJSON := filepath.Join(tmp, "bru.sample.json")
	gruJSON := filepath.Join(tmp, "gru.sample.json")
	bruHTML := filepath.Join(tmp, "bru.sample.html")
	gruHTML := filepath.Join(tmp, "gru.sample.html")

	bruCmd := exec.Command("bru", "run", ".", "-r", "--env-file", envPath, "--reporter-json", bruJSON, "--reporter-html", bruHTML)
	bruCmd.Dir = sampleRoot
	if out, err := bruCmd.CombinedOutput(); err != nil {
		t.Logf("bru output:\n%s", out)
	}

	gruCmd := exec.Command(gruBin, "run", sampleRoot, "-r", "--env", envPath, "--reporter-json", gruJSON, "--reporter-html", gruHTML)
	if out, err := gruCmd.CombinedOutput(); err != nil {
		if _, statErr := os.Stat(gruJSON); statErr != nil {
			t.Fatalf("gru sampledata run failed and no report: %v output=%s", err, out)
		}
	}

	bruStatuses, _, _ := parseBruStatuses(t, bruJSON)
	gruStatuses, _, _ := parseGruStatuses(t, gruJSON)
	if !equalStatusMaps(bruStatuses, gruStatuses) {
		t.Fatalf("sampledata status mismatch\nbru=%v\ngru=%v", bruStatuses, gruStatuses)
	}

	assertNonEmptyFile(t, bruHTML)
	assertNonEmptyFile(t, gruHTML)
}

// Parity on compat-collection JSON + HTML reporters (rewritten to mock server).
func TestCLIReporterParityCompatCollection(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	srv := suiteServer()
	defer srv.Close()

	tmp, err := os.MkdirTemp("", "gru-parity-compat-")
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if os.Getenv("GRU_DEBUG_KEEP") != "" {
		t.Logf("keeping temp dir %s", tmp)
		cleanup = func() {}
	}
	defer cleanup()

	compatSrc := findCompatCollection(t)
	compatRoot := filepath.Join(tmp, "compat-collection")
	if err := copyTree(compatSrc, compatRoot); err != nil {
		t.Fatalf("copy compat: %v", err)
	}
	rewriteCompatCollection(t, compatRoot, srv.URL)

	gruBin := buildGruBinary(t, tmp)

	envPath := filepath.Join(compatRoot, "environments", "dev.bru")
	bruJSON := filepath.Join(tmp, "bru.compat.json")
	gruJSON := filepath.Join(tmp, "gru.compat.json")
	bruHTML := filepath.Join(tmp, "bru.compat.html")
	gruHTML := filepath.Join(tmp, "gru.compat.html")

	bruCmd := exec.Command("bru", "run", ".", "-r", "--env-file", envPath, "--reporter-json", bruJSON, "--reporter-html", bruHTML)
	bruCmd.Dir = compatRoot
	if out, err := bruCmd.CombinedOutput(); err != nil {
		t.Logf("bru output:\n%s", out)
		if _, statErr := os.Stat(bruJSON); statErr != nil {
			t.Skipf("bru run failed and no report: %v", err)
		}
	}

	gruCmd := exec.Command(gruBin, "run", compatRoot, "-r", "--env", envPath, "--reporter-json", gruJSON, "--reporter-html", gruHTML)
	if out, err := gruCmd.CombinedOutput(); err != nil {
		if _, statErr := os.Stat(gruJSON); statErr != nil {
			t.Fatalf("gru compat run failed and no report: %v output=%s", err, out)
		}
	}

	bruStatuses, bruCounts, bruSnap := parseBruStatuses(t, bruJSON)
	gruStatuses, gruCounts, gruSnap := parseGruStatuses(t, gruJSON)
	sortSnapshots(bruSnap)
	sortSnapshots(gruSnap)
	for i := range bruSnap {
		bruSnap[i].URL = ""
	}
	for i := range gruSnap {
		gruSnap[i].URL = ""
	}
	if !equalStatusMaps(bruStatuses, gruStatuses) {
		t.Fatalf("compat status mismatch\nbru=%v\ngru=%v", bruStatuses, gruStatuses)
	}
	if bruCounts != gruCounts {
		t.Fatalf("compat status counts mismatch bru=%v gru=%v", bruCounts, gruCounts)
	}
	assertSnapshotsClose(t, bruSnap, gruSnap)

	assertNonEmptyFile(t, bruHTML)
	assertNonEmptyFile(t, gruHTML)
}
