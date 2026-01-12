package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"pkt.systems/gruno"
	"pkt.systems/pslog"
)

func newRunCmd() *cobra.Command {
	runCmd := &cobra.Command{
		Use:   "run [folder|file]",
		Short: "Execute .bru files",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runE,
	}

	addLoggingFlags(runCmd.Flags())
	runCmd.Flags().String("env", "", "Path to environment .bru file")
	runCmd.Flags().StringArray("var", nil, "Override variable (key=value)")
	runCmd.Flags().StringArray("env-var", nil, "Override environment variable (alias for --var)")
	runCmd.Flags().StringSlice("tags", nil, "Only run cases with these tags")
	runCmd.Flags().StringSlice("exclude-tags", nil, "Skip cases with these tags")
	runCmd.Flags().Bool("tests-only", false, "Only run cases that define tests or asserts")
	runCmd.Flags().Int("delay", 0, "Delay between requests (ms)")
	runCmd.Flags().Bool("bail", false, "Stop after first failure")
	runCmd.Flags().BoolP("recursive", "r", false, "Recurse into subfolders (Bru default: false)")
	runCmd.Flags().Int("timeout", 15, "Per-request timeout seconds")
	runCmd.Flags().StringP("output", "o", "", "Write summary to file (see --format)")
	runCmd.Flags().StringP("format", "f", "json", "Output format: json|junit|html")
	runCmd.Flags().String("reporter-json", "", "Write JSON report to path")
	runCmd.Flags().String("reporter-junit", "", "Write JUnit XML report to path")
	runCmd.Flags().String("reporter-html", "", "Write HTML report to path")
	runCmd.Flags().String("csv-file-path", "", "Path to CSV dataset for data-driven iterations")
	runCmd.Flags().String("json-file-path", "", "Path to JSON dataset for data-driven iterations")
	runCmd.Flags().Int("iteration-count", 0, "Execute collection this many times (default 1)")
	runCmd.Flags().Bool("parallel", false, "Run requests in parallel")
	runCmd.Flags().Bool("reporter-skip-all-headers", false, "Omit headers from reporter outputs")
	runCmd.Flags().StringSlice("reporter-skip-headers", nil, "Skip specific headers (case-insensitive) from reporter outputs")
	runCmd.Flags().Bool("insecure", false, "Skip TLS verification")
	runCmd.Flags().String("cacert", "", "Path to custom CA certificate (PEM)")
	runCmd.Flags().Bool("ignore-truststore", false, "Use only the provided CA certificate")
	runCmd.Flags().String("client-cert-config", "", "Path to client certificate config JSON {\"cert\":\"\",\"key\":\"\"}")
	runCmd.Flags().Bool("noproxy", false, "Disable proxy (ignore environment)")
	runCmd.Flags().Bool("disable-cookies", false, "Do not store/send cookies between requests")
	runCmd.Flags().String("run-pre-request", "", "Executable (with args) to run before each request")
	runCmd.Flags().String("run-post-request", "", "Executable (with args) to run after each request")

	return runCmd
}

func newLogger(structured bool, level string, flagSet bool, caller bool, w io.Writer) (pslog.Logger, error) {
	if w == nil {
		w = os.Stdout
	}

	var logger pslog.Logger
	opts := pslog.Options{CallerKeyval: caller}
	if structured {
		opts.Mode = pslog.ModeStructured
	}
	logger = pslog.NewWithOptions(w, opts)

	// Default to info to match Bru.
	logger = logger.LogLevel(pslog.InfoLevel)

	if flagSet {
		if lvl, ok := pslog.ParseLevel(level); ok {
			return logger.LogLevel(lvl), nil
		}
		return nil, fmt.Errorf("unknown level %q", level)
	}

	if lvl, ok := pslog.LevelFromEnv("LOG_LEVEL"); ok {
		return logger.LogLevel(lvl), nil
	}
	if lvl, ok := pslog.ParseLevel(level); ok {
		return logger.LogLevel(lvl), nil
	}
	return logger, nil
}

