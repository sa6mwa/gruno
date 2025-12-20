package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/dop251/goja"
	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oasdiff/yaml"
	"pkt.systems/pslog"
)

// StrictnessLevel controls how deep/strict generated schema assertions are.
type StrictnessLevel int

const (
	// StrictnessLoose disables deep assertion generation.
	StrictnessLoose StrictnessLevel = iota
	// StrictnessStandard keeps baseline assertion generation (default).
	StrictnessStandard
	// StrictnessStrict enables deep nested assertions and numeric strictness.
	StrictnessStrict
)

func parseStrictness(level string) StrictnessLevel {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "loose":
		return StrictnessLoose
	case "strict":
		return StrictnessStrict
	default:
		return StrictnessStandard
	}
}

// ImportOpenAPI generates a Bruno collection from an OpenAPI/Swagger spec.
func ImportOpenAPI(ctx context.Context, opts Options) error {
	var (
		doc      *openapi3.T
		err      error
		data     []byte
		location *url.URL
	)

	if isURL(opts.Source) {
		client := http.DefaultClient
		if opts.Insecure {
			client = insecureHTTPClient()
		}
		data, err = fetchWithClient(opts.Source, client)
		location = mustParse(opts.Source)
	} else {
		if !filepath.IsAbs(opts.Source) {
			if abs, errAbs := filepath.Abs(opts.Source); errAbs == nil {
				opts.Source = abs
			}
		}
		data, err = os.ReadFile(opts.Source)
		location = &url.URL{Path: filepath.ToSlash(opts.Source)}
	}
	if err != nil {
		return fmt.Errorf("load openapi source: %w", err)
	}
	opts.BaseLocation = location

	// Preprocess to hydrate exampleValue into standard example fields.
	data = normalizeExampleValues(data)

	if isSwagger2Data(data) {
		doc, err = loadSwaggerAsV3(ctx, data, location, opts)
	} else {
		doc, err = loadOpenAPIv3(ctx, data, location, opts)
	}
	if err != nil {
		return fmt.Errorf("load openapi: %w", err)
	}

	if !opts.GenerateTestsSet {
		opts.GenerateTests = true
	}

	log := opts.Logger
	if log == nil {
		log = pslog.NewWithOptions(os.Stdout, pslog.Options{Mode: pslog.ModeConsole, MinLevel: pslog.InfoLevel})
	}
	log = log.With("fn", pslog.CurrentFn())

	if verr := doc.Validate(ctx); verr != nil {
		log.Warn("import.openapi.validate.warn", "err", verr)
	}
	pathCount := len(doc.Paths.Map())
	log.Debug("import.openapi.loaded", "servers", len(doc.Servers), "paths", pathCount)

	if opts.OutputDir != "" {
		if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
			return err
		}
	}

	log.Info("import.openapi.start", "source", opts.Source, "output", opts.OutputDir)

	baseURL := ""
	if len(doc.Servers) > 0 {
		baseURL = doc.Servers[0].URL
	}
	if baseURL == "" {
		baseURL = "https://api.example.com"
	}

	collectionName := opts.CollectionName
	if collectionName == "" {
		if doc.Info != nil && doc.Info.Title != "" {
			collectionName = doc.Info.Title
		} else {
			collectionName = "imported-openapi"
		}
	}

	// write bruno.json when output dir is provided
	if opts.OutputDir != "" {
		if err := os.WriteFile(filepath.Join(opts.OutputDir, "bruno.json"), fmt.Appendf(nil, `{"name":%q,"version":"1.0","type":"collection"}`, collectionName), 0o644); err != nil {
			return err
		}
	}

	writeFile := func(relPath, content string) error {
		if opts.OutputDir == "" {
			return nil
		}
		full := filepath.Join(opts.OutputDir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		return os.WriteFile(full, []byte(content), 0o644)
	}

	envVars := map[string]string{
		"baseUrl": baseURL,
	}

	existing := map[string]int{}
	sourceDir := filepath.Dir(opts.Source)
	for route, item := range doc.Paths.Map() {
		if !shouldIncludePath(route, opts.IncludePaths) {
			continue
		}
		brunoRoute := toBrunoRoute(route)
		ops := map[string]*openapi3.Operation{
			"get":     item.Get,
			"post":    item.Post,
			"put":     item.Put,
			"patch":   item.Patch,
			"delete":  item.Delete,
			"options": item.Options,
			"head":    item.Head,
			"trace":   item.Trace,
		}
		for verb, op := range ops {
			if op == nil {
				continue
			}
			name := op.OperationID
			if name == "" {
				name = op.Summary
			}
			if name == "" {
				name = strings.TrimSpace(titleCase(verb) + " " + route)
			}
			tags := op.Tags
			if len(tags) == 0 && item != nil && len(item.Servers) > 0 {
				tags = []string{path.Dir(route)}
			}
			relDir := ""
			switch opts.GroupBy {
			case "path":
				relDir = strings.Trim(path.Dir(route), "/")
			default: // tags
				if len(tags) > 0 {
					relDir = tags[0]
				}
			}
			if relDir == "." || relDir == "/" {
				relDir = ""
			}
			base := name
			if base == "" {
				base = fmt.Sprintf("%s %s", strings.ToUpper(verb), strings.ReplaceAll(route, "/", "_"))
			}
			filename := uniqueFileName(existing, relDir, base)
			if relDir != "" {
				filename = filepath.Join(relDir, filename)
			}

			bodyBlock := ""
			headersBlock := ""
			queryBlock := ""
			testsBlock := ""
			// Apply security/auth headers if defined.
			secReq := firstSecurity(op, doc)
			hdrs, queries, envAdd := authHeaders(secReq, doc)
			if len(hdrs) > 0 {
				var lines []string
				for k, v := range hdrs {
					lines = append(lines, fmt.Sprintf("  %s: %s", k, v))
				}
				sort.Strings(lines)
				headersBlock = "headers {\n" + strings.Join(lines, "\n") + "\n}\n\n"
				log.Debug("import.openapi.auth.headers", "op", name, "path", route, "count", len(lines))
			}
			if len(queries) > 0 {
				var lines []string
				for k, v := range queries {
					lines = append(lines, fmt.Sprintf("  %s: %s", k, v))
				}
				sort.Strings(lines)
				queryBlock = "query {\n" + strings.Join(lines, "\n") + "\n}\n\n"
				log.Debug("import.openapi.auth.query", "op", name, "path", route, "count", len(lines))
			}
			maps.Copy(envVars, envAdd)
			if op.RequestBody != nil {
				content := op.RequestBody.Value.Content
				switch {
				case content.Get("application/json") != nil:
					bodyBlock = bodyBlockFromExample(content.Get("application/json"), "application/json", sourceDir, opts)
					if bodyBlock == "" {
						bodyBlock = "body:json {\n  {}\n}\n"
						log.Debug("import.openapi.request.example.missing", "op", name, "path", route, "ct", "application/json")
					} else {
						log.Debug("import.openapi.request.example.injected", "op", name, "path", route, "ct", "application/json")
					}
				case content.Get("application/xml") != nil:
					bodyBlock = bodyBlockFromExample(content.Get("application/xml"), "application/xml", sourceDir, opts)
					if bodyBlock == "" {
						bodyBlock = "body:xml {\n  \n}\n"
						log.Debug("import.openapi.request.example.missing", "op", name, "path", route, "ct", "application/xml")
					} else {
						log.Debug("import.openapi.request.example.injected", "op", name, "path", route, "ct", "application/xml")
					}
				case content.Get("text/xml") != nil:
					bodyBlock = bodyBlockFromExample(content.Get("text/xml"), "text/xml", sourceDir, opts)
					if bodyBlock == "" {
						bodyBlock = "body:xml {\n  \n}\n"
						log.Debug("import.openapi.request.example.missing", "op", name, "path", route, "ct", "text/xml")
					} else {
						log.Debug("import.openapi.request.example.injected", "op", name, "path", route, "ct", "text/xml")
					}
				case content.Get("application/x-www-form-urlencoded") != nil:
					bodyBlock = "body:form-urlencoded {\n}\n"
				default:
					// pick first available media with an example
					for mt, media := range content {
						bodyBlock = bodyBlockFromExample(media, mt, sourceDir, opts)
						if bodyBlock != "" {
							log.Debug("import.openapi.request.example.injected", "op", name, "path", route, "ct", mt)
						}
						break
					}
				}
			}

			if opts.GenerateTests {
				contentTypes := []string{}
				schemaFound := false
				for _, rr := range op.Responses.Map() {
					if rr != nil && rr.Value != nil {
						for mt := range rr.Value.Content {
							contentTypes = append(contentTypes, mt)
						}
						if mt := rr.Value.Content.Get("application/json"); mt != nil && mt.Schema != nil && mt.Schema.Value != nil {
							schemaFound = true
						}
					}
				}
				log.Debug("import.openapi.tests.inspect", "op", name, "path", route, "responses", len(op.Responses.Map()), "contentTypes", strings.Join(contentTypes, ","), "schemaFound", schemaFound)
				tb := buildSchemaTests(op, doc, parseStrictness(opts.Strictness), log)
				log.Debug("import.openapi.tests.generated.raw", "op", name, "len", len(tb))
				if tb != "" {
					testsBlock = tb
					log.Debug("import.openapi.tests.generated", "op", name, "path", route)
				}
				if testsBlock == "" {
					testsBlock = `tests {
  test("status ok", function() {
    expect(res.status).to.be.within(200, 299);
  });
}
`
					log.Debug("import.openapi.tests.default", "op", name, "path", route)
				} else {
					log.Debug("import.openapi.tests.schema", "op", name, "path", route)
				}
			} else {
				testsBlock = `tests {
  test("status ok", function() {
    expect(res.status).to.be.within(200, 299);
  });
}
`
			}

			bru := fmt.Sprintf(`meta {
  name: %s
  type: http
}

%s {
  url: {{baseUrl}}%s
}

%s%s%s
%s`, name, strings.ToLower(verb), brunoRoute, headersBlock, queryBlock, bodyBlock, testsBlock)

			if err := writeFile(filename, bru); err != nil {
				return err
			}
			log.Info("import.openapi.op.write", "op", name, "path", route, "verb", strings.ToUpper(verb), "file", filename)
		}
	}

	// Optionally emit summary JSON listing files
	if opts.OutputFile != "" {
		summary := map[string]any{
			"name":    collectionName,
			"source":  opts.Source,
			"output":  opts.OutputDir,
			"format":  "bruno",
			"groupBy": opts.GroupBy,
		}
		if err := writeJSONFile(opts.OutputFile, summary); err != nil {
			return err
		}
	}
	// Write environments/local.bru including auth placeholders.
	if opts.OutputDir != "" {
		envDir := filepath.Join(opts.OutputDir, "environments")
		_ = os.MkdirAll(envDir, 0o755)
		envPath := filepath.Join(envDir, "local.bru")
		keys := make([]string, 0, len(envVars))
		for k := range envVars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var lines []string
		for _, k := range keys {
			lines = append(lines, fmt.Sprintf("  %s: %s", k, envVars[k]))
		}
		env := "vars {\n" + strings.Join(lines, "\n") + "\n}\n"
		if err := os.WriteFile(envPath, []byte(env), 0o644); err != nil {
			return err
		}
		log.Debug("import.openapi.env.write", "path", envPath, "vars", len(envVars))
	}
	log.Info("import.openapi.done", "output", opts.OutputDir, "paths", pathCount)
	return nil
}

