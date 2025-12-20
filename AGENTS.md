# AGENTS

Context & interfaces
- `gruno.New(ctx)` returns the public `Gruno` interface; keep concrete types unexported.
- Propagate `context.Context` through every call; avoid package-level state so multiple runners can coexist.
- Public methods return max two values (payload + error). For extras, define small structs.

Logging
- Accept `pslog.Base` in the SDK; CLI defaults to `pslog.Logger` console colors, optional structured.

Parsing / execution rules
- Parser is tokenized recursive-descent; prefer small, explicit branches over regex-heavy parsing.
- Supported blocks: meta (name, seq, tags, timeout, skip, script settings), headers, query, params:path, params:query, vars/vars:post-response, body types (json, xml, text, form-urlencoded, multipart-form, graphql + vars), auth none/basic/bearer, asserts, docs, tests, skip/tags.
- Tag filtering: include/exclude respected before execution; aligns with Bru semantics.
- Keep env/var expansion deterministic; error on unresolved `{{var}}` in URL before sending.

Sampledata & parity
- Bruno CLI is the judge: any new sampledata must pass `bru run` before accepting.
- Mock server lives in tests; extend it when adding new protocol/features to keep parity deterministic—do not bend runtime semantics to satisfy fixtures.
- Retain the GitHub mini collection under `sampledata/GitHub/`; do not leak domain-specific names from external projects.

CLI behaviour
- Prefer fatal log on case failures instead of dumping Cobra help unless syntax errors.
- Avoid adding new flags without wiring through to runner; mirror Bru where practical.

Testing
- Maintain integration tests: `TestRunFolderSampledata` (gru) and `TestBruCLISingleFile` (bru on our samples).
- Add regression tests for every bug reproduced via external collections before fixing.

Future work pointers
- Reporters (`--output/--format`, json/junit/html), execution controls (`--tests-only`, `--delay`, `--bail`, recursive toggle), TLS/proxy flags, data-driven iterations.
- Import command: `gru import openapi|wsdl` to generate Bruno collections (see BACKLOG).

Style
- ASCII by default; minimal comments except where intent isn’t obvious, but always comment exported types/functions.
- Do not revert user changes; avoid destructive git commands.

Go
- Always comment exported types/funcs thoroughly.
- Fix golint-style comment warnings on exported identifiers; keep public API lint-clean.
- Run the following after tests pass and resolve any issues:
  * golint ./...
  * go vet ./...
  * modernize ./...
- Run `golangci-lint run ./...`, but only resolve issues that are relevant like comments, ctx as first argument if ctx is used, etc. Checking return errors of certain writes where it's not relevant is not something we should focus on resolving.
- For a clean (no-error) lint run that only suppresses the currently acceptable findings, use:
  * `golangci-lint run -c ./.golangci.yml`
