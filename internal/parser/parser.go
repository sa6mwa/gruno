package parser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var verbSet = map[string]struct{}{
	"get":    {},
	"post":   {},
	"put":    {},
	"patch":  {},
	"delete": {},
}

// ParsedFile captures a parsed .bru file.
type ParsedFile struct {
	FilePath string
	Meta     MetaBlock
	Request  RequestBlock
	TestsRaw string
	Docs     string
	Assert   []AssertRule
	Scripts  ScriptBlock
	VarsPre  map[string]string
	VarsPost map[string]string
}

// MetaBlock stores top-level meta attributes of a case.
type MetaBlock struct {
	Name        string
	Type        string
	Seq         float64
	Tags        []string
	Description string
	Skip        bool
	DelayMS     int
	Repeat      int
	TimeoutMS   int
	Settings    MetaSettings
}

// MetaSettings holds script-level settings for a case.
type MetaSettings struct {
	Script string
}

// RequestBlock models the HTTP request section of a .bru file.
type RequestBlock struct {
	Verb        string
	URL         string
	Headers     map[string]string
	Body        BodyBlock
	Query       map[string]string
	PathParams  map[string]string
	GraphqlVars map[string]string
}

// BodyBlock represents the body block (json/xml/text/form/etc.).
type BodyBlock struct {
	Raw     string
	Type    string // json, xml, text, graphql, form-urlencoded, multipart-form, raw
	Fields  map[string]string
	Present bool
}

// ScriptBlock contains pre/post response scripts.
type ScriptBlock struct {
	PreRequest     string
	PostResponse   string
	SettingsScript string
}

// AssertRule is a parsed assert rule from the assert block.
type AssertRule struct {
	Left  string
	Op    string
	Right string
}

