# gru / gruno SDK

Gru is a drop-in CLI runner for Bruno `.bru` collections, plus a Go SDK (gruno) for embedding Bruno-compatible execution in Go tests or services. It tracks Bru semantics closely while adding safety (remote ref controls), richer import/test generation, programmatic hooks, and new data-driven runs.

## What’s implemented
- **SDK interface**: `gruno.New(ctx)` returns a `gruno.Gruno` interface for running single files or folders in-process.
- **Parser & executor**: Recursive-descent parser covering meta (name/seq/tags/timeout/skip/script), headers, query, path/query params, vars/vars:post-response, body types (json/xml/text/form-urlencoded/multipart-form/graphql+vars), auth none/basic/bearer, asserts, docs, tests, tag filtering, skip.
- **HTTP runner**: Env/var expansion with deterministic unresolved-var errors; context-aware HTTP; JS assertions via goja; pre/post request scripts; Go pre/post hooks; external hook commands.
- **CLI**: `cmd/gru` Cobra app with tag filters, env/var overrides, delay/bail/recursive, reporters (json/junit/html) with header masking, logging controls, TLS/proxy flags, data-driven iterations (CSV/JSON/iteration-count, optional parallel).
- **Import**: `gru import openapi|wsdl` with schema-based tests by default (parity toggle), Swagger→OAS3 upgrade, remote/file-ref policies, include-path filter, Bruno-style path params, optional strictness tiers (loose|standard|strict) for generated assertions.
- **WSDL**: import + mock fixtures covering SOAP faults, facets, attachments; MTOM multipart/related streaming supported for binary parts.
- **Parity guardrails**: Sampledata validated against official `bru` CLI; stateful mock server mirrors httpbin plus domain flows (users/shipping/finance/trace IDs) and Bruno’s GitHub mini-collection.
- **Tests**: Parser/JS/runner unit coverage, importer tests, integration checks `TestBruCLISingleFile` + `TestRunFolderSampledata`.

## Quick start (CLI)
```bash
# build CLI
go build -o bin/gru ./cmd/gru

# run sample collection (uses bundled env)
gru run sampledata --env sampledata/environments/local.bru -r

# run only Compat folder with tag include
gru run sampledata/Compat --env sampledata/environments/local.bru --tags smoke

# data-driven: CSV rows become iterations (vars available as {{col}} and bru.runner.iterationData)
gru run sampledata --csv-file-path users.csv --env sampledata/environments/local.bru --parallel

# data-driven: JSON array
gru run sampledata --json-file-path data.json --iteration-count 0

# fixed iteration count (no data file)
gru run sampledata --iteration-count 3

# env and inline vars (alias: --env-var)
gru run sampledata --env environments/local.bru --var baseUrl=https://api.test --var token=abcd

# write reporters with header redaction
gru run sampledata --env sampledata/environments/local.bru -r \
  --output report.json --format json \
  --reporter-junit report.xml --reporter-html report.html \
  --reporter-skip-headers Authorization

# TLS/proxy knobs
gru run sampledata --env sampledata/environments/local.bru \
  --insecure --cacert root.pem --ignore-truststore --noproxy --disable-cookies

# hooks (OS commands run before/after each request)
gru run sampledata --run-pre-request "./sign.sh" --run-post-request "./audit.sh"

# MTOM / multipart-related (root XML + binary attachment)
cat > cases/mtom.bru <<'EOF'
meta { name: MTOM }
post { url: {{baseUrl}}/mtom }
headers { Content-Type: multipart/related; type="application/xop+xml"; start="<rootpart>" }
body:multipart-form {
  root: <Envelope><Body>ping</Body></Envelope>;type=application/xop+xml;cid=<rootpart>
  file: @./payload.bin;type=application/octet-stream;cid=<attach1>
}
EOF
gru run cases --env environments/local.bru

# MTOM with inline text part (no file)
body:multipart-form {
  root: <Envelope><Body>ping</Body></Envelope>;type=application/xop+xml;cid=<rootpart>
  note: sample-text;type=text/plain;cid=<note1>
}
```

### Imports (OpenAPI / WSDL)
```bash
# OpenAPI → Bruno collection with generated tests (default)
gru import openapi -s api.yaml -o out/collection

# Parity-only import (no generated tests)
gru import openapi -s api.yaml -o out/collection --disable-test-generation

# Limit to select paths and allow remote refs
gru import openapi -s api.yaml -o out/collection -i /v1/users,/v1/orders --allow-remote-refs

# Stricter schema assertions (nested arrays/enums/integer finiteness)
gru import openapi -s api.yaml -o out/collection --strictness strict

# WSDL import (tests on by default; disable if you only want requests)
gru import wsdl -s service.wsdl -o out/wsdl --disable-test-generation
```

Import defaults:
- Tests generated unless `--disable-test-generation` (per-request assertions for required/type/format/range/enum/array/property-count/discriminator). `--strictness` toggles depth: loose (minimal), standard (default), strict (deep nested arrays/objects + numeric/enums).
- Remote `$ref` blocked unless `--allow-remote-refs`; file refs limited to same tree unless `--allow-file-refs`.
- Swagger 2.0 is auto-converted to OAS3; path params rendered as `:id`; include-only paths via `-i/--include-path`.

