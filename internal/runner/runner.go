package runner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"pkt.systems/gruno/internal/parser"
	"pkt.systems/pslog"
)

const defaultTimeout = 15 * time.Second

// runner implements Gruno.
type runner struct {
	logger     pslog.Base
	httpClient *http.Client
	timeout    time.Duration
	preHook    PreRequestHook
	postHook   PostRequestHook
}

type runnerConfig struct {
	logger     pslog.Base
	httpClient *http.Client
	timeout    time.Duration
	preHook    PreRequestHook
	postHook   PostRequestHook
}

// New constructs a Gruno instance with optional configuration.
func New(ctx context.Context, opts ...Option) (Gruno, error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	cfg := runnerConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.logger == nil {
		cfg.logger = pslog.New(os.Stdout)
	}
	if cfg.httpClient == nil {
		cfg.httpClient = &http.Client{Timeout: defaultTimeout}
	}
	if cfg.timeout == 0 {
		cfg.timeout = defaultTimeout
	}
	r := &runner{
		logger:     cfg.logger,
		httpClient: cfg.httpClient,
		timeout:    cfg.timeout,
		preHook:    cfg.preHook,
		postHook:   cfg.postHook,
	}
	return r, nil
}

// RunFile executes a single .bru file.
func (r *runner) RunFile(ctx context.Context, path string, opts RunOptions) (CaseResult, error) {
	return r.runSingle(ctx, path, opts)
}

// RunFolder discovers, sorts, and executes all .bru files in the folder.
func (r *runner) RunFolder(ctx context.Context, path string, opts RunOptions) (RunSummary, error) {
	start := time.Now()

	recursive := true
	if opts.RecursiveSet {
		recursive = opts.Recursive
	}

	files, err := parser.DiscoverBruFiles(path, recursive)
	if err != nil {
		return RunSummary{}, err
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Meta.Seq == files[j].Meta.Seq {
			return files[i].FilePath < files[j].FilePath
		}
		return files[i].Meta.Seq < files[j].Meta.Seq
	})

	envVars, err := loadEnv(ctx, opts.EnvPath)
	if err != nil {
		return RunSummary{}, fmt.Errorf("load env: %w", err)
	}
	if len(opts.Vars) > 0 {
		if envVars == nil {
			envVars = map[string]string{}
		}
		maps.Copy(envVars, opts.Vars)
	}

	iterations, err := buildIterations(opts)
	if err != nil {
		return RunSummary{}, err
	}
	if len(iterations) == 0 {
		iterations = []iterationSpec{{vars: map[string]string{}, data: map[string]any{}}}
	}

	// Filter upfront by tag include/exclude to match bru behaviour (requests count only executed)
	var runnable []parser.ParsedFile
	for _, f := range files {
		if passesTagFilter(f.Meta.Tags, opts.Tags, opts.ExcludeTags) {
			runnable = append(runnable, f)
		}
	}

	totalIterations := len(iterations)
	summary := RunSummary{Total: len(runnable) * totalIterations}
	caseCount := 0

	for iterIdx, iter := range iterations {
		// start with env/vars fresh for each iteration so post-response vars do not leak.
		iterVars := cloneStringMap(envVars)
		maps.Copy(iterVars, iter.vars)

		// run in parallel if requested; requests are independent in this mode.
		if opts.Parallel {
			ctxIter, cancel := context.WithCancel(ctx)
			type resOut struct {
				res CaseResult
				err error
			}
			outCh := make(chan resOut, len(runnable))
			for _, f := range runnable {
				caseOpts := opts
				caseOpts.Vars = cloneStringMap(iterVars)
				caseOpts.IterationIndex = iterIdx
				caseOpts.TotalIterations = totalIterations
				caseOpts.IterationData = cloneAnyMap(iter.data)
				go func(pf parser.ParsedFile, co RunOptions) {
					rres, rerr := r.executeParsed(ctxIter, pf, co)
					outCh <- resOut{res: rres, err: rerr}
				}(f, caseOpts)
			}
			bailTriggered := false
			var iterErr error
			for i := 0; i < len(runnable); i++ {
				out := <-outCh
				if out.err != nil && iterErr == nil {
					iterErr = out.err
					cancel()
				}
				if iterErr != nil {
					continue
				}
				summary.Cases = append(summary.Cases, out.res)
				if out.res.Skipped {
					summary.Skipped++
				} else if out.res.Passed {
					summary.Passed++
				} else {
					summary.Failed++
					bailTriggered = bailTriggered || opts.Bail
				}
				caseCount++
			}
			if iterErr != nil {
				cancel()
				return RunSummary{}, iterErr
			}
			if bailTriggered {
				summary.TotalElapsed = time.Since(start)
				cancel()
				return summary, nil
			}
			cancel()
			continue
		}

		// sequential mode: preserve var mutations within the iteration.
		for _, f := range runnable {
			// delay between cases (global + per-meta)
			delay := opts.Delay
			if f.Meta.DelayMS > 0 {
				delay += time.Duration(f.Meta.DelayMS) * time.Millisecond
			}
			if delay > 0 && caseCount > 0 {
				time.Sleep(delay)
			}

			caseOpts := opts
			caseOpts.Vars = iterVars
			caseOpts.IterationIndex = iterIdx
			caseOpts.TotalIterations = totalIterations
			caseOpts.IterationData = iter.data
			res, err := r.executeParsed(ctx, f, caseOpts)
			if err != nil {
				return RunSummary{}, err
			}
			summary.Cases = append(summary.Cases, res)
			if res.Skipped {
				summary.Skipped++
			} else if res.Passed {
				summary.Passed++
			} else {
				summary.Failed++
			}
			caseCount++
			if opts.Bail && !res.Passed && !res.Skipped {
				summary.TotalElapsed = time.Since(start)
				return summary, nil
			}
		}
	}
	summary.TotalElapsed = time.Since(start)
	return summary, nil
}

