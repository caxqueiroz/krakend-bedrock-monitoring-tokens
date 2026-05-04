# KrakenD Bedrock Usage Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a KrakenD HTTP server handler plugin that observes Bedrock responses and records token usage by hashed caller key without changing client-visible behavior.

**Architecture:** The plugin exports KrakenD's HTTP handler registerer shape, classifies Bedrock runtime paths, wraps the downstream `http.ResponseWriter`, tees response bytes into surface-specific parsers, and emits usage records through stdout/EMF/Redis reporters. Internal packages keep routing, identity, parsing, response tapping, reporting, and handler wiring independently testable.

**Tech Stack:** Go 1.26, standard `net/http`, KrakenD plugin exported registerer shape, AWS event-stream decoding with CRC32 validation, `log/slog`, optional `github.com/redis/go-redis/v9`.

---

## File Structure

- Create `plugin.go`: exported `HandlerRegisterer` variable and KrakenD registration method.
- Create `handler.go`: plugin config parsing, handler factory, request lifecycle, parser/reporter wiring.
- Create `internal/usage/usage.go`: shared `Usage` struct and parse error sentinels.
- Create `internal/identity/apikey.go`: API key extraction and SHA-256 hashing.
- Create `internal/bedrockpath/route.go`: Bedrock API path classifier.
- Create `internal/parser/*.go`: JSON parsers, AWS event-stream decoder, and stream parsers.
- Create `internal/tap/responsewriter.go`: `http.ResponseWriter` wrapper that writes upstream first and feeds parser second.
- Create `internal/reporter/*.go`: stdout, EMF, Redis, and multi reporter implementations.
- Create `examples/README.md` and `examples/krakend.json`: plugin build and KrakenD wiring docs.

## Task 1: Route And Identity

**Files:**
- Create: `internal/usage/usage.go`
- Create: `internal/identity/apikey.go`
- Create: `internal/identity/apikey_test.go`
- Create: `internal/bedrockpath/route.go`
- Create: `internal/bedrockpath/route_test.go`

- [ ] **Step 1: Write failing tests**
  - `identity` tests cover `x-api-key`, bearer auth stripping, configured priority order, missing key as `anonymous`, and case-insensitive headers.
  - `bedrockpath` tests cover the four Bedrock suffixes, prefixed paths, model IDs containing dots/colons, and non-Bedrock paths.

- [ ] **Step 2: Run tests and confirm red**
  - Run: `go test ./internal/identity ./internal/bedrockpath`
  - Expected: package compile failures because the packages do not exist yet.

- [ ] **Step 3: Implement minimal route and identity packages**
  - `identity.Extract(headers http.Header, keyHeaders []string) Result`
  - `bedrockpath.Classify(path string) (Route, bool)`
  - `usage.Usage` shared record type and parser error sentinels.

- [ ] **Step 4: Run tests and confirm green**
  - Run: `go test ./internal/identity ./internal/bedrockpath`
  - Expected: pass.

## Task 2: Parsers

**Files:**
- Create: `internal/parser/parser.go`
- Create: `internal/parser/json.go`
- Create: `internal/parser/eventstream.go`
- Create: `internal/parser/stream.go`
- Create: `internal/parser/json_test.go`
- Create: `internal/parser/eventstream_test.go`
- Create: `internal/parser/stream_test.go`

- [ ] **Step 1: Write failing parser tests**
  - JSON tests cover Converse `usage`, Anthropic InvokeModel `usage`, Anthropic Messages `usage`, Llama token counts, Titan output token summing, Cohere billed units, AI21 usage, missing usage, malformed JSON, empty body, and body cap.
  - Event-stream tests cover a complete frame, split frames, multiple frames in one feed, truncated EOF, CRC mismatch, and `:event-type` header decoding.
  - Stream tests cover ConverseStream metadata events, Anthropic streaming usage deltas, Bedrock invocation metrics fallback, missing usage, and malformed payload JSON.

- [ ] **Step 2: Run tests and confirm red**
  - Run: `go test ./internal/parser`
  - Expected: compile failures because parser code does not exist yet.

- [ ] **Step 3: Implement parser interfaces and JSON extraction**
  - Add `Parser` interface with `Feed([]byte)` and `Close() (usage.TokenUsage, error)`.
  - Add bounded JSON buffering and token extraction for the provider fields in the design.

