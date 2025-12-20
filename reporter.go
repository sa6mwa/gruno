package gruno

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"os"
	"strings"
)

// FilterReportHeaders applies reporter skip/redaction options to a summary before writing outputs.
// It removes headers from the summary when requested and masks sensitive values.
func FilterReportHeaders(sum RunSummary, opts RunOptions) RunSummary {
	return filterReportHeaders(sum, opts.ReporterSkipAllHeaders, opts.ReporterSkipHeaders)
}

func filterReportHeaders(sum RunSummary, skipAll bool, skipList []string) RunSummary {
	skipSet := map[string]struct{}{}
	for _, h := range skipList {
		skipSet[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
	}

	out := sum
	out.Cases = make([]CaseResult, len(sum.Cases))
	copy(out.Cases, sum.Cases)

	for i := range out.Cases {
		if skipAll {
			out.Cases[i].RequestHeaders = nil
			out.Cases[i].ResponseHeaders = nil
			continue
		}
		out.Cases[i].RequestHeaders = filterHeaderMap(out.Cases[i].RequestHeaders, skipSet)
		out.Cases[i].ResponseHeaders = filterHeaderMap(out.Cases[i].ResponseHeaders, skipSet)
		maskSensitiveHeaders(out.Cases[i].RequestHeaders)
		maskSensitiveHeaders(out.Cases[i].ResponseHeaders)
	}

	return out
}

func filterHeaderMap(hdrs map[string]string, skipSet map[string]struct{}) map[string]string {
	if hdrs == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range hdrs {
		if _, skip := skipSet[strings.ToLower(k)]; skip {
			continue
		}
		out[k] = v
	}
	return out
}

func maskSensitiveHeaders(hdrs map[string]string) {
	if hdrs == nil {
		return
	}
	if _, ok := hdrs["authorization"]; ok {
		hdrs["authorization"] = "********"
	}
	if _, ok := hdrs["proxy-authorization"]; ok {
		hdrs["proxy-authorization"] = "********"
	}
}

// WriteReportJSON writes a RunSummary to a JSON file.
func WriteReportJSON(path string, sum RunSummary) error {
	data, err := json.MarshalIndent(sum, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Minimal JUnit reporter for CI compatibility.
type junitTestsuite struct {
	XMLName  xml.Name        `xml:"testsuite"`
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Skipped  int             `xml:"skipped,attr"`
	Time     string          `xml:"time,attr"`
	Cases    []junitTestcase `xml:"testcase"`
}

type junitTestcase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr,omitempty"`
}

// WriteReportJUnit writes a RunSummary to JUnit XML for CI consumers.
func WriteReportJUnit(path string, sum RunSummary) error {
	ts := junitTestsuite{
		Name:     "gru",
		Tests:    len(sum.Cases),
		Failures: sum.Failed,
		Skipped:  sum.Skipped,
		Time:     fmt.Sprintf("%.3f", sum.TotalElapsed.Seconds()),
	}
	for _, c := range sum.Cases {
		tc := junitTestcase{
			Name:      c.Name,
			Classname: c.FilePath,
			Time:      fmt.Sprintf("%.3f", c.Duration.Seconds()),
		}
		if c.Skipped {
			tc.Skipped = &junitSkipped{}
		} else if !c.Passed {
			msg := c.ErrorText
			if len(c.Failures) > 0 && c.Failures[0].Message != "" {
				msg = c.Failures[0].Message
			}
			tc.Failure = &junitFailure{
				Message: msg,
				Type:    "assertion",
				Body:    msg,
			}
		}
		ts.Cases = append(ts.Cases, tc)
	}
	data, err := xml.MarshalIndent(ts, "", "  ")
	if err != nil {
		return err
	}
	data = append([]byte(xml.Header), data...)
	return os.WriteFile(path, data, 0o644)
}

// HTML template structured similarly to Bru's report (table-based, status classes)
var htmlTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <title>gru report</title>
  <style>
    body { font-family: Arial, sans-serif; margin: 16px; background: #fafafa; }
    h1 { margin-bottom: 8px; }
    .summary { margin-bottom: 16px; }
    table { width: 100%; border-collapse: collapse; background: #fff; }
    th, td { padding: 8px 10px; border: 1px solid #e0e0e0; font-size: 14px; }
    th { background: #f5f5f5; text-align: left; }
    .status-pass { color: #2e7d32; font-weight: 600; }
    .status-fail { color: #c62828; font-weight: 600; }
    .status-skip { color: #9e9e9e; font-weight: 600; }
    .mono { font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; font-size: 12px; }
  </style>
</head>
<body>
  <h1>gru report</h1>
  <div class="summary">
    <div>Total: {{.Total}} &nbsp; Passed: {{.Passed}} &nbsp; Failed: {{.Failed}} &nbsp; Skipped: {{.Skipped}} &nbsp; Time: {{.TotalElapsed}}</div>
  </div>
  <table>
    <thead>
      <tr>
        <th>#</th>
        <th>Name</th>
        <th>File</th>
        <th>Status</th>
        <th>Duration</th>
        <th>Error</th>
      </tr>
    </thead>
    <tbody>
      {{range $idx, $c := .Cases}}
      <tr>
        <td>{{$idx}}</td>
        <td>{{$c.Name}}</td>
        <td class="mono">{{$c.FilePath}}</td>
        <td>
          {{if $c.Skipped}}<span class="status-skip">skipped</span>{{else if $c.Passed}}<span class="status-pass">passed</span>{{else}}<span class="status-fail">failed</span>{{end}}
        </td>
        <td>{{$c.Duration}}</td>
        <td>{{if $c.ErrorText}}<span class="mono">{{$c.ErrorText}}</span>{{end}}</td>
      </tr>
      {{end}}
    </tbody>
  </table>
</body>
</html>`))

// WriteReportHTML renders a simple HTML table summary.
func WriteReportHTML(path string, sum RunSummary) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return htmlTemplate.Execute(f, sum)
}

// WriteReport picks the reporter function by format.
func WriteReport(format, path string, sum RunSummary) error {
	switch strings.ToLower(format) {
	case "json", "":
		return WriteReportJSON(path, sum)
	case "junit":
		return WriteReportJUnit(path, sum)
	case "html":
		return WriteReportHTML(path, sum)
	default:
		return fmt.Errorf("unknown format %s", format)
	}
}
