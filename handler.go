package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"krakendBedRockPlugin/internal/bedrockpath"
	"krakendBedRockPlugin/internal/identity"
	"krakendBedRockPlugin/internal/parser"
	"krakendBedRockPlugin/internal/reporter"
	"krakendBedRockPlugin/internal/tap"
	"krakendBedRockPlugin/internal/usage"
)

type pluginConfig struct {
	enabled             bool
	keyHeaders          []string
	reporter            string
	logToStdout         bool
	maxBodyBytes        int64
	cloudwatch          reporter.EMFConfig
	redis               reporter.RedisConfig
	parseFailuresMetric string
}

type handlerOptions struct {
	reporter      reporter.Reporter
	now           func() time.Time
	newRequestID  func() string
	parserFactory func(bedrockpath.Route, int64) parser.Parser
}

type bedrockUsageHandler struct {
	next          http.Handler
	cfg           pluginConfig
	reporter      reporter.Reporter
	now           func() time.Time
	newRequestID  func() string
	parserFactory func(bedrockpath.Route, int64) parser.Parser
}

func NewHandler(_ context.Context, cfg map[string]interface{}, next http.Handler) (http.Handler, error) {
	return newHandlerWithOptions(cfg, next, handlerOptions{})
}

func newHandlerWithOptions(raw map[string]any, next http.Handler, opts handlerOptions) (http.Handler, error) {
	if next == nil {
		return nil, errors.New("next handler is nil")
	}

	cfg, err := parseConfig(raw)
	if err != nil {
		return nil, err
	}
	if !cfg.enabled {
		return next, nil
	}

	rep := opts.reporter
	if rep == nil {
		rep, err = buildReporter(cfg)
		if err != nil {
			return nil, err
		}
	}

	now := opts.now
	if now == nil {
		now = time.Now
	}
	newReqID := opts.newRequestID
	if newReqID == nil {
		newReqID = newRequestID
	}
	parserFactory := opts.parserFactory
	if parserFactory == nil {
		parserFactory = parser.New
	}

	return &bedrockUsageHandler{
		next:          next,
		cfg:           cfg,
		reporter:      rep,
		now:           now,
		newRequestID:  newReqID,
		parserFactory: parserFactory,
	}, nil
}

func (h *bedrockUsageHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := bedrockpath.Classify(r.URL.Path)
	if !ok {
		h.next.ServeHTTP(w, r)
		return
	}

	key := identity.Extract(r.Header, h.cfg.keyHeaders)
	reqID := h.newRequestID()
	start := h.now()
	p := h.parserFactory(route, h.cfg.maxBodyBytes)
	tw := tap.New(w, p)

	h.next.ServeHTTP(tw, r)

	status := tw.StatusCode()
	record := usage.Usage{
		Timestamp:   h.now().UTC(),
		UserKeyHash: key.Hash,
		Model:       route.ModelID,
		APISurface:  string(route.Surface),
		DurationMs:  h.now().Sub(start).Milliseconds(),
		RequestID:   reqID,
		StatusCode:  status,
	}

	if status >= http.StatusBadRequest {
		record.ParseError = fmt.Sprintf("upstream status %d", status)
		h.record(r.Context(), record)
		return
	}

	tokens, err := p.Close()
	if parserErr := tw.ParserError(); parserErr != nil {
		err = parserErr
		tokens = usage.TokenUsage{}
	}
	tokens = tokens.WithTotal()
	record.InputTokens = tokens.InputTokens
	record.OutputTokens = tokens.OutputTokens
	record.TotalTokens = tokens.TotalTokens
	if err != nil {
		record.ParseError = err.Error()
		record.ParseFailure = parser.IsParseFailure(err)
	}

	h.record(r.Context(), record)
}

func (h *bedrockUsageHandler) record(ctx context.Context, u usage.Usage) {
	if h.reporter == nil {
		return
	}
	_ = h.reporter.Record(ctx, u)
}

