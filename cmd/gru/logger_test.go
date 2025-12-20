package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewLoggerDefaultInfo(t *testing.T) {
	buf := &bytes.Buffer{}
	logger, err := newLogger(false, "info", false, false, buf)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}

	logger.Debug("debug-msg")
	logger.Info("info-msg")

	out := buf.String()
	if strings.Contains(out, "debug-msg") {
		t.Fatalf("expected debug to be filtered at info level, got %q", out)
	}
	if !strings.Contains(out, "info-msg") {
		t.Fatalf("expected info message, got %q", out)
	}
}

func TestNewLoggerRespectsEnvWhenFlagUnset(t *testing.T) {
	t.Setenv("LOG_LEVEL", "trace")
	buf := &bytes.Buffer{}

	logger, err := newLogger(false, "info", false, false, buf)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	logger.Debug("debug-msg")

	if !strings.Contains(buf.String(), "debug-msg") {
		t.Fatalf("expected debug output when LOG_LEVEL=trace, got %q", buf.String())
	}
}

func TestNewLoggerFlagOverridesEnv(t *testing.T) {
	t.Setenv("LOG_LEVEL", "trace")
	buf := &bytes.Buffer{}

	logger, err := newLogger(false, "error", true, false, buf)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	logger.Info("info-msg")
	logger.Error("error-msg")

	out := buf.String()
	if strings.Contains(out, "info-msg") {
		t.Fatalf("expected info to be filtered at error level, got %q", out)
	}
	if !strings.Contains(out, "error-msg") {
		t.Fatalf("expected error message, got %q", out)
	}
}
