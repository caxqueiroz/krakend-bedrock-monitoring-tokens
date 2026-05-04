# KrakenD Bedrock Usage Plugin — Design

- **Date:** 2026-05-05
- **Module:** `krakendBedRockPlugin`
- **Status:** Approved (pending user review of this written spec)
- **Based on:** [`krakend/examples/12.plugins-and-quotas`](https://github.com/krakend/examples/tree/main/12.plugins-and-quotas) (HTTP server handler plugin shape)

## Goal

Build a KrakenD HTTP server handler plugin that observes AWS Bedrock requests passing through the gateway, extracts token usage per request, and records it keyed by the caller's API key. Records always go to a stdout log line; an optional backend (CloudWatch via Embedded Metric Format, or Redis hash counters) can be enabled by config.

This plugin is observability-only. Quota enforcement is explicitly out of scope and may be a follow-up project that consumes the same data sources.

## Non-goals

- Authenticating callers. The plugin trusts whatever key it sees and never validates it.
- Signing requests to Bedrock. AWS SigV4 is left to whatever already does it in the KrakenD pipeline.
- Quota enforcement, rate limiting, or request rejection. The plugin never blocks a request.
- Live AWS calls from the plugin's runtime path. EMF goes to stdout; the operator's existing log shipping carries it to CloudWatch.

## Scope of API surfaces

The plugin auto-detects four Bedrock API paths and parses each appropriately:

| Path suffix                                 | API surface                     | Streaming | Parser                                          |
|---------------------------------------------|---------------------------------|-----------|-------------------------------------------------|
| `/model/{id}/invoke`                        | InvokeModel                     | no        | JSON body, provider-specific                    |
| `/model/{id}/invoke-with-response-stream`   | InvokeModelWithResponseStream   | yes       | AWS event-stream frames, provider-specific      |
| `/model/{id}/converse`                      | Converse                        | no        | JSON body, unified `usage` field                |
| `/model/{id}/converse-stream`               | ConverseStream                  | yes       | AWS event-stream frames, unified `metadata` event |

Both API surfaces are required because Claude Code and the Anthropic SDK use the `InvokeModel*` paths, while newer integrations standardize on `Converse*`.

`{modelId}` is captured from the URL and used as the `Model` dimension. Anything not matching one of the four patterns is passed through unchanged with no recording.

## Architecture

```
Client ──► KrakenD ─► [bedrock-usage plugin] ─► AWS SigV4 signer ─► Bedrock
                              │                        (existing/separate)
                              │
                              ├─ on request:  classify route, extract API key,
                              │               sha256-hash it, capture model from URL,
                              │               record start time, attach RequestID
                              │
                              └─ on response: wrap ResponseWriter, tee bytes to a parser
                                              that detects API surface and extracts token
                                              usage from the right field/event
                                              ──► reporter.Record(usage)
```

The plugin is purely observational on the response path. Every byte from Bedrock flows to the client first; the parser sees only what was actually flushed. Parsing and reporting run synchronously on the request goroutine — no background workers, no buffering of the full response, no critical-path latency added beyond the cost of one `slog` line.

## Components & package layout

```
krakendBedRockPlugin/
├── go.mod
├── plugin.go                         # exports HandlerRegisterer, registers the plugin
├── handler.go                        # http.Handler factory: parses config, wraps next.ServeHTTP
│
├── internal/
│   ├── identity/
│   │   └── apikey.go                 # extract from x-api-key / Authorization, sha256 → hex
│   │
│   ├── bedrockpath/
│   │   └── route.go                  # classify URL → {APISurface, ModelID, IsStream}
│   │
│   ├── parser/
│   │   ├── parser.go                 # interface Parser { Feed([]byte); Close() (Usage, error) }
│   │   ├── json_unified.go           # Converse + Anthropic InvokeModel non-streaming
│   │   ├── json_provider.go          # InvokeModel non-streaming for non-Anthropic providers
│   │   ├── eventstream.go            # AWS event-stream frame decoder (length+CRC framing)
│   │   ├── stream_converse.go        # ConverseStream: pulls usage from `metadata` event
│   │   └── stream_invoke.go          # InvokeModelWithResponseStream: per-provider event handlers
│   │
│   ├── reporter/
│   │   ├── reporter.go               # interface Reporter { Record(ctx, Usage); Close() }
│   │   ├── stdout.go                 # default — slog JSON line, no backend
│   │   ├── emf.go                    # CloudWatch EMF JSON to stdout
│   │   ├── redis.go                  # HINCRBY backend
│   │   └── multi.go                  # tee (stdout always on, plus optional backend)
│   │
│   └── tap/
│       └── responsewriter.go         # http.ResponseWriter + http.Flusher wrapper that tees
│                                     # bytes to a parser without buffering the whole response
│
└── examples/
    ├── krakend.json                  # sample KrakenD config wiring the plugin
    └── README.md                     # how to build the .so and configure CloudWatch shipping
```

### Boundaries

| Package        | Owns                                       | Knows nothing about                          |
|----------------|--------------------------------------------|----------------------------------------------|
| `identity`     | header → sha256-hex of API key (or `"anonymous"`). Headers are tried in `key_headers` order; for `Authorization`, the `Bearer ` prefix is stripped (case-insensitive); the raw key is never returned outside this package. | HTTP routing, parsers, reporters             |
| `bedrockpath`  | URL → `(surface, modelID, streaming)`      | request bodies, auth, reporters              |
| `parser`       | byte-stream → `Usage` struct               | HTTP, identity, reporting                    |
| `tap`          | wrapping `ResponseWriter`, teeing bytes    | what the parser does with them               |
| `reporter`     | `Usage` → log line / Redis op              | how usage was extracted                      |
| `handler`      | wires the above together for one HTTP call | concrete parser/reporter implementations (gets them via factory) |

### The `Usage` contract

Single struct shared between `parser` and `reporter`:

```go
type Usage struct {
    Timestamp     time.Time
    UserKeyHash   string  // sha256 hex of the API key, or "anonymous"
    Model         string  // from URL path
    APISurface    string  // "InvokeModel" | "InvokeModelWithResponseStream" | "Converse" | "ConverseStream"
    InputTokens   int64
    OutputTokens  int64
    TotalTokens   int64
    DurationMs    int64
    RequestID     string  // generated UUID per request
    ParseError    string  // empty on success; populated when extraction failed for any reason
    ParseFailure  bool    // true ONLY for genuine parse failures (schema/format issues);
                          // drives the BedrockUsageParseFailures metric.
                          // Stays false for upstream errors, missing keys, or client disconnects.
}
```

## Data flow

### Per-request lifecycle

```
1. handler.ServeHTTP(w, r)
   ├─ route = bedrockpath.Classify(r.URL.Path)
   │     → if not a Bedrock path: next.ServeHTTP(w,r); return       (cheap fast-path)
   │
   ├─ keyHash = identity.Extract(r.Header)                          (sha256 hex; "anonymous" if missing)
   ├─ requestID = uuid.NewString()                                  (added to slog & EMF)
   │
   ├─ parser = parser.For(route)                                    (factory → correct impl)
   ├─ tw = tap.New(w, parser)                                       (wraps ResponseWriter)
   ├─ start = time.Now()
   │
   ├─ next.ServeHTTP(tw, r)            ◄── bytes flow client-bound, parser sniffs in parallel
   │
   ├─ usage, err = parser.Close()
   ├─ usage.UserKeyHash = keyHash
   │   usage.Model      = route.ModelID
   │   usage.APISurface = route.Surface
   │   usage.DurationMs = time.Since(start).Milliseconds()
   │   usage.RequestID  = requestID
   │   if err != nil { usage.ParseError = err.Error() }
   │
   └─ reporter.Record(ctx, usage)                                   (best-effort, never blocks)
```

### The tap (response wrapper)

```go
type Tap struct {
    upstream http.ResponseWriter
    parser   parser.Parser
}

func (t *Tap) Write(p []byte) (int, error) {
    n, err := t.upstream.Write(p)         // client-bound bytes go out FIRST
    if n > 0 { t.parser.Feed(p[:n]) }     // parser sees only what was actually flushed
    return n, err
}

func (t *Tap) Flush() {                   // satisfy http.Flusher for streaming
    if f, ok := t.upstream.(http.Flusher); ok { f.Flush() }
}

func (t *Tap) Header() http.Header     { return t.upstream.Header() }
func (t *Tap) WriteHeader(code int)    { t.upstream.WriteHeader(code) }
```

Bytes are never held back. If the parser panics or runs slow, the bytes have already left the gateway; a `recover()` in `Feed` catches the panic and logs it.

### Parser dispatch

| `route.Surface`                 | `route.IsStream` | Parser implementation                          |
|---------------------------------|------------------|------------------------------------------------|
| `InvokeModel`                   | false            | `json_unified` (Anthropic) or `json_provider`  |
| `InvokeModelWithResponseStream` | true             | `eventstream` → `stream_invoke` (per-provider) |
| `Converse`                      | false            | `json_unified`                                 |
| `ConverseStream`                | true             | `eventstream` → `stream_converse`              |

### AWS event-stream framing

Streaming APIs return frames in AWS's event-stream format:

```
+--------------+--------------+------------------+----------+----------+
| TotalLen (4) | HeadersLen(4)| Prelude CRC32(4) | Headers  | Payload  | Msg CRC32(4)
+--------------+--------------+------------------+----------+----------+
```

`parser/eventstream.go` is a streaming decoder: feed bytes, emit complete `Frame{Headers, Payload}` events. It buffers only the current frame (≤ a few KB), never the whole response.

For **ConverseStream**, `stream_converse.go` watches for a frame whose `:event-type` header is `metadata` and pulls `usage.inputTokens` / `usage.outputTokens` / `usage.totalTokens` out of the JSON payload.

For **InvokeModelWithResponseStream**, `stream_invoke.go` looks at two places:
1. **Anthropic Claude:** `message_start.message.usage.input_tokens` and the running `message_delta.usage.output_tokens` — final value wins.
2. **Cross-provider fallback:** AWS injects `amazon-bedrock-invocationMetrics` (with `inputTokenCount` / `outputTokenCount`) into the last frame regardless of provider — used as a safety net.

If neither yields usage, `Close()` returns `ErrUsageMissing`, the reporter records the row with `tokens=unknown`, and the parse-failures metric increments.

### Non-streaming bodies

For `InvokeModel` / `Converse` (no streaming), the parser buffers up to a configurable cap (default 1 MiB) and JSON-decodes once at `Close()`. Beyond the cap the parser stops buffering, returns `ErrBodyTooLarge`, and the request still completes normally for the client.

## Reporters

Exactly one reporter is selected via config. The stdout slog line is always emitted in addition (controllable with `log_to_stdout: false`).

### Stdout (default)

A single `log/slog` JSON line per request:

```json
{
  "level": "INFO",
  "time": "2026-05-05T10:23:11.482Z",
  "msg": "bedrock_usage",
  "user_key_hash": "9af1...c2",
  "model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
  "api_surface": "InvokeModelWithResponseStream",
  "input_tokens": 1234,
  "output_tokens": 567,
  "total_tokens": 1801,
  "duration_ms": 842,
  "request_id": "req-xyz"
}
```

### CloudWatch via EMF

The plugin writes a single JSON line to **stdout** in EMF shape per request. It does not call any AWS API. The operator's existing log-shipping path (ECS `awslogs` driver, EKS Fluent Bit, EC2 CloudWatch Agent, or Lambda native) carries the line into CloudWatch Logs, which automatically extracts the metrics declared in the `_aws.CloudWatchMetrics` block.

```json
{
  "_aws": {
    "Timestamp": 1714831200000,
    "CloudWatchMetrics": [{
      "Namespace": "BedrockUsage",
      "Dimensions": [["UserKeyHash","Model"]],
      "Metrics": [
        {"Name":"InputTokens","Unit":"Count"},
        {"Name":"OutputTokens","Unit":"Count"},
        {"Name":"TotalTokens","Unit":"Count"}
      ]
    }]
  },
  "UserKeyHash": "9af1...c2",
  "Model": "anthropic.claude-3-5-sonnet-20241022-v2:0",
  "ApiSurface": "InvokeModelWithResponseStream",
  "InputTokens": 1234,
  "OutputTokens": 567,
  "TotalTokens": 1801,
  "RequestId": "req-xyz",
  "DurationMs": 842
}
```

Writes to `os.Stdout` are mutex-guarded so JSON lines are not interleaved.

### Redis (alternative)

Hash counters via `HINCRBY`:

- Key:   `<key_prefix>:<UserKeyHash>` — default prefix `bedrock:usage`
- Field: rendered from `field_format` (default `2006-01-02:%s` — Go time layout for the date, plus `%s` for the metric name `input_tokens` / `output_tokens` / `total_tokens`)
- TTL:   refreshed to `ttl_days` on each write (0 = no TTL)

A single `go-redis` client is built at registration and reused. Each per-request operation runs under a 200 ms timeout derived from `r.Context()` so Redis hiccups never delay the response.

## Configuration

KrakenD passes plugin config as a `map[string]interface{}` from the endpoint's `extra_config` block. This plugin reads its config under namespace `plugin/http-server` with key `bedrock-usage`.

```jsonc
{
  "endpoint": "/bedrock/{model}/{action}",
  "method": "POST",
  "extra_config": {
    "plugin/http-server": {
      "name": ["bedrock-usage"],
      "bedrock-usage": {
        "key_headers": ["x-api-key", "Authorization"],
        "reporter": "cloudwatch",
        "log_to_stdout": true,

        "cloudwatch": {
          "namespace": "BedrockUsage",
          "dimensions": ["UserKeyHash", "Model"],
          "log_group_hint": "krakend-bedrock"
        },

        "redis": {
          "url": "redis://localhost:6379/0",
          "key_prefix": "bedrock:usage",
          "field_format": "2006-01-02:%s",
          "ttl_days": 90
        },

        "parse_failures_metric": "BedrockUsageParseFailures",
        "max_body_bytes": 1048576
      }
    }
  }
}
```

### Config validation (at registration)

| Field                    | Rule                                              | On failure                              |
|--------------------------|---------------------------------------------------|-----------------------------------------|
| `reporter`               | one of `stdout` / `cloudwatch` / `redis`          | refuse to register, log error           |
| `key_headers`            | non-empty                                         | default to `["x-api-key","Authorization"]` |
| `cloudwatch.namespace`   | non-empty when `reporter == "cloudwatch"`         | refuse to register                      |
| `redis.url`              | parseable when `reporter == "redis"`              | refuse to register                      |
| `redis.field_format`     | must contain exactly one `%s`                     | default to `"2006-01-02:%s"`            |
| `max_body_bytes`         | > 0                                               | default to 1 MiB                        |

If config is missing entirely, the plugin registers as a pure pass-through (does nothing) and logs once at startup so misconfiguration is visible.

### Friendly-name mapping — deferred

Per-key friendly names are intentionally not in this design. The `UserKeyHash` is the sole identifier in records. A future additive `key_aliases: {"<hash>": "name"}` block can be consumed by `identity` without touching other units.

## Error handling & failure modes

The plugin is an observer. Every failure mode below results in (a) the client request completing exactly as it would without the plugin, and (b) some signal — a log line, a parse-failure metric, or both — so silent breakage is impossible.

| Failure                                                      | What the plugin does                                                                                  | What the client sees                          |
|--------------------------------------------------------------|-------------------------------------------------------------------------------------------------------|-----------------------------------------------|
| **No API key in any configured header**                      | Record `usage.UserKeyHash="anonymous"`, log at `WARN` once per minute, continue normally              | Normal response                               |
| **Path doesn't match Bedrock pattern**                       | Skip everything, call `next.ServeHTTP` directly                                                       | Normal response                               |
| **Streaming parse error** (frame CRC mismatch, malformed JSON) | Stop feeding the parser, mark `usage.ParseError`, increment `BedrockUsageParseFailures`             | Normal response — bytes already left via the tap |
| **Streaming usage missing** (stream ended cleanly, no `metadata` event) | Emit record with `tokens=unknown`, `ParseError="usage event missing"`, increment metric    | Normal response                               |
| **Non-streaming body exceeds `max_body_bytes`**              | Stop buffering at the cap, emit record with `ParseError="body too large"`, increment metric           | Normal response (bytes still flow through)    |
| **Non-streaming JSON unparseable**                           | `ParseError="json decode: <msg>"`, increment metric                                                   | Normal response                               |
| **Client disconnects mid-stream**                            | `parser.Close()` called on partial data; emit record with `ParseError="client disconnected"`; if usage already extracted, report it | Disconnect (caused by client)                 |
| **Bedrock returns non-2xx**                                  | Skip parsing; record `usage.ParseError="upstream status N"` and HTTP status as a separate field; no token fields | Original error response from Bedrock          |
| **Reporter call fails** (Redis unreachable, stdout closed)   | Log at `ERROR` once per minute, drop the record, continue                                             | Normal response                               |
| **Parser panics**                                            | `recover()` in the tap's `Write`; log stack at `ERROR` with request ID; future bytes pass through unparsed; record emitted with `ParseError="parser panic"` and `ParseFailure=true` (increments metric) | Normal response                               |
| **Reporter panics**                                          | `recover()` around `reporter.Record`; log stack                                                       | Normal response                               |
| **Plugin config invalid at startup**                         | Registration fails, KrakenD logs the reason, plugin is a no-op                                        | Normal response (no token tracking)           |

### Concurrency & resource bounds

- **No background goroutines per request.** Parser runs synchronously inside `tap.Write` and `parser.Close`, both already on the request goroutine.
- **One Redis client** for the lifetime of the plugin (built at registration), with `go-redis`'s built-in pool. Per-request operations use a 200 ms timeout derived from `r.Context()`.
- **EMF reporter** writes to `os.Stdout` under a `sync.Mutex` so JSON lines aren't interleaved.
- **Rate-limited error logs** use a per-category token bucket (`rate.NewLimiter(rate.Every(time.Minute), 1)`) to keep noisy clients from drowning the log.

### Parse-failure metric

`BedrockUsageParseFailures` is dimensioned by `Model` and `APISurface` and increments once per record where `Usage.ParseFailure == true` — i.e. genuine schema/format issues only (CRC mismatch, malformed JSON, missing usage event, body too large, parser panic). It does **not** increment for upstream non-2xx, missing API key, client disconnect, or reporter failures, because those carry no schema-drift signal and would otherwise drown the metric in normal operational noise.

A sudden non-zero value on a model that previously worked is the early-warning signal for: AWS changed the event-stream schema, a new Anthropic model version moved fields around, or a client started sending malformed bodies. Operators should alarm on `> 0` over a 5-minute window.

## Testing strategy

Tests are organized to match the package boundaries above. Each unit is testable in isolation; integration tests cover the wiring.

### Unit tests (table-driven, fast, no I/O)

- **`identity/apikey_test.go`** — `x-api-key` present, `Authorization: Bearer`, both present (x-api-key wins), neither (`"anonymous"`), malformed `Authorization`, case-insensitive lookup.
- **`bedrockpath/route_test.go`** — all four path shapes, real Bedrock model IDs (containing `:` and `.`), prefixed paths (`/bedrock/...`), non-Bedrock paths.
- **`parser/eventstream_test.go`** — single complete frame, frame split across `Feed` calls (fuzzed chunking), multiple frames per `Feed`, truncation at EOF, prelude/message CRC mismatch, `:event-type` header parsing.
- **`parser/json_unified_test.go` and `json_provider_test.go`** — Converse, Anthropic InvokeModel, Llama (`prompt_token_count`/`generation_token_count`), Titan (`inputTextTokenCount` + summed `results[].tokenCount`), Cohere (`meta.billed_units.*`), AI21 Jamba (`usage.prompt_tokens`/`completion_tokens`), Mistral → `ErrUsageMissing`, empty body, malformed JSON, body over `max_body_bytes`.
- **`parser/stream_*_test.go`** — ConverseStream golden fixture; InvokeModelWithResponseStream Anthropic golden fixture; `amazon-bedrock-invocationMetrics` fallback for non-Anthropic providers; truncated-before-`metadata` → `ErrUsageMissing`; malformed JSON inside otherwise-valid frame.
- **`reporter/*_test.go`** — `stdout`: capture stdout, assert exactly one slog JSON line, all required fields present, no raw key material anywhere. `emf`: capture stdout, assert `_aws.CloudWatchMetrics` block well-formed, dimensions present, metric values match. `redis`: against `miniredis`, assert `HINCRBY` calls, key shape, TTL set when configured. Panic in reporter is recovered, error logged, no propagation.
- **`tap/responsewriter_test.go`** — bytes flushed to upstream before parser sees them (recording parser); `Flush()` propagates when upstream implements `http.Flusher`; `Header()` and `WriteHeader` pass through; parser panic during `Feed` is recovered, subsequent `Write`s still work.

### Integration tests

**`handler_test.go`** — end-to-end through the plugin's `http.Handler`:

- mount plugin in front of an `httptest.Server` that replays a recorded Bedrock response (one fixture per `(surface, provider)` cell)
- assert: client receives byte-identical response, recorded `Usage` matches expected
- streaming variant: server flushes frames with delays; client reads all bytes; recorded `Usage` correct
- client disconnects after first frame: server keeps writing, plugin emits record with `ParseError="client disconnected"`
- upstream returns 4xx/5xx: no token fields, status captured, no parse-failure metric increment

### Fixture corpus

Lives in `internal/parser/testdata/`:

```
testdata/
├── invoke/
│   ├── anthropic-claude-3-5-sonnet.json
│   ├── llama-3-1-70b.json
│   ├── titan-text-premier.json
│   ├── cohere-command-r.json
│   └── ai21-jamba-1-5-large.json
├── invoke-stream/
│   ├── anthropic-claude-3-5-sonnet.bin    (raw event-stream bytes)
│   └── llama-3-1-70b.bin
├── converse/
│   └── unified.json
└── converse-stream/
    └── unified.bin
```

Bytes captured once with `aws bedrock-runtime` against real models, then committed. Tests never touch AWS.

### Out of scope for tests

- **No live AWS calls.** Bedrock is non-deterministic and costs money per test run; fixtures are deterministic and free.
- **No load tests in CI.** A separate `bench_test.go` measures parser throughput (`-bench=. -benchmem`) — runnable locally, not on every PR.
- **No KrakenD-end-to-end tests.** Building the `.so` and starting KrakenD is a smoke test we run manually before release; the unit + integration suite already covers all plugin behavior.

### Lint & coverage gates

- `golangci-lint run ./...` — must be clean
- `go test -race ./...` — race detector mandatory
- Coverage target: **≥ 85%** on `internal/parser` and `internal/reporter`; the rest is exercised by integration tests

## Open follow-ups (not part of this spec)

- **Quota enforcement** as a separate plugin (or a mode of this one), consuming the Redis hash counters.
- **Friendly key-name mapping** via additive `key_aliases` config.
- **Prometheus reporter** as a sibling of EMF/Redis — natural extension since `reporter.Reporter` is an interface.
- **Provider parsers beyond the initial set** (e.g. Stability AI image models, Mistral with token estimation) — added as new files under `internal/parser/` without touching existing code.
