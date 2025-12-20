# Backlog (gru parity with bru)

## Checklist

- [x] Reporters / outputs
  - [x] Implement `--output/--format` with JSON/JUnit/HTML writers and snapshot-style unit tests; JSON parity test vs Bru.
  - [x] Header masking/skip knobs (`--reporter-skip-headers`, `--reporter-skip-all-headers`) with CLI parity timing/delay coverage.
  - [x] Expand reporter parity to larger sampledata, add HTML baselines, and extend CLI parity runs against real-world collections.

- [x] Execution controls & CLI parity
  - [x] Wire `--tests-only`, `--delay`, `--bail`, recursive `-r`, `--env-var`; runner unit coverage for tests-only/delay/bail/recursive.
  - [x] CLI tag include/exclude parity vs Bru; deterministic fixture parity runs for delay/bail/tests-only/recursive.

- [x] Import command foundation
  - [x] `gru import openapi|wsdl` implemented; integration tests validate directory output and summary JSON.
  - [x] Swagger 2.0 conversion to OAS3; path params rendered Bruno-style (`:id`); YAML imports covered; include-path filtering (`-i`).
  - [x] Default-on schema-based tests with required/type/format/regex/length/enum/range/array/property-count/discriminator/oneOf-anyOf coverage; nullable/non-null required assertions added.
  - [x] Remote `$ref` policy: same-origin allowed, cross-origin only with `--allow-remote-refs`; file refs limited to same tree unless `--allow-file-refs`.
  - [x] Example hydration: `exampleValue`/`x-example` normalized to `example`; JSON + XML bodies honored; inline/relative/file/remote (flagged) examples fetched; validation runs post-hydration (warnings only).
  - [x] Broaden schema-based assertions (numeric strictness tiers, array item type depth, enums) and optional strictness levels.
  - [x] Support reporter-parity URL fetch paths with `--insecure` where Bru parity is not required.

- [x] Sampledata / fixtures
  - [x] OpenAPI parity against stored specs (visma/cinode/inyett/lockd) with tests disabled; schema-format fixture exercises generated assertions end-to-end.
  - [x] External example integration fixture with httptest server.

- [x] JS runtime / hooks
  - [x] `process.env` exposed in tests/prelude.
  - [x] Go SDK pre/post request hooks and external pre/post executables per case; abort on nonzero with log streaming.

- [x] Transport / TLS
  - [x] Client-cert config parsing parity (mutual TLS); disable-cookies parity retained.
  - [x] Refine docs/edge cases for `--ignore-truststore`, proxy bypass rules, cookie-jar defaults.

- [x] Logging
  - [x] Global `--log-level`, `--structured`, `--log-caller` persistent flags; LOG_LEVEL env respected when flag unset; pslog integration.
  - [x] Import/run subcommands emit info-level by default with rich debug trace (fn names, With(err)).

- [x] WSDL foundation
  - [x] Real-world fixtures (eBay, Salesforce MC, PayPal sandbox); mock SOAP responder; default tests assert HTTP 200/envelope/response element.
  - [x] Namespace-aware XPath helper; SOAP Faults trigger failures.
  - [x] Base64/hexBinary length checks on decoded bytes; simpleType facets for enums/pattern/length/min/max (basic coverage); VM2-safe XML helper.
  - [x] Parity toggle: `--disable-test-generation` outputs requests only.
  - [x] Required elements (minOccurs > 0) -> XPath presence assertions (deepen nested/mixed/simple-content).
  - [x] SimpleType facet depth: broadened base types + inherited restrictions; parent-array count checks and per-occurrence complex child coverage added.
  - [x] Base64/hexBinary values now require non-zero decoded length even without minLength.
  - [x] Attachments/MTOM: base64 presence/length sanity covered with attachment fixture; MTOM multipart/related streaming supported (root + binary parts with Content-ID).
  - [x] Mock responses per fixture satisfy expanded assertions for eBay/PayPal/MC.

- [x] Data-driven runs
  - [x] CSV/JSON iteration flags (`--csv-file-path`, `--json-file-path`, `--iteration-count`, `--parallel`).

- [x] Docs
  - [x] Refresh INSTRUCTIONS once pending flags/tests land.
