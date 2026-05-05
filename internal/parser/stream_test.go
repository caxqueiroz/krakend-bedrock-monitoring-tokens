package parser

import (
	"encoding/base64"
	"errors"
	"testing"

	"krakendBedRockPlugin/internal/bedrockpath"
	"krakendBedRockPlugin/internal/usage"
)

func TestStreamParserExtractsUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		surface bedrockpath.APISurface
		frames  [][]byte
		want    usage.TokenUsage
	}{
		{
			name:    "converse stream metadata",
			surface: bedrockpath.ConverseStream,
			frames: [][]byte{
				BuildEventStreamFrameForTest(t, map[string]string{":event-type": "contentBlockDelta"}, []byte(`{"delta":{"text":"hi"}}`)),
				BuildEventStreamFrameForTest(t, map[string]string{":event-type": "metadata"}, []byte(`{"usage":{"inputTokens":3,"outputTokens":4,"totalTokens":7}}`)),
			},
			want: usage.TokenUsage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7},
		},
		{
			name:    "anthropic invoke stream",
			surface: bedrockpath.InvokeModelWithResponseStream,
			frames: [][]byte{
				BuildEventStreamFrameForTest(t, map[string]string{":event-type": "chunk"}, []byte(`{"type":"message_start","message":{"usage":{"input_tokens":11,"output_tokens":1}}}`)),
				BuildEventStreamFrameForTest(t, map[string]string{":event-type": "chunk"}, []byte(`{"type":"message_delta","usage":{"output_tokens":6}}`)),
			},
			want: usage.TokenUsage{InputTokens: 11, OutputTokens: 6, TotalTokens: 17},
		},
		{
			name:    "bedrock invocation metrics fallback",
			surface: bedrockpath.InvokeModelWithResponseStream,
			frames: [][]byte{
				BuildEventStreamFrameForTest(t, map[string]string{":event-type": "chunk"}, []byte(`{"amazon-bedrock-invocationMetrics":{"inputTokenCount":21,"outputTokenCount":22}}`)),
			},
			want: usage.TokenUsage{InputTokens: 21, OutputTokens: 22, TotalTokens: 43},
		},
		{
			name:    "bedrock chunk bytes envelope",
			surface: bedrockpath.InvokeModelWithResponseStream,
			frames: [][]byte{
				BuildEventStreamFrameForTest(t, map[string]string{":event-type": "chunk"}, []byte(`{"bytes":"`+base64.StdEncoding.EncodeToString([]byte(`{"amazon-bedrock-invocationMetrics":{"inputTokenCount":7,"outputTokenCount":3}}`))+`"}`)),
			},
			want: usage.TokenUsage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := NewStreamParser(tt.surface)
			for _, frame := range tt.frames {
				p.Feed(frame)
			}
			got, err := p.Close()
			if err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("usage = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestStreamParserErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		surface bedrockpath.APISurface
		frames  [][]byte
		want    error
	}{
		{
			name:    "missing usage",
			surface: bedrockpath.ConverseStream,
			frames:  [][]byte{BuildEventStreamFrameForTest(t, map[string]string{":event-type": "contentBlockDelta"}, []byte(`{"delta":{"text":"hi"}}`))},
			want:    usage.ErrUsageMissing,
		},
		{
			name:    "malformed payload json",
			surface: bedrockpath.ConverseStream,
			frames:  [][]byte{BuildEventStreamFrameForTest(t, map[string]string{":event-type": "metadata"}, []byte(`{"usage":`))},
			want:    usage.ErrMalformedJSON,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := NewStreamParser(tt.surface)
			for _, frame := range tt.frames {
				p.Feed(frame)
			}
			_, err := p.Close()
			if !errors.Is(err, tt.want) {
				t.Fatalf("Close() error = %v, want %v", err, tt.want)
			}
		})
	}
}