func parseConfig(raw map[string]any) (pluginConfig, error) {
	cfgMap := pluginConfigMap(raw)
	cfg := pluginConfig{
		enabled:             len(cfgMap) > 0,
		keyHeaders:          []string{"x-api-key", "Authorization"},
		reporter:            "stdout",
		logToStdout:         true,
		maxBodyBytes:        parser.DefaultMaxBodyBytes,
		parseFailuresMetric: "BedrockUsageParseFailures",
	}
	cfg.cloudwatch = reporter.EMFConfig{
		Namespace:           "BedrockUsage",
		Dimensions:          []string{"UserKeyHash", "Model"},
		ParseFailuresMetric: cfg.parseFailuresMetric,
	}
	cfg.redis = reporter.RedisConfig{
		KeyPrefix:   "bedrock:usage",
		FieldFormat: "2006-01-02:%s",
	}

	if !cfg.enabled {
		return cfg, nil
	}

	if headers := stringSlice(cfgMap["key_headers"]); len(headers) > 0 {
		cfg.keyHeaders = headers
	}
	if v := stringValue(cfgMap["reporter"]); v != "" {
		cfg.reporter = strings.ToLower(v)
	}
	if v, ok := boolValue(cfgMap["log_to_stdout"]); ok {
		cfg.logToStdout = v
	}
	if v, ok := int64Value(cfgMap["max_body_bytes"]); ok && v > 0 {
		cfg.maxBodyBytes = v
	}
	if v := stringValue(cfgMap["parse_failures_metric"]); v != "" {
		cfg.parseFailuresMetric = v
		cfg.cloudwatch.ParseFailuresMetric = v
	}

	if cw := mapValue(cfgMap["cloudwatch"]); cw != nil {
		if v := stringValue(cw["namespace"]); v != "" {
			cfg.cloudwatch.Namespace = v
		}
		if dims := stringSlice(cw["dimensions"]); len(dims) > 0 {
			cfg.cloudwatch.Dimensions = dims
		}
	}
	if rd := mapValue(cfgMap["redis"]); rd != nil {
		if v := stringValue(rd["url"]); v != "" {
			cfg.redis.URL = v
		}
		if v := stringValue(rd["key_prefix"]); v != "" {
			cfg.redis.KeyPrefix = v
		}
		if v := stringValue(rd["field_format"]); v != "" {
			cfg.redis.FieldFormat = v
		}
		if v, ok := int64Value(rd["ttl_days"]); ok {
			cfg.redis.TTLDays = int(v)
		}
	}

	switch cfg.reporter {
	case "stdout":
	case "cloudwatch":
		if cfg.cloudwatch.Namespace == "" {
			return cfg, errors.New("cloudwatch.namespace is required")
		}
	case "redis":
		if cfg.redis.URL == "" {
			return cfg, errors.New("redis.url is required")
		}
	default:
		return cfg, fmt.Errorf("unsupported reporter %q", cfg.reporter)
	}

	return cfg, nil
}

func pluginConfigMap(raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	if direct := mapValue(raw[PluginName]); direct != nil {
		return direct
	}
	if pluginHTTP := mapValue(raw["plugin/http-server"]); pluginHTTP != nil {
		if nested := mapValue(pluginHTTP[PluginName]); nested != nil {
			return nested
		}
	}
	return raw
}

func buildReporter(cfg pluginConfig) (reporter.Reporter, error) {
	var reporters []reporter.Reporter
	if cfg.logToStdout || cfg.reporter == "stdout" {
		reporters = append(reporters, reporter.NewStdout(nil))
	}

	switch cfg.reporter {
	case "stdout":
	case "cloudwatch":
		reporters = append(reporters, reporter.NewEMF(nil, cfg.cloudwatch))
	case "redis":
		redisReporter, err := reporter.NewRedis(cfg.redis)
		if err != nil {
			return nil, err
		}
		reporters = append(reporters, redisReporter)
	}

	if len(reporters) == 1 {
		return reporters[0], nil
	}
	return reporter.Multi{Reporters: reporters}, nil
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

func mapValue(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	default:
		return nil
	}
}

func stringValue(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func stringSlice(v any) []string {
	switch values := v.(type) {
	case []string:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				out = append(out, value)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if s := stringValue(value); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func boolValue(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

func int64Value(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}
