package importer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"pkt.systems/gruno/internal/runner"
)

// Import real-world WSDLs, point baseUrl to a mock SOAP server, and run generated requests.
func TestImportWSDLAndRunAgainstMock(t *testing.T) {
	fixtures := []struct {
		spec      string
		withTests bool
	}{
		{filepath.Join("..", "..", "sampledata", "wsdl", "paypal_sandbox.wsdl"), true},
		{filepath.Join("..", "..", "sampledata", "wsdl", "ebaySvc_latest.wsdl"), true},
		{filepath.Join("..", "..", "sampledata", "wsdl", "marketingcloud_etframework.wsdl"), true},
		{filepath.Join("..", "..", "sampledata", "wsdl", "schema_sample.wsdl"), true},
		{filepath.Join("..", "..", "sampledata", "wsdl", "attachment_sample.wsdl"), true},
	}

	for _, spec := range fixtures {
		t.Run(filepath.Base(spec.spec), func(t *testing.T) {
			wsdlDef, idx := mustLoadWSDL(t, spec.spec)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bodyBytes, _ := io.ReadAll(r.Body)
				op := extractSoapOperation(bodyBytes)
				if op == "" {
					op = "Operation"
				}
				w.Header().Set("Content-Type", "text/xml")
				w.WriteHeader(http.StatusOK)
				resp := buildSoapResponse(wsdlDef, idx, op)
				_, _ = w.Write([]byte(resp))
			}))
			defer srv.Close()

			tmp := t.TempDir()
			if err := ImportWSDL(context.Background(), Options{Source: spec.spec, OutputDir: tmp, GenerateTests: spec.withTests, GenerateTestsSet: true}); err != nil {
				t.Fatalf("import wsdl: %v", err)
			}

			// Override env with mock baseUrl
			envDir := filepath.Join(tmp, "environments")
			_ = os.MkdirAll(envDir, 0o755)
			envPath := filepath.Join(envDir, "local.bru")
			if err := os.WriteFile(envPath, []byte("vars {\n  baseUrl: "+srv.URL+"\n}\n"), 0o644); err != nil {
				t.Fatalf("write env: %v", err)
			}

			g, err := runner.New(context.Background())
			if err != nil {
				t.Fatalf("runner: %v", err)
			}

			sum, err := g.RunFolder(context.Background(), tmp, runner.RunOptions{EnvPath: envPath})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if sum.Failed != 0 {
				var details []string
				for _, c := range sum.Cases {
					if c.Passed || c.Skipped {
						continue
					}
					msg := "no assertion message"
					if len(c.Failures) > 0 {
						msg = c.Failures[0].Message
					} else if c.ErrorText != "" {
						msg = c.ErrorText
					}
					details = append(details, fmt.Sprintf("%s: %s", c.Name, msg))
				}
				t.Fatalf("expected 0 failures, got %d (%s)", sum.Failed, strings.Join(details, "; "))
			}
		})
	}
}

func TestWSDLBase64AttachmentMustBePresent(t *testing.T) {
	spec := filepath.Join("..", "..", "sampledata", "wsdl", "attachment_sample.wsdl")
	def, idx := mustLoadWSDL(t, spec)

	tests := []struct {
		name     string
		payload  string
		failWant bool
	}{
		{"nonempty base64 passes", "QQ==", false},
		{"empty base64 fails", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := fmt.Sprintf(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><UploadResponse><Attachment>%s</Attachment></UploadResponse></soap:Body></soap:Envelope>`, tt.payload)
				w.Header().Set("Content-Type", "text/xml")
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			tmp := t.TempDir()
			if err := ImportWSDL(context.Background(), Options{Source: spec, OutputDir: tmp, GenerateTests: true}); err != nil {
				t.Fatalf("import wsdl: %v", err)
			}

			envDir := filepath.Join(tmp, "environments")
			_ = os.MkdirAll(envDir, 0o755)
			envPath := filepath.Join(envDir, "local.bru")
			if err := os.WriteFile(envPath, []byte("vars {\n  baseUrl: "+srv.URL+"\n}\n"), 0o644); err != nil {
				t.Fatalf("env: %v", err)
			}

			g, err := runner.New(context.Background())
			if err != nil {
				t.Fatalf("runner: %v", err)
			}
			sum, err := g.RunFolder(context.Background(), tmp, runner.RunOptions{EnvPath: envPath})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if tt.failWant {
				if sum.Failed == 0 {
					t.Fatalf("expected failure but got success")
				}
			} else if sum.Failed > 0 {
				t.Fatalf("expected success, got failures: %+v", sum)
			}
			_ = def
			_ = idx
		})
	}
}

func extractSoapOperation(body []byte) string {
	s := string(body)
	low := strings.ToLower(s)
	bodyIdx := strings.Index(low, "<soapenv:body")
	if bodyIdx == -1 {
		bodyIdx = strings.Index(low, "<soap:body")
	}
	if bodyIdx != -1 {
		if gt := strings.Index(s[bodyIdx:], ">"); gt != -1 {
			s = s[bodyIdx+gt+1:]
		}
	}
	start := strings.Index(s, "<")
	if start == -1 {
		return ""
	}
	end := strings.IndexAny(s[start+1:], " >/")
	if end == -1 {
		return ""
	}
	name := s[start+1 : start+1+end]
	if idx := strings.Index(name, ":"); idx >= 0 {
		name = name[idx+1:]
	}
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, "Response")
	return name
}

func mockSoapResponse(op string) string {
	switch op {
	case "GetStatus":
		return `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetStatusResponse><state>OPEN</state><message>Hello</message><code>5</code><items><item>1</item></items><items><item>2</item></items><payload>QUJDREVGR0g=</payload><details><inner>WORLD</inner></details></GetStatusResponse></soap:Body></soap:Envelope>`
	default:
		return fmt.Sprintf(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><%sResponse><result>ok</result></%sResponse></soap:Body></soap:Envelope>`, op, op)
	}
}

func mustLoadWSDL(t *testing.T, path string) (wsdlDefinitions, map[string]xsdElement) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read wsdl: %v", err)
	}
	var def wsdlDefinitions
	if err := xml.Unmarshal(data, &def); err != nil {
		t.Fatalf("parse wsdl: %v", err)
	}
	return def, buildElementIndex(def)
}

