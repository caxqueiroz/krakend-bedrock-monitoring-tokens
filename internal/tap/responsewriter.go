package tap

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"sync"

	"krakendBedRockPlugin/internal/usage"
)

type Parser interface {
	Feed([]byte)
}

type ResponseWriter struct {
	upstream http.ResponseWriter
	parser   Parser

	mu        sync.Mutex
	parserErr error
	status    int
}

func New(upstream http.ResponseWriter, parser Parser) *ResponseWriter {
	return &ResponseWriter{upstream: upstream, parser: parser}
}

func (w *ResponseWriter) Header() http.Header {
	return w.upstream.Header()
}

func (w *ResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.upstream.WriteHeader(statusCode)
}

func (w *ResponseWriter) Write(b []byte) (int, error) {
	n, err := w.upstream.Write(b)
	if n > 0 {
		w.feed(b[:n])
	}
	return n, err
}

func (w *ResponseWriter) Flush() {
	if f, ok := w.upstream.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *ResponseWriter) StatusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *ResponseWriter) ParserError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.parserErr
}

func (w *ResponseWriter) Unwrap() http.ResponseWriter {
	return w.upstream
}

func (w *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.upstream.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

func (w *ResponseWriter) Push(target string, opts *http.PushOptions) error {
	p, ok := w.upstream.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return p.Push(target, opts)
}

func (w *ResponseWriter) feed(b []byte) {
	w.mu.Lock()
	if w.parserErr != nil {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()

	defer func() {
		if recovered := recover(); recovered != nil {
			w.mu.Lock()
			w.parserErr = fmt.Errorf("%w: %v", usage.ErrParserPanic, recovered)
			w.mu.Unlock()
		}
	}()
	if w.parser != nil {
		w.parser.Feed(b)
	}
}
