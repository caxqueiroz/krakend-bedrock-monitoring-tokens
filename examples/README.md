# KrakenD Bedrock Usage Plugin Example

Build the plugin from the repository root:

```bash
go build -buildmode=plugin -o bedrock-usage.so .
```

Copy `bedrock-usage.so` into KrakenD's plugin folder and point `plugin.pattern` at that folder. The plugin name is `bedrock-usage`.

## Headers

The plugin reads caller identity from `X-Api-Key` first, then `Authorization` by default. `Authorization: Bearer <token>` is normalized to `<token>` before hashing. Raw keys are never logged; records use a SHA-256 hex hash or `anonymous` if no configured header is present.

## Reporting

`reporter: "stdout"` emits one JSON `slog` line per Bedrock request. `reporter: "cloudwatch"` emits CloudWatch Embedded Metric Format JSON to stdout so the existing log shipping path can publish metrics. `reporter: "redis"` increments Redis hash counters by user-key hash.

The plugin observes responses only. It does not authenticate callers, sign AWS requests, enforce quotas, or block traffic when parsing/reporting fails.
