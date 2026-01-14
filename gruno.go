package gruno

import (
	"context"

	"pkt.systems/gruno/internal/runner"
	"pkt.systems/version"
)

// Public type aliases to runner package

// Gruno exposes methods to run .bru files or folders.
type (
	Gruno = runner.Gruno
	// RunOptions configure a single run invocation.
	RunOptions = runner.RunOptions
	// CaseResult captures the outcome of a single case.
	CaseResult = runner.CaseResult
	// RunSummary aggregates case results from a folder run.
	RunSummary = runner.RunSummary
	// AssertionFailure mirrors a failed JS assertion.
	AssertionFailure = runner.AssertionFailure
	// HookInfo carries request metadata provided to hooks.
	HookInfo = runner.HookInfo
)

// Option tweaks runner construction.
type Option = runner.Option

var (
	// WithLogger supplies a custom pslog logger.
	WithLogger = runner.WithLogger
	// WithHTTPClient injects a custom HTTP client.
	WithHTTPClient = runner.WithHTTPClient
	// WithTimeout sets a default per-request timeout.
	WithTimeout = runner.WithTimeout
	// WithPreRequestHook registers a Go hook invoked before each .bru request (logger provided).
	WithPreRequestHook = runner.WithPreRequestHook
	// WithPostRequestHook registers a Go hook invoked after each .bru request (logger provided).
	WithPostRequestHook = runner.WithPostRequestHook
)

// New constructs a Gruno instance.
func New(ctx context.Context, opts ...Option) (Gruno, error) {
	return runner.New(ctx, opts...)
}

// Version returns the current module version (best effort).
func Version() string {
	return moduleVersion(modulePath)
}

const modulePath = "pkt.systems/gruno"

var moduleVersion = version.ModuleVersion