func runE(cmd *cobra.Command, args []string) error {
	target := "."
	if len(args) > 0 {
		target = args[0]
	}
	envPath, _ := cmd.Flags().GetString("env")
	varsList, _ := cmd.Flags().GetStringArray("var")
	envVarList, _ := cmd.Flags().GetStringArray("env-var")
	tags, _ := cmd.Flags().GetStringSlice("tags")
	exclude, _ := cmd.Flags().GetStringSlice("exclude-tags")
	testsOnly, _ := cmd.Flags().GetBool("tests-only")
	delayMS, _ := cmd.Flags().GetInt("delay")
	bail, _ := cmd.Flags().GetBool("bail")
	recursive, _ := cmd.Flags().GetBool("recursive")
	csvPath, _ := cmd.Flags().GetString("csv-file-path")
	jsonPath, _ := cmd.Flags().GetString("json-file-path")
	iterCount, _ := cmd.Flags().GetInt("iteration-count")
	parallel, _ := cmd.Flags().GetBool("parallel")
	output, _ := cmd.Flags().GetString("output")
	format, _ := cmd.Flags().GetString("format")
	reportJSON, _ := cmd.Flags().GetString("reporter-json")
	reportJUnit, _ := cmd.Flags().GetString("reporter-junit")
	reportHTML, _ := cmd.Flags().GetString("reporter-html")
	reportSkipAll, _ := cmd.Flags().GetBool("reporter-skip-all-headers")
	reportSkip, _ := cmd.Flags().GetStringSlice("reporter-skip-headers")
	timeoutSec, _ := cmd.Flags().GetInt("timeout")
	insecure, _ := cmd.Flags().GetBool("insecure")
	cacert, _ := cmd.Flags().GetString("cacert")
	ignoreTS, _ := cmd.Flags().GetBool("ignore-truststore")
	clientCertPath, _ := cmd.Flags().GetString("client-cert-config")
	noProxy, _ := cmd.Flags().GetBool("noproxy")
	disableCookies, _ := cmd.Flags().GetBool("disable-cookies")
	preHookCmd, _ := cmd.Flags().GetString("run-pre-request")
	postHookCmd, _ := cmd.Flags().GetString("run-post-request")

	logger := loggerFromCmd(cmd)

	if csvPath != "" && jsonPath != "" {
		logger.Fatal("choose either --csv-file-path or --json-file-path")
		return nil
	}
	if iterCount < 0 {
		logger.Fatal("iteration-count must be >= 0", "value", iterCount)
		return nil
	}

	// Bru-style env resolution: --env local resolves to environments/local.bru
	if envPath != "" {
		if !strings.Contains(envPath, string(os.PathSeparator)) && !strings.HasSuffix(envPath, ".bru") {
			envPath = filepath.Join("environments", envPath+".bru")
		}
		if _, err := os.Stat(envPath); err != nil {
			logger.Fatal("env file not found", "path", envPath, "err", err)
			return nil
		}
	}

	// Build HTTP client based on flags
	httpClient, err := buildHTTPClient(insecure, cacert, ignoreTS, clientCertPath, noProxy, disableCookies)
	if err != nil {
		logger.Fatal("http client", "err", err)
		return nil
	}

	g, err := gruno.New(context.Background(), gruno.WithLogger(logger), gruno.WithHTTPClient(httpClient), gruno.WithTimeout(time.Duration(timeoutSec)*time.Second))
	if err != nil {
		logger.Fatal("init", "err", err)
		return nil
	}

	vars := map[string]string{}
	for _, kv := range append(varsList, envVarList...) {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			logger.Fatal("invalid --var", "value", kv)
			return nil
		}
		vars[parts[0]] = parts[1]
	}

	opts := gruno.RunOptions{
		EnvPath:                envPath,
		Vars:                   vars,
		Tags:                   tags,
		ExcludeTags:            exclude,
		TestsOnly:              testsOnly,
		Bail:                   bail,
		CSVFilePath:            csvPath,
		JSONFilePath:           jsonPath,
		IterationCount:         iterCount,
		Parallel:               parallel,
		Delay:                  time.Duration(delayMS) * time.Millisecond,
		OutputPath:             output,
		OutputFormat:           format,
		ReporterJSON:           reportJSON,
		ReporterJUnit:          reportJUnit,
		ReporterHTML:           reportHTML,
		ReporterSkipAllHeaders: reportSkipAll,
		ReporterSkipHeaders:    reportSkip,
		Recursive:              recursive,
		RecursiveSet:           true,
		PreHookCmd:             splitCmd(preHookCmd),
		PostHookCmd:            splitCmd(postHookCmd),
	}
	if timeoutSec > 0 {
		opts.Timeout = time.Duration(timeoutSec) * time.Second
	}
	// timeout already set via WithTimeout for default

	info, err := os.Stat(target)
	if err != nil {
		logger.Fatal("stat", "path", target, "err", err)
		return nil
	}
	if info.IsDir() || csvPath != "" || jsonPath != "" || iterCount > 1 || parallel {
		summary, err := g.RunFolder(cmd.Context(), target, opts)
		if err != nil {
			logger.Fatal("run", "err", err)
			return nil
		}
		if err := writeOutputs(opts, summary, logger); err != nil {
			logger.Fatal("report", "err", err)
			return nil
		}
		printSummary(summary, logger)
		if summary.Failed > 0 {
			logger.Fatal("cases failed", "count", summary.Failed)
		}
		return nil
	}
	res, err := g.RunFile(cmd.Context(), target, opts)
	if err != nil {
		logger.Fatal("run", "err", err)
		return nil
	}
	printSingle(res, logger)
	summary := gruno.RunSummary{
		Cases:        []gruno.CaseResult{res},
		Total:        1,
		Passed:       boolToInt(res.Passed),
		Failed:       boolToInt(!res.Passed && !res.Skipped),
		Skipped:      boolToInt(res.Skipped),
		TotalElapsed: res.Duration,
	}
	if err := writeOutputs(opts, summary, logger); err != nil {
		logger.Fatal("report", "err", err)
		return nil
	}
	if !res.Passed {
		logger.Fatal("case failed", "file", res.FilePath)
	}
	return nil
}

