package runner

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
)

type iterationSpec struct {
	vars map[string]string
	data map[string]any
}

// buildIterations resolves the iteration plan based on CSV/JSON datasets or
// iteration count. When no data source is provided, a single iteration is
// returned by default.
func buildIterations(opts RunOptions) ([]iterationSpec, error) {
	if opts.CSVFilePath != "" && opts.JSONFilePath != "" {
		return nil, fmt.Errorf("csv-file-path and json-file-path cannot be used together")
	}

	if opts.CSVFilePath != "" {
		return readCSVIterations(opts.CSVFilePath)
	}
	if opts.JSONFilePath != "" {
		return readJSONIterations(opts.JSONFilePath)
	}

	count := opts.IterationCount
	if count <= 0 {
		count = 1
	}
	its := make([]iterationSpec, count)
	for i := 0; i < count; i++ {
		its[i] = iterationSpec{vars: map[string]string{}, data: map[string]any{}}
	}
	return its, nil
}

func readCSVIterations(path string) ([]iterationSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("csv-file-path: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	headers, err := r.Read()
	if err == io.EOF {
		return nil, fmt.Errorf("csv-file-path %s is empty", path)
	}
	if err != nil {
		return nil, fmt.Errorf("csv-file-path: %w", err)
	}
	for i, h := range headers {
		headers[i] = strings.TrimSpace(h)
	}

	var out []iterationSpec
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv-file-path: %w", err)
		}
		strs := map[string]string{}
		raw := map[string]any{}
		for i, h := range headers {
			val := ""
			if i < len(row) {
				val = strings.TrimSpace(row[i])
			}
			strs[h] = val
			raw[h] = val
		}
		out = append(out, iterationSpec{vars: strs, data: raw})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("csv-file-path %s contains no data rows", path)
	}
	return out, nil
}

func readJSONIterations(path string) ([]iterationSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("json-file-path: %w", err)
	}
	defer f.Close()

	var raw any
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return nil, fmt.Errorf("json-file-path: %w", err)
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("json-file-path %s must be a JSON array", path)
	}
	var out []iterationSpec
	for _, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("json-file-path %s must contain objects", path)
		}
		strs := map[string]string{}
		rawCopy := map[string]any{}
		for k, v := range obj {
			strs[k] = fmt.Sprint(v)
			rawCopy[k] = v
		}
		out = append(out, iterationSpec{vars: strs, data: rawCopy})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("json-file-path %s contains no data rows", path)
	}
	return out, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	out := map[string]string{}
	maps.Copy(out, in)
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	out := map[string]any{}
	maps.Copy(out, in)
	return out
}
