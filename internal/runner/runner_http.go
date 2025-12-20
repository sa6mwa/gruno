package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"pkt.systems/gruno/internal/parser"
)

func buildHTTPRequest(p parsedFile, exp *expander) (*http.Request, error) {
	url := exp.expand(p.Request.URL)
	// substitute path params like :id
	for k, v := range p.Request.PathParams {
		url = strings.ReplaceAll(url, ":"+k, exp.expand(v))
	}
	if parser.VarPattern.MatchString(url) {
		matches := parser.VarPattern.FindAllStringSubmatch(url, -1)
		var names []string
		for _, m := range matches {
			if len(m) > 1 {
				names = append(names, strings.TrimSpace(m[1]))
			}
		}
		return nil, fmt.Errorf("unresolved variable(s) in url: %s (provide --env/--var)", strings.Join(names, ", "))
	}
	if p.Request.Headers == nil {
		p.Request.Headers = map[string]string{}
	}

	var bodyReader io.Reader = http.NoBody
	if p.Request.Body.Present {
		btype := p.Request.Body.Type
		if btype == "" {
			btype = "json"
		}
		switch btype {
		case "json", "graphql":
			expanded := strings.TrimSpace(exp.expand(p.Request.Body.Raw))
			var payload []byte
			if btype == "graphql" {
				obj := map[string]any{"query": expanded}
				if len(p.Request.GraphqlVars) > 0 {
					vars := map[string]any{}
					for k, v := range p.Request.GraphqlVars {
						vars[k] = exp.expand(v)
					}
					obj["variables"] = vars
				}
				if b, err := json.Marshal(obj); err == nil {
					payload = b
				}
			}
			if payload == nil {
				jbytes, err := normalizeJSONBody(expanded)
				if err != nil {
					return nil, err
				}
				payload = jbytes
			}
			bodyReader = bytes.NewBuffer(payload)
			if _, ok := p.Request.Headers["Content-Type"]; !ok {
				p.Request.Headers["Content-Type"] = "application/json"
			}
		case "form-urlencoded":
			vals := urlValuesFromMap(p.Request.Body.Fields, exp)
			bodyReader = strings.NewReader(vals.Encode())
			if p.Request.Headers == nil {
				p.Request.Headers = map[string]string{}
			}
			p.Request.Headers["Content-Type"] = "application/x-www-form-urlencoded"
		case "multipart-form":
			var buf bytes.Buffer
			w := multipart.NewWriter(&buf)
			for k, v := range p.Request.Body.Fields {
				part := parseMultipartValue(v)
				if part.isFile {
					f, err := os.Open(exp.expand(part.value))
					if err != nil {
						return nil, err
					}
					defer f.Close()
					h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, k, filepath.Base(part.value)))
				if part.contentType != "" {
					h.Set("Content-Type", part.contentType)
				}
				if part.contentID != "" {
					h.Set("Content-ID", part.contentID)
					}
					pw, err := w.CreatePart(h)
					if err != nil {
						return nil, err
					}
					if _, err := io.Copy(pw, f); err != nil {
						return nil, err
					}
					continue
				}
				// string part (may carry explicit content-type or content-id)
			if part.contentType != "" || part.contentID != "" {
				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, k))
				if part.contentType != "" {
					h.Set("Content-Type", part.contentType)
				}
					if part.contentID != "" {
						h.Set("Content-ID", part.contentID)
					}
					pw, err := w.CreatePart(h)
					if err != nil {
						return nil, err
					}
					if _, err := pw.Write([]byte(exp.expand(part.value))); err != nil {
						return nil, err
					}
					continue
				}
				_ = w.WriteField(k, exp.expand(part.value))
			}
			_ = w.Close()
			bodyReader = &buf
			if p.Request.Headers == nil {
				p.Request.Headers = map[string]string{}
			}
			if ct, ok := p.Request.Headers["Content-Type"]; ok && strings.Contains(strings.ToLower(ct), "multipart/related") {
				if !strings.Contains(ct, "boundary=") {
					p.Request.Headers["Content-Type"] = ct + "; boundary=" + w.Boundary()
				}
			} else {
				p.Request.Headers["Content-Type"] = w.FormDataContentType()
			}
		case "xml":
			bodyReader = bytes.NewBufferString(exp.expand(p.Request.Body.Raw))
			if _, ok := p.Request.Headers["Content-Type"]; !ok {
				p.Request.Headers["Content-Type"] = "application/xml"
			}
		case "text":
			bodyReader = bytes.NewBufferString(exp.expand(p.Request.Body.Raw))
			if _, ok := p.Request.Headers["Content-Type"]; !ok {
				p.Request.Headers["Content-Type"] = "text/plain"
			}
		default:
			bodyReader = bytes.NewBufferString(exp.expand(p.Request.Body.Raw))
		}
	}
	if p.Request.Headers == nil {
		p.Request.Headers = map[string]string{}
	}
	req, err := http.NewRequest(p.Request.Verb, url, bodyReader)
	if err != nil {
		return nil, err
	}
	for k, v := range p.Request.Headers {
		req.Header.Set(k, exp.expand(v))
	}
	// query params
	if len(p.Request.Query) > 0 {
		q := req.URL.Query()
		for k, v := range p.Request.Query {
			q.Set(k, exp.expand(v))
		}
		req.URL.RawQuery = q.Encode()
	}
	return req, nil
}