func sanitizeFileName(s string) string {
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, ":", "_")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, "?", "_")
	s = strings.ReplaceAll(s, "*", "_")
	const maxLen = 120
	if len(s) > maxLen {
		ext := filepath.Ext(s)
		base := strings.TrimSuffix(s, ext)
		over := len(base) - (maxLen - len(ext))
		if over > 0 && len(base) > over {
			base = base[:len(base)-over]
		}
		s = base + ext
	}
	return s
}

func uniqueFileName(counts map[string]int, dir, base string) string {
	key := dir + "|" + base
	count := counts[key]
	name := sanitizeFileName(base + ".bru")
	if count > 0 {
		name = sanitizeFileName(fmt.Sprintf("%s_%d.bru", base, count+1))
	}
	counts[key] = count + 1
	return name
}

func normalizeExampleValues(data []byte) []byte {
	var obj any
	if err := yaml.Unmarshal(data, &obj); err != nil {
		return data
	}
	obj = fixExampleValue(obj, "")
	out, err := json.Marshal(obj)
	if err != nil {
		return data
	}
	return out
}

func fixExampleValue(node any, parentKey string) any {
	switch v := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			if k == "exampleValue" {
				fixed := fixExampleValue(val, k)
				if _, ok := out["example"]; !ok {
					out["example"] = fixed
				}
				if parentKey == "examples" {
					if _, ok := out["value"]; !ok {
						out["value"] = fixed
					}
				}
				continue
			}
			if k == "x-example" {
				fixed := fixExampleValue(val, k)
				if _, ok := out["example"]; !ok {
					out["example"] = fixed
				}
				continue
			}
			out[k] = fixExampleValue(val, k)
		}
		return out
	case []any:
		for i := range v {
			v[i] = fixExampleValue(v[i], parentKey)
		}
		return v
	default:
		return v
	}
}