func (r *runner) runSingle(ctx context.Context, path string, opts RunOptions) (CaseResult, error) {
	parsed, err := parser.ParseFile(ctx, path)
	if err != nil {
		return CaseResult{}, err
	}
	envVars, err := loadEnv(ctx, opts.EnvPath)
	if err != nil {
		return CaseResult{}, fmt.Errorf("load env: %w", err)
	}
	if len(opts.Vars) > 0 {
		if envVars == nil {
			envVars = map[string]string{}
		}
		maps.Copy(envVars, opts.Vars)
	}

	iterations, err := buildIterations(opts)
	if err != nil {
		return CaseResult{}, err
	}
	if len(iterations) == 0 {
		iterations = []iterationSpec{{vars: map[string]string{}, data: map[string]any{}}}
	}

	var last CaseResult
	for iterIdx, iter := range iterations {
		iterVars := cloneStringMap(envVars)
		maps.Copy(iterVars, iter.vars)
		caseOpts := RunOptions{
			Vars:            iterVars,
			Tags:            opts.Tags,
			ExcludeTags:     opts.ExcludeTags,
			HTTPClient:      opts.HTTPClient,
			Logger:          opts.Logger,
			Timeout:         opts.Timeout,
			TestsOnly:       opts.TestsOnly,
			Delay:           opts.Delay,
			PreHookCmd:      opts.PreHookCmd,
			PostHookCmd:     opts.PostHookCmd,
			IterationIndex:  iterIdx,
			TotalIterations: len(iterations),
			IterationData:   iter.data,
		}
		res, err := r.executeParsed(ctx, parsed, caseOpts)
		if err != nil {
			return CaseResult{}, err
		}
		last = res
		if !res.Passed && !res.Skipped && opts.Bail {
			return res, nil
		}
		// Delay between iterations, similar to folder sequencing.
		if iterIdx < len(iterations)-1 && opts.Delay > 0 {
			time.Sleep(opts.Delay)
		}
	}
	return last, nil
}

