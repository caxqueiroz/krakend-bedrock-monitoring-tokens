package usage

import (
	"errors"
	"time"
)

var (
	ErrUsageMissing         = errors.New("usage event missing")
	ErrBodyTooLarge         = errors.New("body too large")
	ErrMalformedJSON        = errors.New("malformed json")
	ErrTruncatedEventStream = errors.New("truncated event stream")
	ErrEventStreamCRC       = errors.New("event stream crc mismatch")
	ErrParserPanic          = errors.New("parser panic")
)

type TokenUsage struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

func (u TokenUsage) WithTotal() TokenUsage {
	if u.TotalTokens == 0 && (u.InputTokens != 0 || u.OutputTokens != 0) {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return u
}

func (u TokenUsage) Empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.TotalTokens == 0
}

type Usage struct {
	Timestamp    time.Time
	UserKeyHash  string
	Model        string
	APISurface   string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	DurationMs   int64
	RequestID    string
	ParseError   string
	ParseFailure bool
	StatusCode   int
}

func FromTokenUsage(t TokenUsage) Usage {
	t = t.WithTotal()
	return Usage{
		InputTokens:  t.InputTokens,
		OutputTokens: t.OutputTokens,
		TotalTokens:  t.TotalTokens,
	}
}