- [ ] **Step 4: Implement event-stream and streaming parsers**
  - Decode AWS event-stream frames incrementally.
  - Validate prelude and message CRCs.
  - Parse string/bool/int header values needed for `:event-type`.
  - Extract usage from Converse metadata, Anthropic usage events, and `amazon-bedrock-invocationMetrics`.

- [ ] **Step 5: Run parser tests and confirm green**
  - Run: `go test ./internal/parser`
  - Expected: pass.

## Task 3: Tap And Reporters

**Files:**
- Create: `internal/tap/responsewriter.go`
- Create: `internal/tap/responsewriter_test.go`
- Create: `internal/reporter/reporter.go`
- Create: `internal/reporter/stdout.go`
- Create: `internal/reporter/emf.go`
- Create: `internal/reporter/redis.go`
- Create: `internal/reporter/multi.go`
- Create: `internal/reporter/reporter_test.go`

- [ ] **Step 1: Write failing tap and reporter tests**
  - Tap tests assert upstream write order, flush propagation, header/status passthrough, parser panic recovery, and continued writes after panic.
  - Reporter tests assert stdout JSON fields, no raw key material, EMF metric block and parse-failure metric, multi reporter error joining, and Redis key/field increments using a fake client.

- [ ] **Step 2: Run tests and confirm red**
  - Run: `go test ./internal/tap ./internal/reporter`
  - Expected: compile failures because packages do not exist yet.

- [ ] **Step 3: Implement tap**
  - Write client-bound bytes before parser feed.
  - Recover parser panic, remember the panic error, and keep future writes pass-through.
  - Preserve `http.Flusher`, `http.Hijacker`, and `http.Pusher` only where supported by the upstream writer.

- [ ] **Step 4: Implement reporters**
  - `Stdout` writes one JSON log event through `slog`.
  - `EMF` writes mutex-guarded CloudWatch Embedded Metric Format JSON lines.
  - `Redis` increments input/output/total fields through a small interface so tests do not require a Redis server.
  - `Multi` records to each reporter and returns joined errors without panicking.

- [ ] **Step 5: Run tests and confirm green**
  - Run: `go test ./internal/tap ./internal/reporter`
  - Expected: pass.

## Task 4: Handler And Plugin Wiring

**Files:**
- Create: `handler.go`
- Create: `plugin.go`
- Create: `handler_test.go`

- [ ] **Step 1: Write failing handler tests**
  - Non-Bedrock path is pass-through and does not record usage.
  - Non-streaming Converse response is byte-identical to the client and records expected usage.
  - Streaming Converse response is byte-identical and records expected usage.
  - Upstream non-2xx records status error without parse-failure.
  - Parser panic is recorded as parse failure while response still reaches the client.
  - Invalid config rejects handler construction.

- [ ] **Step 2: Run tests and confirm red**
  - Run: `go test ./...`
  - Expected: compile failures because handler/plugin code does not exist yet.

- [ ] **Step 3: Implement plugin registerer and handler factory**
  - Export `var HandlerRegisterer = registerer("bedrock-usage")`.
  - Implement `RegisterHandlers` with KrakenD's HTTP server plugin callback shape.
  - Parse config from the plugin namespace and support direct config in tests.
  - Build parser/reporter per request and record best-effort usage after `next.ServeHTTP`.

- [ ] **Step 4: Run all tests and confirm green**
  - Run: `go test ./...`
  - Expected: pass.

## Task 5: Examples, Formatting, And Verification

**Files:**
- Create: `examples/README.md`
- Create: `examples/krakend.json`
- Modify: `go.mod`

- [ ] **Step 1: Add examples**
  - Document `go build -buildmode=plugin -o bedrock-usage.so .`.
  - Show KrakenD `extra_config` for stdout and CloudWatch EMF reporting.
  - Explain required caller headers and that raw API keys are hashed before logging.

- [ ] **Step 2: Format**
  - Run: `gofmt -w plugin.go handler.go internal examples`
  - Expected: no output.

- [ ] **Step 3: Verify tests**
  - Run: `go test ./...`
  - Expected: pass.

- [ ] **Step 4: Verify plugin build**
  - Run: `go build -buildmode=plugin -o /tmp/bedrock-usage.so .`
  - Expected: pass.

- [ ] **Step 5: Review git diff**
  - Run: `git diff --stat`
  - Expected: implementation files, examples, and plan only.