func firstSecurity(op *openapi3.Operation, doc *openapi3.T) openapi3.SecurityRequirement {
	if op != nil && op.Security != nil && len(*op.Security) > 0 {
		return (*op.Security)[0]
	}
	if doc != nil && doc.Security != nil && len(doc.Security) > 0 {
		return doc.Security[0]
	}
	return nil
}

// authHeaders derives headers/query vars plus env placeholders from an OpenAPI security requirement.
func authHeaders(sec openapi3.SecurityRequirement, doc *openapi3.T) (map[string]string, map[string]string, map[string]string) {
	headers := map[string]string{}
	queries := map[string]string{}
	env := map[string]string{}
	if sec == nil || doc == nil || doc.Components.SecuritySchemes == nil {
		return headers, queries, env
	}
	for name := range sec {
		sref := doc.Components.SecuritySchemes[name]
		if sref == nil || sref.Value == nil {
			continue
		}
		s := sref.Value
		switch strings.ToLower(s.Type) {
		case "apikey":
			varName := toVarName(name)
			switch strings.ToLower(s.In) {
			case "header":
				headers[s.Name] = fmt.Sprintf("{{%s}}", varName)
				env[varName] = "CHANGEME"
			case "query":
				queries[s.Name] = fmt.Sprintf("{{%s}}", varName)
				env[varName] = "CHANGEME"
			case "cookie":
				headers["Cookie"] = fmt.Sprintf("%s={{%s}}", s.Name, varName)
				env[varName] = "CHANGEME"
			}
		case "http":
			scheme := strings.ToLower(s.Scheme)
			switch scheme {
			case "bearer":
				headers["Authorization"] = "Bearer {{bearerToken}}"
				env["bearerToken"] = "CHANGEME"
			case "basic":
				headers["Authorization"] = "Basic {{basicAuth}}"
				env["basicAuth"] = "CHANGEME"
			}
		case "oauth2":
			headers["Authorization"] = "Bearer {{accessToken}}"
			env["accessToken"] = "CHANGEME"
		}
	}
	return headers, queries, env
}

func toVarName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "auth"
	}
	// replace non-alnum with underscores then camel-ish lower
	re := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	name = re.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		return "auth"
	}
	parts := strings.Split(name, "_")
	for i := range parts {
		if parts[i] == "" {
			continue
		}
		if i == 0 {
			parts[i] = strings.ToLower(parts[i])
		} else {
			parts[i] = titleCase(strings.ToLower(parts[i]))
		}
	}
	return strings.Join(parts, "")
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	rs := []rune(strings.ToLower(s))
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isSafeLocalRef enforces allow-file-refs and same-tree rules for local file refs.
func isSafeLocalRef(refPath string, baseDir string, allowFileRefs bool) bool {
	if allowFileRefs {
		return true
	}
	if refPath == "" {
		return false
	}
	refAbs, err := filepath.Abs(refPath)
	if err != nil {
		return false
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return false
	}
	if !strings.HasPrefix(refAbs, baseAbs) {
		return false
	}
	return true
}

func shouldIncludePath(route string, includes []string) bool {
	if len(includes) == 0 {
		return true
	}
	for _, p := range includes {
		if p == route || strings.HasPrefix(route, p) {
			return true
		}
	}
	return false
}

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

func toBrunoRoute(route string) string {
	return pathParamRe.ReplaceAllString(route, ":$1")
}

func isSwagger2Data(data []byte) bool {
	lower := bytes.ToLower(data)
	return bytes.Contains(lower, []byte("swagger")) && bytes.Contains(lower, []byte("2.0"))
}

func loadOpenAPIv3(ctx context.Context, data []byte, location *url.URL, opts Options) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	loader.Context = ctx

	if opts.Insecure || opts.AllowRemoteRefs {
		client := insecureHTTPClient()
		loader.ReadFromURIFunc = func(_ *openapi3.Loader, u *url.URL) ([]byte, error) {
			return fetchExternal(u, client, opts)
		}
	}
	if loader.ReadFromURIFunc == nil {
		loader.ReadFromURIFunc = func(_ *openapi3.Loader, u *url.URL) ([]byte, error) {
			return fetchExternal(u, http.DefaultClient, opts)
		}
	}

	if location != nil {
		return loader.LoadFromDataWithPath(data, location)
	}
	return loader.LoadFromData(data)
}

