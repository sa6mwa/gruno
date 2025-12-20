package runner

import (
	"context"
	"net/http"
	"time"

	"pkt.systems/pslog"
)

// Gruno is the public interface exposed by this module. It is safe to hold and
// use concurrently from multiple goroutines.
type Gruno interface {
	RunFile(ctx context.Context, path string, opts RunOptions) (CaseResult, error)
	RunFolder(ctx context.Context, path string, opts RunOptions) (RunSummary, error)
}

// RunOptions controls execution of one or more .bru cases.
type RunOptions struct {
	EnvPath     string
	Vars        map[string]string
	Tags        []string
	ExcludeTags []string
	// CSVFilePath points to a CSV dataset used for data-driven iterations.
	CSVFilePath string
	// JSONFilePath points to a JSON array dataset used for data-driven iterations.
	JSONFilePath string
	// IterationCount executes the collection this many times (default 1). Ignored when a data file is provided.
	IterationCount int
	// Parallel executes cases in parallel for each iteration when true.
	Parallel bool
	// IterationIndex is the zero-based index for the current iteration (internal use).
	IterationIndex int
	// TotalIterations captures how many iterations will run (internal use).
	TotalIterations int
	// IterationData holds the raw iteration payload exposed to scripts (internal use).
	IterationData map[string]any
	HTTPClient    *http.Client
	Logger        pslog.Base
	Timeout       time.Duration // per request timeout; 0 means default (15s)
	Delay         time.Duration // delay between cases; 0 to skip
	Bail          bool          // stop after first failure
	TestsOnly     bool          // skip cases without tests/asserts
	Recursive     bool          // walk subfolders
	RecursiveSet  bool          // whether Recursive was explicitly set by caller

	// Reporter/output hints (used by CLI layer).
	OutputPath    string
	OutputFormat  string // json|junit|html
	ReporterJSON  string
	ReporterJUnit string
	ReporterHTML  string
	// ReporterSkipAllHeaders omits all request/response headers from reporter outputs.
	ReporterSkipAllHeaders bool
	// ReporterSkipHeaders removes specific headers (case-insensitive) from reporter outputs.
	ReporterSkipHeaders []string
	PreHookCmd          []string
	PostHookCmd         []string
}

// HookInfo provides the minimal request metadata exposed to user hooks without
// leaking internal parser types.
type HookInfo struct {
	Name     string
	FilePath string
	Seq      float64
	Tags     []string
	Method   string
	URL      string
}

// PreRequestHook is invoked before each request is executed (after the HTTP request has been built).
// It can mutate the *http.Request or return an error to abort the run. The logger
// is the same pslog logger used by the runner.
type PreRequestHook func(ctx context.Context, info HookInfo, req *http.Request, logger pslog.Base) error

// PostRequestHook is invoked after a request has been executed and JS tests/asserts have run.
// It receives the CaseResult and may return an error to abort the run. The logger
// is the same pslog logger used by the runner.
type PostRequestHook func(ctx context.Context, info HookInfo, res CaseResult, logger pslog.Base) error

// CaseResult captures the outcome of a single .bru case.
type CaseResult struct {
	Name       string
	FilePath   string
	RequestURL string
	// RequestHeaders captures the request headers sent for this case.
	RequestHeaders map[string]string
	// ResponseHeaders captures the response headers returned for this case.
	ResponseHeaders map[string]string
	Status          int
	Seq             float64
	Tags            []string
	Duration        time.Duration
	Passed          bool
	Skipped         bool
	Failures        []AssertionFailure
	Console         []string
	ErrorText       string // set when execution/setup failed before assertions
}

// RunSummary aggregates multiple case results.
type RunSummary struct {
	Cases        []CaseResult
	Total        int
	Passed       int
	Failed       int
	Skipped      int
	TotalElapsed time.Duration
}

// AssertionFailure mirrors a failed JS assertion.
type AssertionFailure struct {
	Name    string
	Message string
}

// Option modifies a Gruno instance at construction time.
type Option func(*runnerConfig)

// WithPreRequestHook registers a Go hook invoked before each .bru request is executed.
func WithPreRequestHook(h PreRequestHook) Option {
	return func(rc *runnerConfig) { rc.preHook = h }
}

// WithPostRequestHook registers a Go hook invoked after each .bru request finishes (tests included).
func WithPostRequestHook(h PostRequestHook) Option {
	return func(rc *runnerConfig) { rc.postHook = h }
}

// WithLogger overrides the default logger (pslog console).
func WithLogger(logger pslog.Base) Option {
	return func(rc *runnerConfig) { rc.logger = logger }
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(rc *runnerConfig) { rc.httpClient = client }
}

// WithTimeout sets the default per-request timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(rc *runnerConfig) { rc.timeout = timeout }
}