func (r *runner) executeParsed(ctx context.Context, parsed parser.ParsedFile, opts RunOptions) (CaseResult, error) {
	logger := r.logger
	if opts.Logger != nil {
		logger = opts.Logger
	}
	client := r.httpClient
	if opts.HTTPClient != nil {
		client = opts.HTTPClient
	}
	timeout := r.timeout
	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}
	if parsed.Meta.TimeoutMS > 0 {
		timeout = time.Duration(parsed.Meta.TimeoutMS) * time.Millisecond
	}

	if !passesTagFilter(parsed.Meta.Tags, opts.Tags, opts.ExcludeTags) {
		return CaseResult{FilePath: parsed.FilePath, Name: parsed.Meta.Name, Seq: parsed.Meta.Seq, Tags: parsed.Meta.Tags, Passed: true, Skipped: true}, nil
	}

	if parsed.Meta.Skip {
		return CaseResult{FilePath: parsed.FilePath, Name: parsed.Meta.Name, Seq: parsed.Meta.Seq, Tags: parsed.Meta.Tags, Passed: true, Skipped: true}, nil
	}
	if opts.TestsOnly && parsed.TestsRaw == "" && len(parsed.Assert) == 0 {
		return CaseResult{FilePath: parsed.FilePath, Name: parsed.Meta.Name, Seq: parsed.Meta.Seq, Tags: parsed.Meta.Tags, Passed: true, Skipped: true}, nil
	}

	expander := newExpander(opts.Vars)
	iterInfo := iterationInfo{
		index: opts.IterationIndex,
		total: opts.TotalIterations,
		data:  opts.IterationData,
		exp:   expander,
	}
	// apply pre-request vars
	if len(parsed.VarsPre) > 0 {
		maps.Copy(expander.vars, parsed.VarsPre)
	}

	var prelude string
	if parsed.Meta.Settings.Script != "" {
		scriptPath := parsed.Meta.Settings.Script
		// if env path provided, resolve relative to its dir; else relative to file dir
		if opts.EnvPath != "" && !filepath.IsAbs(scriptPath) {
			scriptPath = filepath.Join(filepath.Dir(opts.EnvPath), scriptPath)
		} else if !filepath.IsAbs(scriptPath) {
			scriptPath = filepath.Join(filepath.Dir(parsed.FilePath), scriptPath)
		}
		if b, err := os.ReadFile(scriptPath); err == nil {
			prelude = string(b)
		} else {
			return CaseResult{}, fmt.Errorf("load prelude %s: %w", scriptPath, err)
		}
	}

	repeat := parsed.Meta.Repeat
	if repeat <= 0 {
		repeat = 1
	}

	var result CaseResult
	for i := 0; i < repeat; i++ {
		if parsed.Meta.DelayMS > 0 {
			select {
			case <-ctx.Done():
				return CaseResult{}, ctx.Err()
			case <-time.After(time.Duration(parsed.Meta.DelayMS) * time.Millisecond):
			}
		}

		req, err := buildHTTPRequest(parsed, expander)
		if err != nil {
			return CaseResult{}, err
		}
		result.RequestURL = req.URL.String()

		// Go pre-request hook (runs before JS pre-request)
		if r.preHook != nil {
			if err := r.preHook(ctx, hookInfoFromParsed(parsed, req), req, logger); err != nil {
				return CaseResult{}, err
			}
		}
		if len(opts.PreHookCmd) > 0 {
			if err := r.runExternalHook(ctx, "pre", opts.PreHookCmd, parsed, nil); err != nil {
				return CaseResult{}, err
			}
		}

		// run JS pre-request script to allow header/query/body tweaks
		if parsed.Scripts.PreRequest != "" {
			if err := runPreRequestScript(parsed.Scripts.PreRequest, req, expander, iterInfo); err != nil {
				return CaseResult{}, fmt.Errorf("pre script: %w", err)
			}
		}

		reqHeaders := headerMap(req.Header)

		if timeout <= 0 {
			timeout = defaultTimeout
		}
		ctxTimeout, cancel := context.WithTimeout(ctx, timeout)

		start := time.Now()
		resp, err := client.Do(req.WithContext(ctxTimeout))
		duration := time.Since(start)
		cancel()
		if err != nil {
			// Surface connection/refused/etc as a case-level failure instead of aborting the run.
			return CaseResult{
				FilePath:   parsed.FilePath,
				Name:       parsed.Meta.Name,
				RequestURL: req.URL.String(),
				Seq:        parsed.Meta.Seq,
				Tags:       parsed.Meta.Tags,
				Duration:   duration,
				Passed:     false,
				ErrorText:  fmt.Sprintf("http request failed: %v", err),
			}, nil
		}
		defer resp.Body.Close()

		// post-response script and assertions
		result, err = executeTests(ctx, parsed, resp, duration, expander, logger, prelude, iterInfo)
		if err != nil {
			result.Passed = false
			result.ErrorText = err.Error()
		}
		result.Status = resp.StatusCode
		result.RequestHeaders = reqHeaders
		result.ResponseHeaders = headerMap(resp.Header)

		// vars:post-response merge back
		if len(parsed.VarsPost) > 0 {
			if opts.Vars == nil {
				opts.Vars = map[string]string{}
			}
			for k, v := range parsed.VarsPost {
				expander.vars[k] = v
				opts.Vars[k] = v
			}
		}

		result.FilePath = parsed.FilePath
		result.Name = parsed.Meta.Name
		result.RequestURL = req.URL.String()
		result.Seq = parsed.Meta.Seq
		result.Tags = parsed.Meta.Tags
		result.Duration = duration

		if r.postHook != nil {
			if err := r.postHook(ctx, hookInfoFromParsed(parsed, req), result, logger); err != nil {
				return CaseResult{}, err
			}
		}
		if len(opts.PostHookCmd) > 0 {
			if err := r.runExternalHook(ctx, "post", opts.PostHookCmd, parsed, &result); err != nil {
				return CaseResult{}, err
			}
		}

		if !result.Passed {
			break
		}
	}

	return result, nil
}