// DiscoverBruFiles walks a folder and parses .bru files (skipping environments).
func DiscoverBruFiles(folder string, recursive bool) ([]ParsedFile, error) {
	var files []ParsedFile
	rootDepth := strings.Count(filepath.Clean(folder), string(os.PathSeparator))
	err := filepath.WalkDir(folder, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.EqualFold(d.Name(), "environments") {
				return filepath.SkipDir
			}
			if !recursive && strings.Count(filepath.Clean(path), string(os.PathSeparator)) > rootDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".bru") {
			pf, perr := ParseFile(context.Background(), path)
			if perr != nil {
				// Skip env-style .bru files that lack a request block.
				if errors.Is(perr, errMissingRequest) {
					return nil
				}
				return perr
			}
			files = append(files, pf)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// ParseFile reads and parses a single .bru file.
func ParseFile(ctx context.Context, path string) (ParsedFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return ParsedFile{}, err
	}
	defer f.Close()
	return parse(ctx, path, f)
}

func parse(ctx context.Context, path string, r io.Reader) (ParsedFile, error) {
	scanner := bufio.NewScanner(r)
	pf := ParsedFile{FilePath: path}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "meta"):
			block, err := readBlock(scanner, line)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("meta: %w", err)
			}
			meta, err := parseMeta(block)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("meta: %w", err)
			}
			pf.Meta = meta
		case strings.HasPrefix(lower, "tests"):
			tests, err := readBlockWithBraces(line, scanner)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("tests: %w", err)
			}
			pf.TestsRaw = tests
		case strings.HasPrefix(lower, "docs"):
			docs, err := readBlockWithBraces(line, scanner)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("docs: %w", err)
			}
			pf.Docs = docs
		case strings.HasPrefix(lower, "assert"):
			block, err := readBlock(scanner, line)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("assert: %w", err)
			}
			pf.Assert = parseAssert(block)
		case strings.HasPrefix(lower, "script:pre-request"):
			block, err := readBlockWithBraces(line, scanner)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("script pre: %w", err)
			}
			pf.Scripts.PreRequest = block
		case strings.HasPrefix(lower, "script:post-response"):
			block, err := readBlockWithBraces(line, scanner)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("script post: %w", err)
			}
			pf.Scripts.PostResponse = block
		case strings.HasPrefix(lower, "vars:pre-request"):
			block, err := readBlock(scanner, line)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("vars pre: %w", err)
			}
			pf.VarsPre = parseKVBlock(block)
		case strings.HasPrefix(lower, "vars:post-response"):
			block, err := readBlock(scanner, line)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("vars post: %w", err)
			}
			pf.VarsPost = parseKVBlock(block)
		case strings.HasPrefix(lower, "headers"):
			block, err := readBlock(scanner, line)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("headers: %w", err)
			}
			hdrs := parseHeaders(block)
			if pf.Request.Headers == nil {
				pf.Request.Headers = map[string]string{}
			}
			maps.Copy(pf.Request.Headers, hdrs)
		case strings.HasPrefix(lower, "query"):
			block, err := readBlock(scanner, line)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("query: %w", err)
			}
			pf.Request.Query = parseKVBlock(block)
		case strings.HasPrefix(lower, "params:query"):
			block, err := readBlock(scanner, line)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("params:query: %w", err)
			}
			pf.Request.Query = parseKVBlock(block)
		case strings.HasPrefix(lower, "params:path"):
			block, err := readBlock(scanner, line)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("params:path: %w", err)
			}
			pf.Request.PathParams = parseKVBlock(block)
		case strings.HasPrefix(lower, "body:graphql:vars"):
			block, err := readBlockWithBraces(line, scanner)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("graphql vars: %w", err)
			}
			if varsMap, err := parseJSONMap(block); err == nil {
				pf.Request.GraphqlVars = varsMap
			} else {
				pf.Request.GraphqlVars = parseKVBlock(strings.Split(block, "\n"))
			}
		case strings.HasPrefix(lower, "body:"):
			bType := strings.TrimSpace(strings.TrimPrefix(lower, "body:"))
			bType = strings.TrimSpace(strings.TrimSuffix(bType, "{"))
			block, err := readBlockWithBraces(line, scanner)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("body: %w", err)
			}
			pf.Request.Body.Present = true
			if bType == "" {
				bType = "json"
			}
			pf.Request.Body.Type = strings.ToLower(strings.TrimSpace(bType))
			pf.Request.Body.Raw = block
			if pf.Request.Body.Type == "form-urlencoded" || pf.Request.Body.Type == "multipart-form" {
				pf.Request.Body.Fields = parseFormBlock(strings.Split(block, "\n"))
			}
		case strings.HasPrefix(lower, "body"):
			// plain body treated as JSON
			block, err := readBlockWithBraces(line, scanner)
			if err != nil {
				return ParsedFile{}, fmt.Errorf("body: %w", err)
			}
			pf.Request.Body.Present = true
			pf.Request.Body.Type = "json"
			pf.Request.Body.Raw = block
		default:
			for verb := range verbSet {
				if strings.HasPrefix(lower, verb) {
					block, err := readBlock(scanner, line)
					if err != nil {
						return ParsedFile{}, fmt.Errorf("request: %w", err)
					}
					req, err := parseRequest(verb, block)
					if err != nil {
						return ParsedFile{}, fmt.Errorf("request: %w", err)
					}
					pf.Request = req
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ParsedFile{}, err
	}
	if pf.Request.Verb == "" {
		return ParsedFile{}, errMissingRequest
	}
	return pf, nil
}

var errMissingRequest = errors.New("missing request block")

func parseMeta(lines []string) (MetaBlock, error) {
	m := MetaBlock{}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "//") {
			continue
		}
		parts := strings.SplitN(l, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		val = strings.TrimSuffix(val, ",")
		switch key {
		case "name":
			m.Name = val
		case "type":
			m.Type = val
		case "seq":
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return MetaBlock{}, err
			}
			m.Seq = f
		case "description":
			m.Description = strings.Trim(val, "\"")
		case "skip":
			m.Skip = strings.EqualFold(val, "true")
		case "delay":
			if v, err := strconv.Atoi(val); err == nil {
				m.DelayMS = v
			}
		case "repeat":
			if v, err := strconv.Atoi(val); err == nil {
				m.Repeat = v
			}
		case "timeout":
			if v, err := strconv.Atoi(val); err == nil {
				m.TimeoutMS = v
			}
		case "tags":
			// tags: [a, b]
			val = strings.Trim(val, "[] \"")
			if val != "" {
				parts := strings.SplitSeq(val, ",")
				for p := range parts {
					t := strings.Trim(strings.TrimSpace(p), "\"")
					if t != "" {
						m.Tags = append(m.Tags, t)
					}
				}
			}
		case "settings":
			// settings: { script: "prelude.js" }
			val = strings.Trim(val, "{} ")
			if strings.Contains(val, "script") {
				val = strings.TrimSpace(strings.TrimPrefix(val, "script"))
				val = strings.TrimPrefix(val, ":")
				m.Settings.Script = strings.Trim(val, "\" ")
			}
		case "enabled":
			m.Skip = strings.EqualFold(val, "false")
		}
	}
	return m, nil
}

