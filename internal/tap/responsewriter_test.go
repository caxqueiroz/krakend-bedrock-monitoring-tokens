package tap

import (
	"bytes"
	"errors"
	"net/http"
	"testing"

	"krakendBedRockPlugin/internal/usage"
)

type parserFunc func([]byte)

func (f parserFunc) Feed(b []byte) {
	f(b)
}

type recordingResponseWriter struct {
	header  http.Header
	body    bytes.Buffer
	status  int
	flushed bool
}

func (w *recordingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *recordingResponseWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

func (w *recordingResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func (w *recordingResponseWriter) Flush() {
	w.flushed = true
}

func TestWriteSendsBytesBeforeParserFeed(t *testing.T) {
	t.Parallel()

	upstream := &recordingResponseWriter{}
	var parserSaw []byte
	tw := New(upstream, parserFunc(func(b []byte) {
		if upstream.body.String() != "hello" {
			t.Fatalf("upstream body before parser = %q, want hello", upstream.body.String())
		}
		parserSaw = append(parserSaw, b...)
	}))

	n, err := tw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 5 {
		t.Fatalf("Write() n = %d, want 5", n)
	}
	if upstream.body.String() != "hello" {
		t.Fatalf("upstream body = %q, want hello", upstream.body.String())
	}
	if string(parserSaw) != "hello" {
		t.Fatalf("parser saw %q, want hello", parserSaw)
	}
}

func TestPassthroughMethods(t *testing.T) {
	t.Parallel()

	upstream := &recordingResponseWriter{}
	tw := New(upstream, parserFunc(func([]byte) {}))

	tw.Header().Set("x-test", "ok")
	tw.WriteHeader(http.StatusAccepted)
	tw.Flush()

	if got := upstream.Header().Get("x-test"); got != "ok" {
		t.Fatalf("header = %q, want ok", got)
	}
	if upstream.status != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", upstream.status, http.StatusAccepted)
	}
	if !upstream.flushed {
		t.Fatal("Flush() was not propagated")
	}
}

func TestParserPanicIsRecoveredAndWritesContinue(t *testing.T) {
	t.Parallel()

	upstream := &recordingResponseWriter{}
	tw := New(upstream, parserFunc(func([]byte) {
		panic("boom")
	}))

	if _, err := tw.Write([]byte("first")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}
	if _, err := tw.Write([]byte("second")); err != nil {
		t.Fatalf("second Write() error = %v", err)
	}

	if upstream.body.String() != "firstsecond" {
		t.Fatalf("upstream body = %q, want firstsecond", upstream.body.String())
	}
	if !errors.Is(tw.ParserError(), usage.ErrParserPanic) {
		t.Fatalf("ParserError() = %v, want %v", tw.ParserError(), usage.ErrParserPanic)
	}
}
