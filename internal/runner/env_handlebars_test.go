package runner

import (
	"os"
	"testing"
)

func TestHandlebarProcessEnvResolution(t *testing.T) {
	const key = "GRU_ENV_TEST"
	os.Setenv(key, "from-env")
	defer os.Unsetenv(key)

	exp := newExpander(map[string]string{"FILE_ONLY": "fv"})

	got := exp.expand("{{process.env." + key + "}}")
	if got != "from-env" {
		t.Fatalf("expected env value, got %q", got)
	}

	// existing var still works
	if v := exp.expand("{{FILE_ONLY}}"); v != "fv" {
		t.Fatalf("expected file var, got %q", v)
	}
}