func loadSwaggerAsV3(ctx context.Context, data []byte, location *url.URL, opts Options) (*openapi3.T, error) {
	var doc2 openapi2.T
	if err := json.Unmarshal(data, &doc2); err != nil {
		if err2 := yaml.Unmarshal(data, &doc2); err2 != nil {
			return nil, fmt.Errorf("unmarshal swagger: %v / %v", err, err2)
		}
	}
	if doc2.Swagger == "" {
		return nil, fmt.Errorf("invalid swagger: missing swagger field")
	}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	loader.Context = ctx
	if opts.Insecure || opts.AllowRemoteRefs {
		client := insecureHTTPClient()
		loader.ReadFromURIFunc = func(_ *openapi3.Loader, u *url.URL) ([]byte, error) {
			return fetchExternal(u, client, opts)
		}
	}
	if loader.ReadFromURIFunc == nil {
		loader.ReadFromURIFunc = func(_ *openapi3.Loader, u *url.URL) ([]byte, error) {
			return fetchExternal(u, http.DefaultClient, opts)
		}
	}
	return openapi2conv.ToV3WithLoader(&doc2, loader, location)
}

func fetchExternal(u *url.URL, client *http.Client, opts Options) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}

	// file / local paths
	if u.Scheme == "" || u.Scheme == "file" {
		if !allowLocalRef(u, opts) {
			return nil, fmt.Errorf("file ref blocked: %s (use --allow-file-refs)", u.String())
		}
		return os.ReadFile(u.Path)
	}

	// http/https
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported external ref scheme: %s", u.Scheme)
	}
	if !opts.AllowRemoteRefs {
		if !sameOrigin(u, opts.Source) {
			return nil, fmt.Errorf("remote external ref blocked: %s (use --allow-remote-refs)", u.String())
		}
	}
	return fetchWithClient(u.String(), client)
}

func sameOrigin(ref *url.URL, source string) bool {
	srcURL, err := url.Parse(source)
	if err != nil || srcURL.Scheme == "" {
		return false
	}
	return srcURL.Scheme == ref.Scheme && srcURL.Host == ref.Host
}

func allowLocalRef(u *url.URL, opts Options) bool {
	if opts.AllowFileRefs {
		return true
	}
	// Only allow if root source is a local file and ref is within the same directory tree.
	srcURL, err := url.Parse(opts.Source)
	if err != nil || srcURL.Scheme == "http" || srcURL.Scheme == "https" {
		return false
	}
	baseDir := filepath.Clean(filepath.Dir(opts.Source))
	refPath := u.Path
	if !filepath.IsAbs(refPath) {
		refPath = filepath.Join(baseDir, refPath)
	}
	refPath = filepath.Clean(refPath)

	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return false
	}
	refAbs, err := filepath.Abs(refPath)
	if err != nil {
		return false
	}

	if !strings.HasPrefix(refAbs, baseAbs+string(filepath.Separator)) && refAbs != baseAbs {
		return false
	}
	rel, err := filepath.Rel(baseAbs, refAbs)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

// buildSchemaTests generates JS assertions from the first 2xx response schema.
func buildSchemaTests(op *openapi3.Operation, doc *openapi3.T, level StrictnessLevel, log pslog.Logger) string {
	if op == nil {
		return ""
	}

	var respRef *openapi3.ResponseRef
	if op.Responses != nil {
		for code, rr := range op.Responses.Map() {
			if code == "200" || strings.HasPrefix(code, "2") {
				respRef = rr
				break
			}
		}
	}
	if respRef == nil || respRef.Value == nil || respRef.Value.Content == nil {
		return ""
	}
	media := respRef.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		return ""
	}

	schema := media.Schema.Value
	// depth controls recursion for nested type checks; strict goes deeper.
	depth := 1
	if level == StrictnessStrict {
		depth = 2
	} else if level == StrictnessLoose {
		depth = 0
	}
	asserts := []string{"expect(res.status).to.equal(200);"}

	// Top-level array response
	if isType(schema, "array") {
		asserts = append(asserts, "expect(Array.isArray(res.body)).to.equal(true);")
		appendArrayChecks(&asserts, "res.body", schema, level, depth)
	}

	// Discriminator presence on top-level
	if schema.Discriminator != nil && schema.Discriminator.PropertyName != "" {
		p := schema.Discriminator.PropertyName
		asserts = append(asserts, fmt.Sprintf("expect(res.body).to.have.property('%s');", p))
		if len(schema.Discriminator.Mapping) > 0 {
			vals := make([]string, 0, len(schema.Discriminator.Mapping))
			for v := range schema.Discriminator.Mapping {
				vals = append(vals, v)
			}
			asserts = append(asserts, fmt.Sprintf("expect(%s).to.include(res.body['%s']);", toJSArrayString(vals), p))
		}
	}

	// oneOf / anyOf basic type satisfaction (best-effort)
	branchConds := variantTypeConds(schema.OneOf, "res.body", level, depth)
	branchConds = append(branchConds, variantTypeConds(schema.AnyOf, "res.body", level, depth)...)
	if len(branchConds) > 0 {
		asserts = append(asserts, fmt.Sprintf("expect([%s].some(Boolean)).to.equal(true);", strings.Join(branchConds, ", ")))
	}
	if sw := discriminatorSwitch(schema, "res.body", doc, level, depth); sw != "" {
		asserts = append(asserts, sw)
	}

	// Object response with properties
	if isType(schema, "object") || len(schema.Properties) > 0 {
		if schema.MinProps > 0 {
			asserts = append(asserts, fmt.Sprintf("expect(Object.keys(res.body).length).to.be.at.least(%d);", schema.MinProps))
		}
		if schema.MaxProps != nil && *schema.MaxProps > 0 {
			asserts = append(asserts, fmt.Sprintf("expect(Object.keys(res.body).length).to.be.at.most(%d);", *schema.MaxProps))
		}
		for _, req := range schema.Required {
			asserts = append(asserts, fmt.Sprintf("expect(res.body).to.have.property('%s');", req))
		}
		for name, prop := range schema.Properties {
			if prop.Value == nil {
				continue
			}
			valExpr := fmt.Sprintf("res.body['%s']", name)
			// If required and not nullable, assert presence of a concrete value (not null/undefined).
			if contains(schema.Required, name) && !prop.Value.Nullable {
				asserts = append(asserts, fmt.Sprintf("expect(%s).to.not.equal(undefined);", valExpr))
				asserts = append(asserts, fmt.Sprintf("expect(%s).to.not.equal(null);", valExpr))
			}
			checks := propertyChecks(prop.Value, valExpr, level, depth)
			if len(checks) > 0 {
				if contains(schema.Required, name) && !prop.Value.Nullable {
					// Already asserted non-null; no guard needed.
					asserts = append(asserts, strings.Join(checks, "\n    "))
				} else {
					block := fmt.Sprintf("if (%s !== undefined && %s !== null) {\n      %s\n    }", valExpr, valExpr, strings.Join(checks, "\n      "))
					asserts = append(asserts, block)
				}
			}
		}
	}

	if len(asserts) == 0 {
		return ""
	}
	body := strings.Join(asserts, "\n    ")
	code := fmt.Sprintf(`tests {
  test("schema", function() {
    %s
  });
}
`, body)
	if !validJS(code, log) {
		if log != nil {
			log.Error("import.openapi.tests.invalid-js", "len", len(code))
			log.Debug("import.openapi.tests.invalid-js.code", "code", code)
		}
		// Emit the tests even if the validator complains; runtime will surface any issues.
		return code
	}
	return code
}

