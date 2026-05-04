package reporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"krakendBedRockPlugin/internal/usage"
)

func sampleUsage() usage.Usage {
	return usage.Usage{
		Timestamp:    time.Unix(1714831200, 0).UTC(),
		UserKeyHash:  "hash-only",
		Model:        "anthropic.claude-3-5-sonnet-20241022-v2:0",
		APISurface:   "InvokeModel",
		InputTokens:  12,
		OutputTokens: 8,
		TotalTokens:  20,
		DurationMs:   42,
		RequestID:    "req-123",
	}
}

func TestStdoutReporterWritesJSONLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	r := NewStdout(&buf)

	if err := r.Record(context.Background(), sampleUsage()); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	line := buf.String()
	if strings.Contains(line, "secret-key") {
		t.Fatalf("stdout leaked raw key material: %s", line)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, line)
	}
	if got["msg"] != "bedrock_usage" {
		t.Fatalf("msg = %v, want bedrock_usage", got["msg"])
	}
	if got["user_key_hash"] != "hash-only" {
		t.Fatalf("user_key_hash = %v, want hash-only", got["user_key_hash"])
	}
	if got["input_tokens"] != float64(12) {
		t.Fatalf("input_tokens = %v, want 12", got["input_tokens"])
	}
}

func TestEMFReporterWritesMetricBlock(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	r := NewEMF(&buf, EMFConfig{
		Namespace:           "BedrockUsage",
		Dimensions:          []string{"UserKeyHash", "Model"},
		ParseFailuresMetric: "BedrockUsageParseFailures",
	})
	u := sampleUsage()
	u.ParseFailure = true

	if err := r.Record(context.Background(), u); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("EMF is not JSON: %v\n%s", err, buf.String())
	}
	if got["UserKeyHash"] != "hash-only" {
		t.Fatalf("UserKeyHash = %v, want hash-only", got["UserKeyHash"])
	}
	if got["BedrockUsageParseFailures"] != float64(1) {
		t.Fatalf("BedrockUsageParseFailures = %v, want 1", got["BedrockUsageParseFailures"])
	}
	awsBlock := got["_aws"].(map[string]any)
	metrics := awsBlock["CloudWatchMetrics"].([]any)[0].(map[string]any)
	if metrics["Namespace"] != "BedrockUsage" {
		t.Fatalf("Namespace = %v, want BedrockUsage", metrics["Namespace"])
	}
}

type fakeRedisClient struct {
	increments []redisIncrement
	expires    []string
	err        error
}

func (f *fakeRedisClient) HIncrBy(ctx context.Context, key, field string, value int64) error {
	f.increments = append(f.increments, redisIncrement{key: key, field: field, value: value})
	return f.err
}

func (f *fakeRedisClient) Expire(ctx context.Context, key string, ttl time.Duration) error {
	f.expires = append(f.expires, key)
	return f.err
}

func (f *fakeRedisClient) Close() error {
	return nil
}

func TestRedisReporterIncrementsTokenFields(t *testing.T) {
	t.Parallel()

	client := &fakeRedisClient{}
	r := NewRedisWithClient(client, RedisConfig{
		KeyPrefix:   "bedrock:usage",
		FieldFormat: "2006-01-02:%s",
		TTLDays:     90,
	})

	if err := r.Record(context.Background(), sampleUsage()); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	want := []redisIncrement{
		{key: "bedrock:usage:hash-only", field: "2024-05-04:input_tokens", value: 12},
		{key: "bedrock:usage:hash-only", field: "2024-05-04:output_tokens", value: 8},
		{key: "bedrock:usage:hash-only", field: "2024-05-04:total_tokens", value: 20},
	}
	if len(client.increments) != len(want) {
		t.Fatalf("increments = %+v, want %+v", client.increments, want)
	}
	for i := range want {
		if client.increments[i] != want[i] {
			t.Fatalf("increment[%d] = %+v, want %+v", i, client.increments[i], want[i])
		}
	}
	if len(client.expires) != 1 || client.expires[0] != "bedrock:usage:hash-only" {
		t.Fatalf("expires = %+v, want key expiry", client.expires)
	}
}

type failingReporter struct {
	err error
}

func (r failingReporter) Record(context.Context, usage.Usage) error {
	return r.err
}

func (r failingReporter) Close() error {
	return nil
}

func TestMultiReporterJoinsErrors(t *testing.T) {
	t.Parallel()

	errA := errors.New("a")
	errB := errors.New("b")
	r := Multi{Reporters: []Reporter{
		failingReporter{err: errA},
		failingReporter{err: errB},
	}}

	err := r.Record(context.Background(), sampleUsage())
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Fatalf("Record() error = %v, want joined a and b", err)
	}
}