### Transport defaults & edge cases
- Root CAs: system truststore is used unless `--ignore-truststore`, in which case only `--cacert` (if provided) is trusted.
- `--cacert` appends to the system pool by default; combine with `--ignore-truststore` to pin to that CA only.
- Proxy: honours standard env vars unless `--noproxy`; `NO_PROXY` rules still apply when proxies are enabled.
- Cookies: cookie jar is enabled by default; disable with `--disable-cookies`.
- TLS client certs: JSON via `--client-cert-config` accepts `{ "cert": "...", "key": "..." }` or Bruno-style domain entries; first valid cert/key is used.

## CLI cheat sheet
- **Env/vars**: `--env <file>` (relative names resolve to `environments/<name>.bru`), inline overrides via `--var key=value` or `--env-var`.
- **Filtering**: `--tags`, `--exclude-tags`, `--tests-only`.
- **Flow control**: `--delay`, `--bail`, `-r/--recursive`, per-request `--timeout`.
- **Data-driven**: `--csv-file-path`, `--json-file-path`, `--iteration-count` (default 1), `--parallel` (runs cases per iteration concurrently).
- **Hooks**: `--run-pre-request <cmd>` / `--run-post-request <cmd>`; non-zero exit aborts the run (stdout/stderr streamed).
- **Logging**: `--structured` JSON logs; `--log-level trace|debug|info|warn|error` (defaults to info; honours LOG_LEVEL when flag unset); `--log-caller`.
- **TLS/transport**: `--insecure`, `--cacert`, `--ignore-truststore`, `--client-cert-config`, `--noproxy`, `--disable-cookies`.
- **Reporters**: `-o/--output` with `-f/--format json|junit|html` or explicit `--reporter-json|junit|html`; `--reporter-skip-headers` or `--reporter-skip-all-headers` to strip/mask.

## Go SDK usage

### Minimal run
```go
ctx := context.Background()
g, _ := gruno.New(ctx)
sum, _ := g.RunFolder(ctx, "sampledata", gruno.RunOptions{
    EnvPath: "sampledata/environments/local.bru",
    Vars:    map[string]string{"HELLO": "world"},
})
log.Printf("passed=%d failed=%d", sum.Passed, sum.Failed)
```

### With Go hooks and custom HTTP client
```go
httpClient := &http.Client{Timeout: 5 * time.Second}

g, _ := gruno.New(ctx,
    gruno.WithHTTPClient(httpClient),
    gruno.WithPreRequestHook(func(ctx context.Context, info gruno.HookInfo, req *http.Request, logger pslog.Base) error {
        req.Header.Set("X-Signature", sign(req))
        return nil
    }),
    gruno.WithPostRequestHook(func(ctx context.Context, info gruno.HookInfo, res gruno.CaseResult, logger pslog.Base) error {
        if !res.Passed {
            logger.Warn("case failed", "file", info.FilePath, "err", res.ErrorText)
        }
        return nil
    }),
)

res, _ := g.RunFile(ctx, "sampledata/Users/get_user.bru", gruno.RunOptions{
    EnvPath: "sampledata/environments/local.bru",
    Timeout: 10 * time.Second,
})
```

### Data-driven from SDK
```go
sum, _ := g.RunFolder(ctx, "sampledata", gruno.RunOptions{
    EnvPath:        "sampledata/environments/local.bru",
    CSVFilePath:    "users.csv",      // or JSONFilePath
    Parallel:       true,             // optional
    IterationCount: 0,                // ignored when CSV/JSON present
})
```

### MTOM / multipart from SDK
```go
g, _ := gruno.New(ctx)
bru := `meta { name: MTOM }
post { url: {{baseUrl}}/mtom }
headers { Content-Type: multipart/related; type="application/xop+xml"; start="<rootpart>" }
body:multipart-form {
  root: <Envelope><Body>ping</Body></Envelope>;type=application/xop+xml;cid=<rootpart>
  file: @./payload.bin;type=application/octet-stream;cid=<attach1>
}
tests {
  test("mtom parts", function() {
    expect(res.status).to.equal(200);
  });
}`
_ = os.WriteFile("cases/mtom.bru", []byte(bru), 0o644)
sum, _ := g.RunFile(ctx, "cases/mtom.bru", gruno.RunOptions{
    EnvPath: "environments/local.bru",
})
```

Iteration metadata is available to JS via `bru.runner.iterationIndex`, `bru.runner.totalIterations`, `bru.runner.iterationData.get("field")`, and `bru.getVar("field")`; pre-request scripts can use the same helpers.

### Import helpers (programmatic)
```go
_ = gruno.ImportOpenAPI(ctx, gruno.ImportOptions{
    Source:         "api.yaml",
    OutputDir:      "out/collection",
    CollectionName: "My API",
    GroupBy:        "tags",           // or "path"
    DisableTests:   false,            // default
    AllowRemoteRefs:false,            // default
})
```

## Samples
- `sampledata/` contains compatibility suites plus Bruno’s mini “GitHub” collection under `sampledata/GitHub/`.
- `sampledata/environments/local.bru` seeds variables; adjust `baseUrl` when running against your own server.

## Dependencies
- Go 1.21+
- Bruno CLI in PATH only for parity tests.