func propertyChecks(s *openapi3.Schema, valExpr string, level StrictnessLevel, depth int) []string {
	checks := []string{}

	tp := firstType(s)
	switch tp {
	case "array":
		checks = append(checks, fmt.Sprintf("expect(Array.isArray(%s)).to.equal(true);", valExpr))
		appendArrayChecks(&checks, valExpr, s, level, depth)
	case "object":
		if level >= StrictnessStrict {
			checks = append(checks, fmt.Sprintf("expect(typeof %s).to.equal('object');", valExpr))
			checks = append(checks, fmt.Sprintf("expect(Array.isArray(%s)).to.equal(false);", valExpr))
		}
		if s.MinProps > 0 {
			checks = append(checks, fmt.Sprintf("expect(Object.keys(%s).length).to.be.at.least(%d);", valExpr, s.MinProps))
		}
		if s.MaxProps != nil && *s.MaxProps > 0 {
			checks = append(checks, fmt.Sprintf("expect(Object.keys(%s).length).to.be.at.most(%d);", valExpr, *s.MaxProps))
		}
		if depth > 0 && level >= StrictnessStrict {
			for name, prop := range s.Properties {
				if prop == nil || prop.Value == nil {
					continue
				}
				subExpr := fmt.Sprintf("%s['%s']", valExpr, name)
				subChecks := propertyChecks(prop.Value, subExpr, level, depth-1)
				if contains(s.Required, name) && !prop.Value.Nullable {
					checks = append(checks, fmt.Sprintf("expect(%s).to.not.equal(undefined);", subExpr))
					checks = append(checks, fmt.Sprintf("expect(%s).to.not.equal(null);", subExpr))
					checks = append(checks, subChecks...)
				} else if len(subChecks) > 0 {
					checks = append(checks, fmt.Sprintf("if (%s !== undefined && %s !== null) { %s }", subExpr, subExpr, strings.Join(subChecks, " ")))
				}
			}
		}
	default:
		if tp != "" {
			checks = append(checks, fmt.Sprintf("expect(typeof %s).to.equal('%s');", valExpr, jsTypeFor(tp)))
			if level >= StrictnessStrict && (tp == "number" || tp == "integer") {
				checks = append(checks, fmt.Sprintf("expect(Number.isFinite(%s)).to.equal(true);", valExpr))
				if tp == "integer" {
					checks = append(checks, fmt.Sprintf("expect(Number.isInteger(%s)).to.equal(true);", valExpr))
				}
			}
		}
	}

	if s.MinLength > 0 {
		checks = append(checks, fmt.Sprintf("expect(%s.length).to.be.at.least(%d);", valExpr, s.MinLength))
	}
	if s.MaxLength != nil && *s.MaxLength > 0 {
		checks = append(checks, fmt.Sprintf("expect(%s.length).to.be.at.most(%d);", valExpr, *s.MaxLength))
	}
	if pcheck := patternCheck(s.Pattern, valExpr); pcheck != "" {
		checks = append(checks, pcheck)
	}
	if fmtCheck := formatCheck(s.Format, valExpr); fmtCheck != "" {
		checks = append(checks, fmtCheck)
	}
	if len(s.Enum) > 0 {
		checks = append(checks, fmt.Sprintf("expect(%s).to.include(%s);", toJSArray(s.Enum), valExpr))
	}
	if s.Min != nil {
		if s.ExclusiveMin {
			checks = append(checks, fmt.Sprintf("expect(%s).to.be.above(%v);", valExpr, *s.Min))
		} else {
			checks = append(checks, fmt.Sprintf("expect(%s).to.be.at.least(%v);", valExpr, *s.Min))
		}
	}
	if s.Max != nil {
		if s.ExclusiveMax {
			checks = append(checks, fmt.Sprintf("expect(%s).to.be.below(%v);", valExpr, *s.Max))
		} else {
			checks = append(checks, fmt.Sprintf("expect(%s).to.be.at.most(%v);", valExpr, *s.Max))
		}
	}

	return checks
}