func parseRequest(verb string, lines []string) (RequestBlock, error) {
	req := RequestBlock{Verb: strings.ToUpper(verb), Headers: map[string]string{}}
	inHeaders := false
	inBody := false
	var bodyLines []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}

		if after, ok := strings.CutPrefix(trimmed, "url:"); ok {
			urlPart := strings.TrimSpace(after)
			if sp := strings.Index(urlPart, " "); sp >= 0 {
				urlPart = strings.TrimSpace(urlPart[:sp])
			}
			req.URL = urlPart
			// continue parsing rest of line for inline headers/body
		}

		// Inline headers block: headers { X: 1 }
		if idx := strings.Index(trimmed, "headers"); idx >= 0 && strings.Contains(trimmed[idx:], "{") && strings.Contains(trimmed[idx:], "}") {
			if braceRel := strings.Index(trimmed[idx:], "{"); braceRel >= 0 {
				start := idx + braceRel
				if content, _, ok := findBalancedAt(trimmed, start); ok {
					hdrs := parseHeaders(strings.Split(content, "\n"))
					maps.Copy(req.Headers, hdrs)
				}
			}
		}

		// Inline body: body:json { ... }
		if idx := strings.Index(trimmed, "body:"); idx >= 0 && strings.Contains(trimmed[idx:], "{") && strings.Contains(trimmed[idx:], "}") {
			req.Body.Present = true
			req.Body.Type = detectBodyType(trimmed[idx:])
			if braceRel := strings.Index(trimmed[idx:], "{"); braceRel >= 0 {
				start := idx + braceRel
				if content, _, ok := findBalancedAt(trimmed, start); ok {
					req.Body.Raw = content
					continue
				}
			}
		}

		if strings.HasPrefix(trimmed, "headers") {
			inHeaders = true
			continue
		}
		if strings.HasPrefix(trimmed, "body:") || strings.HasPrefix(trimmed, "body ") {
			req.Body.Type = "json"
			if strings.Contains(strings.ToLower(trimmed), "xml") {
				req.Body.Type = "xml"
			}
			if strings.Contains(strings.ToLower(trimmed), "text") {
				req.Body.Type = "text"
			}
			if strings.Contains(strings.ToLower(trimmed), "form-urlencoded") {
				req.Body.Type = "form-urlencoded"
			}
			if strings.Contains(strings.ToLower(trimmed), "multipart-form") {
				req.Body.Type = "multipart-form"
			}
			if strings.Contains(strings.ToLower(trimmed), "graphql") {
				req.Body.Type = "graphql"
			}
			inBody = true
			req.Body.Present = true
			continue
		}
		if inHeaders {
			if trimmed == "}" {
				inHeaders = false
				continue
			}
			kv := strings.SplitN(trimmed, ":", 2)
			if len(kv) == 2 {
				k := strings.Trim(strings.TrimSpace(kv[0]), "\"")
				v := strings.TrimSpace(kv[1])
				v = strings.TrimSuffix(v, ",")
				req.Headers[k] = v
			}
			continue
		}
		if inBody {
			if trimmed == "}" {
				inBody = false
				continue
			}
			bodyLines = append(bodyLines, l)
			continue
		}
	}
	if len(bodyLines) > 0 {
		req.Body.Raw = strings.Join(bodyLines, "\n")
	}
	return req, nil
}

func parseHeaders(lines []string) map[string]string {
	h := map[string]string{}
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		kv := strings.SplitN(trimmed, ":", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.Trim(strings.TrimSpace(kv[0]), "\"")
		v := strings.TrimSpace(kv[1])
		v = strings.TrimSuffix(v, ",")
		h[k] = v
	}
	return h
}