func buildHTTPClient(insecure bool, cacert string, ignoreTS bool, clientCertPath string, noProxy bool, disableCookies bool) (*http.Client, error) {
	tlsConfig := &tls.Config{InsecureSkipVerify: insecure} //nolint:gosec // user opted in

	if cacert != "" {
		pemData, err := os.ReadFile(cacert)
		if err != nil {
			return nil, fmt.Errorf("read cacert: %w", err)
		}
		var pool *x509.CertPool
		if ignoreTS {
			pool = x509.NewCertPool()
		} else {
			pool, err = x509.SystemCertPool()
			if err != nil {
				pool = x509.NewCertPool()
			}
		}
		if ok := pool.AppendCertsFromPEM(pemData); !ok {
			return nil, fmt.Errorf("failed to append CA cert")
		}
		tlsConfig.RootCAs = pool
	}

	if clientCertPath != "" {
		cfgBytes, err := os.ReadFile(clientCertPath)
		if err != nil {
			return nil, fmt.Errorf("read client-cert-config: %w", err)
		}
		certPath, keyPath, err := parseClientCertConfig(clientCertPath, cfgBytes)
		if err != nil {
			return nil, err
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("load client cert: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	if !noProxy {
		tr.Proxy = http.ProxyFromEnvironment
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   15 * time.Second,
	}
	if !disableCookies {
		if jar, err := cookiejar.New(nil); err == nil {
			client.Jar = jar
		}
	}
	return client, nil
}

func parseClientCertConfig(configPath string, raw []byte) (certPath, keyPath string, err error) {
	type simple struct {
		Cert string `json:"cert"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal(raw, &simple{}); err == nil {
		var cfg simple
		_ = json.Unmarshal(raw, &cfg)
		if cfg.Cert != "" && cfg.Key != "" {
			return resolveRelative(configPath, cfg.Cert), resolveRelative(configPath, cfg.Key), nil
		}
	}
	// Bru-style config: {enabled, certs:[{domain,type,certFilePath,keyFilePath,pfxFilePath,passphrase}]}
	var bru struct {
		Enabled bool `json:"enabled"`
		Certs   []struct {
			Domain       string `json:"domain"`
			Type         string `json:"type"`
			CertFilePath string `json:"certFilePath"`
			KeyFilePath  string `json:"keyFilePath"`
			PFXFilePath  string `json:"pfxFilePath"`
		} `json:"certs"`
	}
	if err := json.Unmarshal(raw, &bru); err == nil {
		for _, c := range bru.Certs {
			ctype := strings.ToLower(c.Type)
			if ctype == "" || ctype == "cert" {
				if c.CertFilePath == "" || c.KeyFilePath == "" {
					continue
				}
				return resolveRelative(configPath, c.CertFilePath), resolveRelative(configPath, c.KeyFilePath), nil
			}
		}
	}
	return "", "", fmt.Errorf("client-cert-config requires cert/key")
}

func resolveRelative(cfgPath, target string) string {
	if filepath.IsAbs(target) {
		return target
	}
	return filepath.Join(filepath.Dir(cfgPath), target)
}

func splitCmd(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func printSummary(sum gruno.RunSummary, logger pslog.Base) {
	for _, c := range sum.Cases {
		printSingle(c, logger)
	}
	logger.Info("summary", "total", sum.Total, "passed", sum.Passed, "failed", sum.Failed, "elapsed", sum.TotalElapsed.String())
}

func printSingle(res gruno.CaseResult, logger pslog.Base) {
	if res.Skipped {
		logger.Info("skip", "name", res.Name, "file", res.FilePath)
		return
	}
	if res.Passed {
		logger.Info("pass", "name", res.Name, "file", res.FilePath, "dur", res.Duration.String())
		return
	}
	logger.Error("fail", "name", res.Name, "file", res.FilePath, "dur", res.Duration.String(), "err", res.ErrorText)
	for _, f := range res.Failures {
		logger.Error("assert", "name", f.Name, "msg", f.Message)
	}
	for _, line := range res.Console {
		logger.Debug("console", "msg", line)
	}
}

func writeOutputs(opts gruno.RunOptions, sum gruno.RunSummary, logger pslog.Base) error {
	sum = gruno.FilterReportHeaders(sum, opts)
	if opts.OutputPath != "" {
		if err := gruno.WriteReport(opts.OutputFormat, opts.OutputPath, sum); err != nil {
			return err
		}
	}
	if opts.ReporterJSON != "" {
		if err := gruno.WriteReportJSON(opts.ReporterJSON, sum); err != nil {
			return err
		}
	}
	if opts.ReporterJUnit != "" {
		if err := gruno.WriteReportJUnit(opts.ReporterJUnit, sum); err != nil {
			return err
		}
	}
	if opts.ReporterHTML != "" {
		if err := gruno.WriteReportHTML(opts.ReporterHTML, sum); err != nil {
			return err
		}
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