func bodyBlockFromExample(media *openapi3.MediaType, mediaType string, sourceDir string, opts Options) string {
	if media == nil {
		return ""
	}
	bodyKind := bodyKindFromMediaType(mediaType)

	// Prefer explicit examples
	if len(media.Examples) > 0 {
		for _, ex := range media.Examples {
			if ex == nil {
				continue
			}
			if ex.Value != nil && ex.Value.Value != nil {
				if body := examplePayload(ex.Value.Value, sourceDir, opts, mediaType); body != "" {
					return wrapBody(body, bodyKind)
				}
			}
			if ex.Value != nil && ex.Value.ExternalValue != "" {
				if body := loadExternalExample(ex.Value.ExternalValue, sourceDir, opts); body != "" {
					return wrapBody(body, bodyKind)
				}
			}
		}
	}

	// Fallback to single example
	if media.Example != nil {
		if body := examplePayload(media.Example, sourceDir, opts, mediaType); body != "" {
			return wrapBody(body, bodyKind)
		}
	}

	// Schema-level example
	if media.Schema != nil && media.Schema.Value != nil && media.Schema.Value.Example != nil {
		if body := examplePayload(media.Schema.Value.Example, sourceDir, opts, mediaType); body != "" {
			return wrapBody(body, bodyKind)
		}
	}

	// Synthesize a minimal example from required schema fields.
	if media.Schema != nil && media.Schema.Value != nil && bodyKind == "json" {
		if ex, ok := synthesizeExample(media.Schema); ok {
			if body := marshalExample(ex); body != "" {
				return wrapBody(body, bodyKind)
			}
		}
	}

	return ""
}

func bodyKindFromMediaType(mt string) string {
	mt = strings.ToLower(mt)
	switch {
	case strings.Contains(mt, "json"):
		return "json"
	case strings.Contains(mt, "xml"):
		return "xml"
	default:
		return "text"
	}
}

func wrapBody(body string, kind string) string {
	return fmt.Sprintf("body:%s {\n%s\n}\n", kind, indentBody(body, 2))
}

func indentBody(body string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	body = strings.TrimSpace(body)
	return prefix + strings.ReplaceAll(body, "\n", "\n"+prefix)
}

