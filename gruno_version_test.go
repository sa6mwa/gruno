package gruno

import "testing"

func TestVersionUsesModuleVersion(t *testing.T) {
	old := moduleVersion
	t.Cleanup(func() { moduleVersion = old })
	moduleVersion = func(path string) string {
		if path != modulePath {
			t.Fatalf("unexpected module path: %q", path)
		}
		return "v1.2.3"
	}

	if got := Version(); got != "v1.2.3" {
		t.Fatalf("expected module version, got %q", got)
	}
}
