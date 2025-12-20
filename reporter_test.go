package gruno

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteReportJSON(t *testing.T) {
	tmp := t.TempDir()
	out := tmp + "/report.json"

	sum := RunSummary{
		Cases: []CaseResult{
			{Name: "ok", FilePath: "a.bru", Passed: true, Duration: 1500 * time.Millisecond},
			{Name: "fail", FilePath: "b.bru", Passed: false, Failures: []AssertionFailure{{Message: "boom"}}, Duration: 500 * time.Millisecond},
		},
		Total:        2,
		Passed:       1,
		Failed:       1,
		Skipped:      0,
		TotalElapsed: 2 * time.Second,
	}

	if err := WriteReportJSON(out, sum); err != nil {
		t.Fatalf("write json: %v", err)
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("open report: %v", err)
	}
	defer f.Close()

	var decoded RunSummary
	if err := json.NewDecoder(f).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Failed != 1 || len(decoded.Cases) != 2 {
		t.Fatalf("unexpected decoded summary %+v", decoded)
	}
}

func TestWriteReportJUnit(t *testing.T) {
	tmp := t.TempDir()
	out := tmp + "/report.xml"

	sum := RunSummary{
		Cases: []CaseResult{
			{Name: "ok", FilePath: "a.bru", Passed: true, Duration: 1200 * time.Millisecond},
			{Name: "skipped", FilePath: "b.bru", Passed: true, Skipped: true, Duration: 0},
			{Name: "fail", FilePath: "c.bru", Passed: false, Failures: []AssertionFailure{{Message: "boom"}}, Duration: 800 * time.Millisecond},
		},
		Total:        3,
		Passed:       1,
		Failed:       1,
		Skipped:      1,
		TotalElapsed: 3 * time.Second,
	}

	if err := WriteReportJUnit(out, sum); err != nil {
		t.Fatalf("write junit: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read junit: %v", err)
	}

	var suite junitTestsuite
	if err := xml.Unmarshal(data, &suite); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if suite.Tests != 3 || suite.Failures != 1 || suite.Skipped != 1 {
		t.Fatalf("unexpected suite %+v", suite)
	}
	if len(suite.Cases) != 3 || suite.Cases[2].Failure == nil {
		t.Fatalf("expected failure case recorded")
	}
}

func TestFilterReportHeaders(t *testing.T) {
	sum := RunSummary{
		Cases: []CaseResult{
			{
				Name:            "case",
				RequestHeaders:  map[string]string{"authorization": "Bearer secret", "x-foo": "bar"},
				ResponseHeaders: map[string]string{"content-type": "application/json", "x-foo": "bar"},
			},
		},
	}

	withMask := FilterReportHeaders(sum, RunOptions{})
	if withMask.Cases[0].RequestHeaders["authorization"] != "********" {
		t.Fatalf("authorization not masked: %+v", withMask.Cases[0].RequestHeaders)
	}
	if withMask.Cases[0].RequestHeaders["x-foo"] != "bar" {
		t.Fatalf("unexpected header retained")
	}

	skipOne := FilterReportHeaders(sum, RunOptions{ReporterSkipHeaders: []string{"Authorization"}})
	if _, ok := skipOne.Cases[0].RequestHeaders["authorization"]; ok {
		t.Fatalf("authorization should be skipped")
	}
	if skipOne.Cases[0].RequestHeaders["x-foo"] != "bar" {
		t.Fatalf("x-foo should remain")
	}

	skipAll := FilterReportHeaders(sum, RunOptions{ReporterSkipAllHeaders: true})
	if skipAll.Cases[0].RequestHeaders != nil || skipAll.Cases[0].ResponseHeaders != nil {
		t.Fatalf("headers should be nil when skipping all: %+v %+v", skipAll.Cases[0].RequestHeaders, skipAll.Cases[0].ResponseHeaders)
	}
}

func TestWriteReportHTMLGolden(t *testing.T) {
	tmp := t.TempDir()
	out := filepath.Join(tmp, "report.html")

	sum := RunSummary{
		Cases: []CaseResult{
			{Name: "ok", FilePath: "a.bru", Passed: true, Duration: 1500 * time.Millisecond},
			{Name: "skipped", FilePath: "b.bru", Passed: true, Skipped: true, Duration: 0},
			{Name: "fail", FilePath: "c.bru", Passed: false, ErrorText: "boom", Duration: 900 * time.Millisecond},
		},
		Total:        3,
		Passed:       1,
		Failed:       1,
		Skipped:      1,
		TotalElapsed: 3 * time.Second,
	}

	if err := WriteReportHTML(out, sum); err != nil {
		t.Fatalf("write html: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read html: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "report.html.golden"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("html report mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}