func examplePayload(v any, sourceDir string, opts Options, mediaType string) string {
	isXML := strings.Contains(strings.ToLower(mediaType), "xml")
	// If v is a string pointing to a URL or file, load it.
	if s, ok := v.(string); ok {
		if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "file://") || fileExists(filepath.Join(sourceDir, s)) || filepath.IsAbs(s) {
			if body := loadExternalExample(s, sourceDir, opts); body != "" {
				if !isXML && json.Valid([]byte(body)) {
					var buf bytes.Buffer
					if err := json.Indent(&buf, []byte(body), "", "  "); err == nil {
						return buf.String()
					}
				}
				return body
			}
		}
		if isXML {
			return strings.TrimSpace(s)
		}
	}
	if isXML {
		// We don't attempt to marshal arbitrary objects to XML; require string.
		return ""
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func marshalExample(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func loadExternalExample(ref string, sourceDir string, opts Options) string {
	if after, ok := strings.CutPrefix(ref, "file://"); ok {
		ref = after
	}

	// Resolve relative refs using spec base location when available.
	base := opts.BaseLocation
	if base != nil && base.Scheme != "" && base.Host != "" {
		if u, err := base.Parse(ref); err == nil {
			ref = u.String()
		}
	} else if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") && !filepath.IsAbs(ref) {
		ref = filepath.Join(sourceDir, ref)
	}

	u, err := url.Parse(ref)
	if err == nil && u.Scheme != "" && u.Host != "" {
		sameOrigin := false
		if base != nil && (base.Scheme == "http" || base.Scheme == "https") {
			sameOrigin = base.Scheme == u.Scheme && base.Host == u.Host
		}
		if !sameOrigin && !opts.AllowRemoteRefs {
			return ""
		}
		client := http.DefaultClient
		if opts.Insecure {
			client = insecureHTTPClient()
		}
		resp, err := client.Get(ref)
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err == nil {
			return string(b)
		}
		return ""
	}

	path := ref
	if !filepath.IsAbs(path) && base != nil && base.Path != "" {
		path = filepath.Clean(filepath.Join(filepath.Dir(base.Path), ref))
	}
	if !isSafeLocalRef(path, filepath.Dir(opts.Source), opts.AllowFileRefs) {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func synthesizeExample(sref *openapi3.SchemaRef) (any, bool) {
	if sref == nil || sref.Value == nil {
		return nil, false
	}
	s := sref.Value
	switch firstType(s) {
	case "object":
		obj := map[string]any{}
		for name, prop := range s.Properties {
			if prop == nil || prop.Value == nil {
				continue
			}
			if len(s.Required) > 0 && !contains(s.Required, name) {
				continue
			}
			if ex, ok := synthesizeExample(prop); ok {
				obj[name] = ex
			}
		}
		return obj, true
	case "array":
		if s.Items != nil {
			if ex, ok := synthesizeExample(s.Items); ok {
				return []any{ex}, true
			}
		}
		return []any{}, true
	case "integer", "number":
		return 0, true
	case "boolean":
		return true, true
	default:
		return "string", true
	}
}

func contains(list []string, val string) bool {
	return slices.Contains(list, val)
}

func appendArrayChecks(checks *[]string, valExpr string, s *openapi3.Schema, level StrictnessLevel, depth int) {
	if s.MinItems > 0 {
		*checks = append(*checks, fmt.Sprintf("expect(%s.length).to.be.at.least(%d);", valExpr, s.MinItems))
	}
	if s.MaxItems != nil && *s.MaxItems > 0 {
		*checks = append(*checks, fmt.Sprintf("expect(%s.length).to.be.at.most(%d);", valExpr, *s.MaxItems))
	}
	if s.UniqueItems {
		*checks = append(*checks, fmt.Sprintf("expect((function(arr){ var seen={}; for (var i=0;i<arr.length;i++){ var k=String(arr[i]); if(seen[k]){ return false; } seen[k]=true; } return true;})(%s)).to.equal(true);", valExpr))
	}
	if s.Items != nil && s.Items.Value != nil {
		itemType := firstType(s.Items.Value)
		if itemType != "" {
			switch itemType {
			case "array":
				*checks = append(*checks, fmt.Sprintf("expect(%s.every(function(it){ return Array.isArray(it); })).to.equal(true);", valExpr))
			case "object":
				if level >= StrictnessStrict {
					*checks = append(*checks, fmt.Sprintf("expect(%s.every(function(it){ return typeof it === 'object' && !Array.isArray(it); })).to.equal(true);", valExpr))
				}
			default:
				*checks = append(*checks, fmt.Sprintf("expect(%s.every(function(it){ return typeof it === '%s'; })).to.equal(true);", valExpr, jsTypeFor(itemType)))
			}
		}
		if len(s.Items.Value.Enum) > 0 && level >= StrictnessStrict {
			*checks = append(*checks, fmt.Sprintf("expect(%s.every(function(it){ return %s.includes(it); })).to.equal(true);", valExpr, toJSArray(s.Items.Value.Enum)))
		}
		if depth > 0 && level >= StrictnessStrict {
			nested := propertyChecks(s.Items.Value, "it", level, depth-1)
			if len(nested) > 0 {
				*checks = append(*checks, fmt.Sprintf("%s.forEach(function(it){ %s });", valExpr, strings.Join(nested, " ")))
			}
		}
	}
}

func firstType(s *openapi3.Schema) string {
	if s == nil || s.Type == nil || len(*s.Type) == 0 {
		return ""
	}
	return (*s.Type)[0]
}

func variantTypeConds(refs openapi3.SchemaRefs, expr string, level StrictnessLevel, depth int) []string {
	conds := []string{}
	for _, ref := range refs {
		if ref.Value == nil {
			continue
		}
		if cond := variantCondition(ref.Value, expr, level, depth); cond != "" {
			conds = append(conds, cond)
		}
	}
	return conds
}

func variantCondition(s *openapi3.Schema, expr string, level StrictnessLevel, depth int) string {
	parts := []string{}
	if cond := typeCondition(s, expr); cond != "" {
		parts = append(parts, cond)
	}
	if s.Discriminator != nil && s.Discriminator.PropertyName != "" {
		p := s.Discriminator.PropertyName
		parts = append(parts, fmt.Sprintf("%s && %s.hasOwnProperty('%s')", expr, expr, p))
		if len(s.Discriminator.Mapping) > 0 {
			vals := make([]string, 0, len(s.Discriminator.Mapping))
			for v := range s.Discriminator.Mapping {
				vals = append(vals, v)
			}
			parts = append(parts, fmt.Sprintf("[%s].includes(%s['%s'])", toJSArrayString(vals), expr, p))
		}
	}
	for _, req := range s.Required {
		parts = append(parts, fmt.Sprintf("%s && %s.hasOwnProperty('%s')", expr, expr, req))
	}
	for name, prop := range s.Properties {
		if prop.Value == nil {
			continue
		}
		pchecks := propertyChecks(prop.Value, fmt.Sprintf("%s['%s']", expr, name), level, depth)
		if len(pchecks) > 0 {
			parts = append(parts, fmt.Sprintf("(function(){ %s; return true; })()", strings.Join(pchecks, " ")))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " && ") + ")"
}

func discriminatorSwitch(s *openapi3.Schema, expr string, doc *openapi3.T, level StrictnessLevel, depth int) string {
	if s == nil || s.Discriminator == nil || s.Discriminator.PropertyName == "" {
		return ""
	}

	mapping := s.Discriminator.Mapping
	if len(mapping) == 0 {
		return ""
	}

	branches := []string{}
	choices := append(openapi3.SchemaRefs{}, s.OneOf...)
	choices = append(choices, s.AnyOf...)
	if doc != nil && doc.Components != nil {
		for name, ref := range doc.Components.Schemas {
			choices = append(choices, &openapi3.SchemaRef{Ref: "#/components/schemas/" + name, Value: ref.Value})
		}
	}

	for discVal, ref := range mapping {
		sch := findSchemaForMapping(ref, choices)
		checks := []string{}
		if sch != nil {
			for name, prop := range sch.Properties {
				if prop.Value == nil {
					continue
				}
				pchecks := propertyChecks(prop.Value, fmt.Sprintf("%s['%s']", expr, name), level, depth)
				if len(pchecks) > 0 {
					checks = append(checks, strings.Join(pchecks, " "))
				}
			}
			for _, req := range sch.Required {
				checks = append(checks, fmt.Sprintf("expect(%s).to.have.property('%s');", expr, req))
			}
		}
		body := strings.Join(checks, " ")
		branch := fmt.Sprintf("case %q: %s break;", discVal, body)
		branches = append(branches, branch)
	}

	if len(branches) == 0 {
		return ""
	}
	code := fmt.Sprintf("switch(%s['%s']) { %s default: expect(false).to.equal(true); }", expr, s.Discriminator.PropertyName, strings.Join(branches, " "))
	return code
}

func findSchemaForMapping(mappingRef string, choices openapi3.SchemaRefs) *openapi3.Schema {
	for _, c := range choices {
		if c.Ref != "" && (c.Ref == mappingRef || strings.HasSuffix(c.Ref, mappingRef)) {
			return c.Value
		}
	}
	return nil
}

func typeCondition(s *openapi3.Schema, expr string) string {
	t := firstType(s)
	switch t {
	case "array":
		return fmt.Sprintf("Array.isArray(%s)", expr)
	case "object":
		return fmt.Sprintf("typeof %s === 'object' && !Array.isArray(%s)", expr, expr)
	case "integer", "number":
		return fmt.Sprintf("typeof %s === 'number'", expr)
	case "boolean":
		return fmt.Sprintf("typeof %s === 'boolean'", expr)
	case "string":
		return fmt.Sprintf("typeof %s === 'string'", expr)
	}
	if len(s.Properties) > 0 || len(s.Required) > 0 {
		return fmt.Sprintf("typeof %s === 'object' && !Array.isArray(%s)", expr, expr)
	}
	return ""
}

func formatCheck(format, valExpr string) string {
	switch strings.ToLower(format) {
	case "email":
		return fmt.Sprintf("expect(/^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$/.test(%s)).to.equal(true);", valExpr)
	case "uuid":
		return fmt.Sprintf("expect(/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$/.test(%s)).to.equal(true);", valExpr)
	case "uri", "url":
		// URI validation is tricky; rely on type check only for now.
		return ""
	case "ipv4":
		return fmt.Sprintf("expect(/^(25[0-5]|2[0-4]\\d|[01]?\\d?\\d)(\\.(25[0-5]|2[0-4]\\d|[01]?\\d?\\d)){3}$/.test(%s)).to.equal(true);", valExpr)
	case "ipv6":
		return fmt.Sprintf("expect(/^([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}$/.test(%s)).to.equal(true);", valExpr)
	case "hostname":
		return fmt.Sprintf("expect(/^(?=.{1,253}$)(?!-)[A-Za-z0-9-]{1,63}(\\.(?!-)[A-Za-z0-9-]{1,63})*$/.test(%s)).to.equal(true);", valExpr)
	case "cidr":
		return fmt.Sprintf("expect(/^(?:\\d{1,3}\\.){3}\\d{1,3}\\/(?:[0-9]|[12]\\d|3[0-2])$/.test(%s)).to.equal(true);", valExpr)
	case "ipv6-cidr":
		// IPv6 CIDR validation is complex; skip strict regex for now.
		return ""
	case "byte":
		return fmt.Sprintf("expect(/^(?:[A-Za-z0-9+\\/]{4})*(?:[A-Za-z0-9+\\/]{2}==|[A-Za-z0-9+\\/]{3}=)?$/.test(%s)).to.equal(true);", valExpr)
	case "date-time":
		return fmt.Sprintf("expect(!isNaN(Date.parse(%s))).to.equal(true);", valExpr)
	case "date":
		return fmt.Sprintf("expect(/^\\d{4}-\\d{2}-\\d{2}$/.test(%s)).to.equal(true);", valExpr)
	default:
		return ""
	}
}

func patternCheck(pattern, valExpr string) string {
	if pattern == "" {
		return ""
	}
	if len(pattern) > 512 {
		return "" // avoid pathological patterns
	}
	if complexRegex(pattern) {
		return "" // skip potentially catastrophic patterns
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return "" // skip invalid regexes
	}
	return fmt.Sprintf("expect(new RegExp(%q).test(%s)).to.equal(true);", pattern, valExpr)
}

// complexRegex provides a cheap heuristic to avoid patterns likely to be catastrophic.
func complexRegex(pattern string) bool {
	depth := 0
	maxDepth := 0
	quantifiers := 0
	lookbehinds := 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '(':
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case ')':
			if depth > 0 {
				depth--
			}
		case '*', '+', '?':
			quantifiers++
		}
		// crude lookbehind detection
		if pattern[i] == '(' && i+3 < len(pattern) && pattern[i+1] == '?' && (pattern[i+2] == '<' || pattern[i+2] == 'P') {
			lookbehinds++
		}
	}
	quantifiers += strings.Count(pattern, "{")
	// Heuristic: excessive nesting combined with many quantifiers or lookbehinds is risky.
	return maxDepth > 5 || quantifiers > 30 || lookbehinds > 0
}

func isType(s *openapi3.Schema, want string) bool {
	if s == nil || s.Type == nil {
		return false
	}
	return slices.Contains(*s.Type, want)
}

func jsTypeFor(openapiType string) string {
	switch openapiType {
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		return "object"
	default:
		return "string"
	}
}

var testsBlockRe = regexp.MustCompile(`(?m)^(\s*)tests\s*{`)

func validJS(code string, log pslog.Logger) bool {
	// Light syntax check using goja parser; if it fails, skip tests block.
	// Bruno's `tests { ... }` block is not valid JS, so rewrite it to a function
	// before parsing purely for validation.
	var transformed string
	if testsBlockRe.MatchString(code) {
		transformed = testsBlockRe.ReplaceAllString(code, "${1}function __tests__() {")
	} else {
		transformed = "(function(){\n" + code + "\n})();"
	}

	_, err := goja.Parse("generated.js", transformed)
	if err != nil {
		if log != nil {
			log.Error("import.openapi.tests.invalid-js.parse", "err", err)
			log.Debug("import.openapi.tests.invalid-js.transformed", "code", transformed)
			log.Debug("import.openapi.tests.invalid-js.bytes", "bytes", fmt.Sprintf("%q", []byte(transformed)))
		}
		return false
	}
	return true
}

func toJSArray(vals []any) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		switch vv := v.(type) {
		case string:
			parts = append(parts, fmt.Sprintf("%q", vv))
		default:
			parts = append(parts, fmt.Sprint(vv))
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func toJSArrayString(vals []string) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, fmt.Sprintf("%q", v))
	}
	return "[" + strings.Join(parts, ",") + "]"
}
