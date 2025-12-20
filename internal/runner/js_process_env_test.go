package runner

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"testing"
)

// Ensure process.env exposes both OS env and loaded vars.
func TestProcessEnvAvailableInJS(t *testing.T) {
	const osKey = "GRU_OS_ENV_TEST"
	const osVal = "from-os"
	os.Setenv(osKey, osVal)
	defer os.Unsetenv(osKey)

	vmExp := newExpander(map[string]string{"FILE_VAR": "from-file"})

	bru := parsedFile{
		FilePath: "env.bru",
		Meta:     metaBlock{Name: "Env Access"},
		Request:  requestBlock{Verb: "GET", URL: "http://example.com"},
		TestsRaw: `
test("env api", function(){
  expect(env("FILE_VAR")).to.equal("from-file");
});
test("process env file", function(){
  expect(process.env.FILE_VAR).to.equal("from-file");
});
test("process env os", function(){
  expect(process.env.` + osKey + `).to.equal("from-os");
});
`,
	}

	resp := &http.Response{StatusCode: 200, Body: ioNopCloser(bytes.NewBufferString("{}")), Header: http.Header{"Content-Type": []string{"application/json"}}}

	res, err := executeTests(context.Background(), bru, resp, 0, vmExp, nil, "", iterationInfo{total: 1, data: map[string]any{}, exp: vmExp})
	if err != nil {
		t.Fatalf("executeTests error: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected tests to pass, failures: %+v", res.Failures)
	}
}