func parseKVBlock(lines []string) map[string]string {
	m := map[string]string{}
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "~") {
			continue
		}
		kv := strings.SplitN(trimmed, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.Trim(strings.TrimSpace(kv[0]), "\"")
		val := strings.TrimSpace(kv[1])
		val = strings.TrimSuffix(val, ",")
		m[key] = val
	}
	return m
}

func parseFormBlock(lines []string) map[string]string {
	m := map[string]string{}
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "~") {
			continue
		}
		kv := strings.SplitN(trimmed, ":", 2)
		if len(kv) != 2 {
			continue
		}
		k := strings.TrimSpace(kv[0])
		v := strings.TrimSpace(kv[1])
		v = strings.TrimSuffix(v, ",")
		m[k] = v
	}
	return m
}

func parseJSONMap(raw string) (map[string]string, error) {
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func detectBodyType(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "form-urlencoded"):
		return "form-urlencoded"
	case strings.Contains(lower, "multipart-form"):
		return "multipart-form"
	case strings.Contains(lower, "xml"):
		return "xml"
	case strings.Contains(lower, "text"):
		return "text"
	case strings.Contains(lower, "graphql"):
		return "graphql"
	default:
		return "json"
	}
}

// findBalancedAt returns the content between the brace at start and its matching closing brace.
func findBalancedAt(s string, start int) (content string, end int, ok bool) {
	depth := 0
	startContent := -1
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			if depth == 0 {
				startContent = i + 1
			}
			depth++
		case '}':
			depth--
			if depth == 0 && startContent >= 0 {
				return s[startContent:i], i, true
			}
		}
	}
	return "", -1, false
}

// findBalancedInline finds a balanced brace pair in a single line and returns the inner bounds.
func findBalancedInline(s string) (start int, end int, ok bool) {
	idx := strings.Index(s, "{")
	if idx < 0 {
		return 0, 0, false
	}
	content, endIdx, ok := findBalancedAt(s, idx)
	if !ok {
		return 0, 0, false
	}
	_ = content
	return idx + 1, endIdx, true
}

func parseAssert(lines []string) []AssertRule {
	var rules []AssertRule
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		parts := strings.Split(trimmed, ":")
		if len(parts) < 2 {
			continue
		}
		left := strings.TrimSpace(parts[0])
		rightParts := strings.Fields(strings.TrimSpace(strings.Join(parts[1:], ":")))
		if len(rightParts) < 2 {
			continue
		}
		rules = append(rules, AssertRule{Left: left, Op: rightParts[0], Right: strings.Join(rightParts[1:], " ")})
	}
	return rules
}

func readBlock(scanner *bufio.Scanner, firstLine string) ([]string, error) {
	if !strings.Contains(firstLine, "{") {
		return nil, errors.New("missing opening brace")
	}
	var lines []string

	if first, last, ok := findBalancedInline(firstLine); ok {
		if trimmed := strings.TrimSpace(firstLine[first:last]); trimmed != "" {
			lines = append(lines, trimmed)
		}
		return lines, nil
	}

	depth := strings.Count(firstLine, "{") - strings.Count(firstLine, "}")
	for scanner.Scan() {
		line := scanner.Text()
		depth += strings.Count(line, "{")
		depth -= strings.Count(line, "}")
		if depth <= 0 {
			break
		}
		lines = append(lines, line)
	}
	if depth != 0 {
		return nil, errors.New("unbalanced braces")
	}
	return lines, nil
}

func readBlockWithBraces(firstLine string, scanner *bufio.Scanner) (string, error) {
	if first, last, ok := findBalancedInline(firstLine); ok {
		content := strings.TrimSpace(firstLine[first:last])
		return content, nil
	}

	depth := strings.Count(firstLine, "{") - strings.Count(firstLine, "}")
	var sb strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		depth += strings.Count(line, "{")
		depth -= strings.Count(line, "}")
		if depth <= 0 {
			break
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	if depth != 0 {
		return "", errors.New("unbalanced braces")
	}
	return sb.String(), nil
}

// VarPattern matches {{var}} placeholders inside requests.
var VarPattern = regexp.MustCompile(`\{\{([^}]+)\}\}`)
