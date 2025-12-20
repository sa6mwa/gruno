package importer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAPIIncludePathFiltersRoutes(t *testing.T) {
	tmp, err := os.MkdirTemp("", "gru-include-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	spec := filepath.Join("..", "..", "sampledata", "openapi", "schema_tests.json")
	if err := ImportOpenAPI(context.Background(), Options{Source: spec, OutputDir: tmp, IncludePaths: []string{"/schema/variant"}}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Expect only one generated request under include path
	bruCount := 0
	filepath.Walk(tmp, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Ext(path) == ".bru" && info.Name() != "bruno.json" {
			if strings.Contains(path, string(filepath.Separator)+"environments"+string(filepath.Separator)) {
				return nil
			}
			bruCount++
		}
		return nil
	})
	if bruCount != 1 {
		t.Fatalf("expected 1 .bru file, got %d", bruCount)
	}
}
