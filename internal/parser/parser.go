package parser

import (
	"errors"

	"krakendBedRockPlugin/internal/bedrockpath"
	"krakendBedRockPlugin/internal/usage"
)

const DefaultMaxBodyBytes int64 = 1024 * 1024

type Parser interface {
	Feed([]byte)
	Close() (usage.TokenUsage, error)
}

func New(route bedrockpath.Route, maxBodyBytes int64) Parser {
	if route.Streaming {
		return NewStreamParser(route.Surface)
	}
	return NewJSONParser(route.Surface, maxBodyBytes)
}

func IsParseFailure(err error) bool {
	return errors.Is(err, usage.ErrUsageMissing) ||
		errors.Is(err, usage.ErrBodyTooLarge) ||
		errors.Is(err, usage.ErrMalformedJSON) ||
		errors.Is(err, usage.ErrTruncatedEventStream) ||
		errors.Is(err, usage.ErrEventStreamCRC) ||
		errors.Is(err, usage.ErrParserPanic)
}