func passesTagFilter(tags []string, include []string, exclude []string) bool {
	if len(include) > 0 {
		match := false
		for _, t := range tags {
			if slices.Contains(include, t) {
				match = true
			}
		}
		if !match {
			return false
		}
	}
	for _, t := range tags {
		if slices.Contains(exclude, t) {
			return false
		}
	}
	return true
}

func (r *runner) runExternalHook(ctx context.Context, phase string, cmd []string, file parser.ParsedFile, res *CaseResult) error {
	if len(cmd) == 0 {
		return nil
	}

	command := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	command.Env = append(os.Environ(), hookEnv(phase, file, res)...)

	stdout, _ := command.StdoutPipe()
	stderr, _ := command.StderrPipe()

	if err := command.Start(); err != nil {
		return fmt.Errorf("%s-hook start: %w", phase, err)
	}

	var wg sync.WaitGroup
	logStream := func(stream string, rdr io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(rdr)
		for scanner.Scan() {
			line := scanner.Text()
			if r.logger != nil {
				r.logger.Info("hook", "phase", phase, "cmd", cmd[0], "stream", stream, "msg", line)
			}
			fmt.Fprintln(os.Stdout, line)
			fmt.Fprintf(os.Stdout, "{\"msg\":\"hook\",\"phase\":\"%s\",\"stream\":\"%s\",\"line\":%q}\n", phase, stream, line)
		}
	}
	if stdout != nil {
		wg.Add(1)
		go logStream("stdout", stdout)
	}
	if stderr != nil {
		wg.Add(1)
		go logStream("stderr", stderr)
	}

	if err := command.Wait(); err != nil {
		wg.Wait()
		return fmt.Errorf("%s-hook failed: %w", phase, err)
	}
	wg.Wait()
	return nil
}

