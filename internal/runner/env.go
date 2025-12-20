package runner

import (
	"bufio"
	"context"
	"maps"
	"os"
	"strings"

	"pkt.systems/gruno/internal/parser"
)

// loadEnv parses an env .bru file containing vars { key: value }.
func loadEnv(ctx context.Context, path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	vars := map[string]string{}
	scanner := bufio.NewScanner(f)
	inVars := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if !inVars {
			if strings.HasPrefix(line, "vars") {
				inVars = true
			}
			continue
		}
		if line == "}" {
			break
		}
		kv := strings.SplitN(line, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		val = strings.TrimSuffix(val, ",")
		vars[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return vars, nil
}

// expander replaces {{var}} tokens using the provided map or environment variables.
type expander struct {
	vars map[string]string
}

func newExpander(vars map[string]string) *expander {
	m := map[string]string{}
	maps.Copy(m, vars)
	return &expander{vars: m}
}

func (e *expander) get(key string) (string, bool) {
	if e == nil {
		return "", false
	}
	if after, ok := strings.CutPrefix(key, "process.env."); ok {
		k := after
		if v, ok := os.LookupEnv(k); ok {
			return v, true
		}
		return "", false
	}
	if v, ok := e.vars[key]; ok {
		return v, true
	}
	if v, ok := os.LookupEnv(key); ok {
		return v, true
	}
	return "", false
}

func (e *expander) set(key, val string) {
	if e == nil {
		return
	}
	if e.vars == nil {
		e.vars = map[string]string{}
	}
	e.vars[key] = val
}

func (e *expander) expand(s string) string {
	return parser.VarPattern.ReplaceAllStringFunc(s, func(match string) string {
		inner := strings.TrimSpace(match[2 : len(match)-2])
		if v, ok := e.get(inner); ok {
			return v
		}
		return match
	})
}