func buildSoapResponse(def wsdlDefinitions, idx map[string]xsdElement, op string) string {
	respEl, ok := idx[op+"Response"]
	body := ""
	if ok {
		body = renderElement(respEl, idx, 0)
	} else {
		body = fmt.Sprintf("<%sResponse><result>ok</result></%sResponse>", op, op)
	}
	return fmt.Sprintf(`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body>%s</soap:Body></soap:Envelope>`, body)
}

func renderElement(el xsdElement, idx map[string]xsdElement, depth int) string {
	if depth > 4 {
		return ""
	}
	name := el.Name
	if name == "" {
		name = localName(el.Type)
	}
	if name == "" {
		name = "Value"
	}

	min := occursToInt(el.MinOccurs, 1)
	var b strings.Builder
	for range min {
		b.WriteString("<" + name + ">")
		b.WriteString(renderElementValue(el, idx, depth+1))
		b.WriteString("</" + name + ">")
	}
	return b.String()
}

func renderElementValue(el xsdElement, idx map[string]xsdElement, depth int) string {
	// Prefer inline complex/simple definitions
	if el.ComplexType != nil {
		return renderComplex(el.ComplexType, idx, depth)
	}
	if el.SimpleType != nil {
		return renderSimple(resolveRestriction(el.SimpleType.Restriction, idx))
	}

	// Named type reference
	if el.Type != "" {
		if ref, ok := idx[localName(el.Type)]; ok {
			if ref.ComplexType != nil {
				return renderComplex(ref.ComplexType, idx, depth)
			}
			if ref.SimpleType != nil {
				return renderSimple(resolveRestriction(ref.SimpleType.Restriction, idx))
			}
		}
		base := localName(el.Type)
		return renderBaseValue(base, xsdRestriction{Base: base})
	}

	// Fallback
	return "value"
}

func renderComplex(ct *xsdComplexType, idx map[string]xsdElement, depth int) string {
	var b strings.Builder
	for _, child := range ct.Sequence.Elements {
		b.WriteString(renderElement(child, idx, depth+1))
	}
	return b.String()
}

func renderSimple(r xsdRestriction) string {
	return renderBaseValue(localName(r.Base), r)
}

func renderBaseValue(base string, r xsdRestriction) string {
	// enums win
	if len(r.Enums) > 0 {
		return r.Enums[0].Value
	}

	// numeric bounds
	if len(r.MinInclusive) > 0 {
		return r.MinInclusive[0].Value
	}
	if len(r.MinExclusive) > 0 {
		// return minExclusive + 1
		if v, err := strconv.Atoi(r.MinExclusive[0].Value); err == nil {
			return strconv.Itoa(v + 1)
		}
	}

	switch base {
	case "base64Binary":
		want := 1
		if len(r.MinLengthVals) > 0 {
			if v, err := strconv.Atoi(r.MinLengthVals[0].Value); err == nil {
				want = v
			}
		}
		buf := bytes.Repeat([]byte{0x41}, want)
		return base64.StdEncoding.EncodeToString(buf)
	case "hexBinary":
		want := 1
		if len(r.MinLengthVals) > 0 {
			if v, err := strconv.Atoi(r.MinLengthVals[0].Value); err == nil {
				want = v
			}
		}
		return strings.Repeat("A", want*2)
	case "boolean":
		return "true"
	case "int", "integer", "decimal", "float", "double", "long", "short", "byte", "unsignedInt", "unsignedShort", "unsignedLong", "unsignedByte", "positiveInteger", "nonNegativeInteger":
		return "1"
	case "date":
		return "2020-01-02"
	case "dateTime":
		return "2020-01-02T03:04:05Z"
	case "time":
		return "03:04:05Z"
	case "anyURI":
		return "http://example.com/value"
	}

	// patterns (best-effort)
	if r.Pattern.Value != "" {
		p := r.Pattern.Value
		switch {
		case strings.Contains(p, `\d`), strings.Contains(p, "[0-9]"):
			return "123"
		case strings.Contains(p, "[A-Z]"):
			return "ABC"
		case strings.Contains(p, "[a-z]"):
			return "abc"
		}
	}

	// lengths
	val := "value"
	if len(r.MinLengthVals) > 0 {
		if n, err := strconv.Atoi(r.MinLengthVals[0].Value); err == nil && n > len(val) {
			val = strings.Repeat("x", n)
		}
	}

	return val
}