func hookInfoFromParsed(parsed parser.ParsedFile, req *http.Request) HookInfo {
	method := strings.ToUpper(parsed.Request.Verb)
	url := parsed.Request.URL
	name := parsed.Meta.Name
	if name == "" {
		name = filepath.Base(parsed.FilePath)
	}
	if req != nil {
		if req.Method != "" {
			method = req.Method
		}
		if req.URL != nil {
			url = req.URL.String()
		}
	}
	return HookInfo{
		Name:     name,
		FilePath: parsed.FilePath,
		Seq:      parsed.Meta.Seq,
		Tags:     parsed.Meta.Tags,
		Method:   method,
		URL:      url,
	}
}

func hookEnv(phase string, file parser.ParsedFile, res *CaseResult) []string {
	vals := []string{
		"GRU_HOOK_PHASE=" + phase,
		"GRU_FILE=" + file.FilePath,
		"GRU_NAME=" + file.Meta.Name,
		fmt.Sprintf("GRU_SEQ=%f", file.Meta.Seq),
		"GRU_METHOD=" + strings.ToUpper(file.Request.Verb),
		"GRU_URL=" + file.Request.URL,
		"GRU_TAGS=" + strings.Join(file.Meta.Tags, ","),
	}
	if res != nil {
		vals = append(vals,
			fmt.Sprintf("GRU_STATUS=%d", res.Status),
			fmt.Sprintf("GRU_PASSED=%v", res.Passed),
			fmt.Sprintf("GRU_FAILED_COUNT=%d", len(res.Failures)),
			fmt.Sprintf("GRU_DURATION_MS=%d", res.Duration.Milliseconds()),
		)
	}
	return vals
}

// runPreRequestScript executes Bruno-style pre-request JS that can mutate headers/query/body.
func runPreRequestScript(code string, req *http.Request, exp *expander, iter iterationInfo) error {
	if strings.TrimSpace(code) == "" {
		return nil
	}
	vm := goja.New()
	// Build req object
	reqObj := vm.NewObject()
	hdrObj := vm.NewObject()
	for k, vals := range req.Header {
		if len(vals) > 0 {
			hdrObj.Set(strings.ToLower(k), vals[0])
		}
	}
	reqObj.Set("headers", hdrObj)
	reqObj.Set("url", req.URL.String())
	reqObj.Set("setHeader", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		name := strings.ToLower(call.Arguments[0].String())
		val := ""
		if len(call.Arguments) > 1 {
			val = call.Arguments[1].String()
		}
		_ = hdrObj.Set(name, val)
		return goja.Undefined()
	})
	vm.Set("req", reqObj)

	registerEnv(vm, exp)
	registerProcessEnv(vm, exp)
	registerBru(vm, exp, iter)

	if _, err := vm.RunString(code); err != nil {
		return err
	}

	// apply header changes back
	if h := reqObj.Get("headers"); h != nil {
		if obj, ok := h.(*goja.Object); ok {
			for _, k := range obj.Keys() {
				val := obj.Get(k).String()
				req.Header.Set(k, val)
			}
		}
	}
	// apply possible url change
	if uVal := reqObj.Get("url"); uVal != nil {
		if uStr := uVal.String(); uStr != "" {
			if newURL, err := http.NewRequest(req.Method, uStr, req.Body); err == nil {
				req.URL = newURL.URL
			}
		}
	}
	return nil
}

func headerMap(h http.Header) map[string]string {
	if h == nil {
		return nil
	}
	out := map[string]string{}
	for k, vals := range h {
		if len(vals) > 0 {
			out[strings.ToLower(k)] = vals[0]
		} else {
			out[strings.ToLower(k)] = ""
		}
	}
	return out
}