func urlValuesFromMap(fields map[string]string, exp *expander) url.Values {
	vals := url.Values{}
	for k, v := range fields {
		vals.Set(k, exp.expand(v))
	}
	return vals
}

type multipartPart struct {
	isFile      bool
	value       string
	contentType string
	contentID   string
}

// parseMultipartValue supports syntaxes:
//   @/path/to/file;type=application/octet-stream;cid=<attach1>
//   raw text;type=application/xop+xml;cid=<rootpart>
func parseMultipartValue(raw string) multipartPart {
	p := multipartPart{value: raw}
	parts := strings.Split(raw, ";")
	if len(parts) == 0 {
		return p
	}
	first := parts[0]
	if strings.HasPrefix(first, "@") {
		p.isFile = true
		p.value = strings.TrimPrefix(first, "@")
	} else {
		p.value = first
	}
	for _, seg := range parts[1:] {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if k, v, ok := strings.Cut(seg, "="); ok {
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "type", "content-type":
				p.contentType = strings.Trim(v, `"`)
			case "cid", "content-id":
				p.contentID = strings.TrimSpace(v)
			}
		}
	}
	return p
}

// Note: We intentionally do not expose more than two return values elsewhere.

// normalizeJSONBody tries to coerce Bruno-style pseudo-JSON into valid JSON by
// evaluating it as a JS object literal and re-encoding.
func normalizeJSONBody(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	// If already valid JSON, use it directly.
	var direct any
	if err := json.Unmarshal([]byte(trimmed), &direct); err == nil {
		return json.Marshal(direct)
	}

	vm := goja.New()
	raw = quoteBareValues(raw)
	script := raw
	if !strings.HasPrefix(strings.TrimSpace(raw), "(") {
		script = "(" + raw + ")"
	}
	if v, err := vm.RunString(script); err == nil {
		exported := v.Export()
		if b, err := json.Marshal(exported); err == nil {
			return b, nil
		}
		// If marshaling fails (e.g. functions, symbols, or other unsupported
		// JS values), fall back to the original raw payload instead of
		// surfacing a marshal error to the caller. We still return nil error so
		// the request can be sent with best-effort body.
		return []byte(trimmed), nil
	}

	// Last resort: return raw bytes (best effort)
	return []byte(trimmed), nil
}

var bareValueRe = regexp.MustCompile(`: ([A-Za-z0-9_.-]+)([\s,\n])`)

func quoteBareValues(raw string) string {
	return bareValueRe.ReplaceAllStringFunc(raw, func(s string) string {
		m := bareValueRe.FindStringSubmatch(s)
		if len(m) != 3 {
			return s
		}
		val := m[1]
		tail := m[2]
		if val == "true" || val == "false" {
			return s
		}
		// numeric?
		if _, err := strconv.ParseFloat(val, 64); err == nil {
			return s
		}
		return ": \"" + val + "\"" + tail
	})
}
