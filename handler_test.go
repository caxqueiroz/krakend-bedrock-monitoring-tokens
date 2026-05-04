package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"krakendBedRockPlugin/internal/bedrockpath"
	"krakendBedRockPlugin/internal/parser"
	"krakendBedRockPlugin/internal/usage"
)

type captureReporter struct {
	records []usage.Usage
}

func (r *captureReporter) Record(_ context.Context, u usage.Usage) error {
	r.records = append(r.records, u)
	return nil
}

func (r *captureReporter) Close() error {
	return nil
}

func testOptions(rep *captureReporter) handlerOptions {
	return handlerOptions{
		reporter: rep,
		now: func() time.Time {
			return time.Unix(1714831200, 0).UTC()
		},
		newRequestID: func() string {
			return "req-test"
		},
	}
}

func TestHandlerPassesThroughNonBedrockPath(t *testing.T) {
	t.Parallel()

	rep := &captureReporter{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})
	h, err := newHandlerWithOptions(map[string]any{"reporter": "stdout"}, next, testOptions(rep))
	if err != nil {
		t.Fatalf("newHandlerWithOptions() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated || rec.Body.String() != "ok" {
		t.Fatalf("response = %d %q, want 201 ok", rec.Code, rec.Body.String())
	}
	if len(rep.records) != 0 {
		t.Fatalf("records = %+v, want none", rep.records)
	}
}

func TestHandlerRecordsConverseUsageAndPreservesResponse(t *testing.T) {
	t.Parallel()

	const body = `{"usage":{"inputTokens":12,"outputTokens":8,"totalTokens":20}}`
	rep := &captureReporter{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	h, err := newHandlerWithOptions(map[string]any{"reporter": "stdout"}, next, testOptions(rep))
	if err != nil {
		t.Fatalf("newHandlerWithOptions() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/bedrock/model/anthropic.claude-3-5-sonnet-20241022-v2:0/converse", nil)
	req.Header.Set("X-Api-Key", "secret-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Body.String() != body {
		t.Fatalf("body = %q, want %q", rec.Body.String(), body)
	}
	if len(rep.records) != 1 {
		t.Fatalf("records = %d, want 1", len(rep.records))
	}
	got := rep.records[0]
	if got.Model != "anthropic.claude-3-5-sonnet-20241022-v2:0" {
		t.Fatalf("Model = %q", got.Model)
	}
	if got.APISurface != string(bedrockpath.Converse) {
		t.Fatalf("APISurface = %q", got.APISurface)
	}
	if got.InputTokens != 12 || got.OutputTokens != 8 || got.TotalTokens != 20 {
		t.Fatalf("tokens = %d/%d/%d, want 12/8/20", got.InputTokens, got.OutputTokens, got.TotalTokens)
	}
	if got.UserKeyHash == "secret-key" || got.UserKeyHash == "" {
		t.Fatalf("UserKeyHash = %q, want hashed key", got.UserKeyHash)
	}
	if got.RequestID != "req-test" {
		t.Fatalf("RequestID = %q, want req-test", got.RequestID)
	}
}

func TestHandlerRecordsStreamingConverseUsageAndPreservesBytes(t *testing.T) {
	t.Parallel()

	frame := buildHandlerFrame(t, map[string]string{":event-type": "metadata"}, []byte(`{"usage":{"inputTokens":3,"outputTokens":4,"totalTokens":7}}`))
	rep := &captureReporter{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(frame[:5])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write(frame[5:])
	})
	h, err := newHandlerWithOptions(map[string]any{"reporter": "stdout"}, next, testOptions(rep))
	if err != nil {
		t.Fatalf("newHandlerWithOptions() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/model/anthropic.claude-3-5-sonnet-20241022-v2:0/converse-stream", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !bytes.Equal(rec.Body.Bytes(), frame) {
		t.Fatalf("stream bytes changed")
	}
	if len(rep.records) != 1 {
		t.Fatalf("records = %d, want 1", len(rep.records))
	}
	got := rep.records[0]
	if got.InputTokens != 3 || got.OutputTokens != 4 || got.TotalTokens != 7 {
		t.Fatalf("tokens = %d/%d/%d, want 3/4/7", got.InputTokens, got.OutputTokens, got.TotalTokens)
	}
}

func TestHandlerRecordsUpstreamErrorWithoutParseFailure(t *testing.T) {
	t.Parallel()

	rep := &captureReporter{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	})
	h, err := newHandlerWithOptions(map[string]any{"reporter": "stdout"}, next, testOptions(rep))
	if err != nil {
		t.Fatalf("newHandlerWithOptions() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/model/anthropic.claude/invoke", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if len(rep.records) != 1 {
		t.Fatalf("records = %d, want 1", len(rep.records))
	}
	got := rep.records[0]
	if got.ParseError != "upstream status 503" {
		t.Fatalf("ParseError = %q, want upstream status 503", got.ParseError)
	}
	if got.ParseFailure {
		t.Fatal("ParseFailure = true, want false for upstream errors")
	}
	if got.TotalTokens != 0 {
		t.Fatalf("TotalTokens = %d, want 0", got.TotalTokens)
	}
}

type panicParser struct{}

func (panicParser) Feed([]byte) {
	panic("parser exploded")
}

func (panicParser) Close() (usage.TokenUsage, error) {
	return usage.TokenUsage{}, usage.ErrUsageMissing
}

func TestHandlerRecordsParserPanicAsParseFailure(t *testing.T) {
	t.Parallel()

	rep := &captureReporter{}
	opts := testOptions(rep)
	opts.parserFactory = func(bedrockpath.Route, int64) parser.Parser {
		return panicParser{}
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"usage":{"inputTokens":1,"outputTokens":2,"totalTokens":3}}`))
	})
	h, err := newHandlerWithOptions(map[string]any{"reporter": "stdout"}, next, opts)
	if err != nil {
		t.Fatalf("newHandlerWithOptions() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/model/anthropic.claude/converse", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Body.String() == "" {
		t.Fatal("response body was not forwarded")
	}
	got := rep.records[0]
	if got.ParseError == "" {
		t.Fatal("ParseError is empty")
	}
	if !got.ParseFailure {
		t.Fatal("ParseFailure = false, want true")
	}
}

func TestHandlerRejectsInvalidReporterConfig(t *testing.T) {
	t.Parallel()

	_, err := newHandlerWithOptions(map[string]any{"reporter": "bogus"}, http.NotFoundHandler(), handlerOptions{})
	if err == nil {
		t.Fatal("newHandlerWithOptions() error = nil, want error")
	}
}

func buildHandlerFrame(t *testing.T, headers map[string]string, payload []byte) []byte {
	t.Helper()

	var headerBytes []byte
	for name, value := range headers {
		headerBytes = append(headerBytes, byte(len(name)))
		headerBytes = append(headerBytes, name...)
		headerBytes = append(headerBytes, 7)
		headerBytes = binary.BigEndian.AppendUint16(headerBytes, uint16(len(value)))
		headerBytes = append(headerBytes, value...)
	}
	totalLen := uint32(12 + len(headerBytes) + len(payload) + 4)
	frame := make([]byte, 0, totalLen)
	frame = binary.BigEndian.AppendUint32(frame, totalLen)
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(headerBytes)))
	frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame[:8]))
	frame = append(frame, headerBytes...)
	frame = append(frame, payload...)
	frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(frame))
	return frame
}
