package parser

import (
	"errors"
	"strings"
	"testing"

	"krakendBedRockPlugin/internal/bedrockpath"
	"krakendBedRockPlugin/internal/usage"
)

func TestJSONParserExtractsUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		surface bedrockpath.APISurface
		body    string
		want    usage.TokenUsage
	}{
		{
			name:    "converse usage",
			surface: bedrockpath.Converse,
			body:    `{"usage":{"inputTokens":12,"outputTokens":8,"totalTokens":20}}`,
			want:    usage.TokenUsage{InputTokens: 12, OutputTokens: 8, TotalTokens: 20},
		},
		{
			name:    "anthropic messages usage",
			surface: bedrockpath.InvokeModel,
			body:    `{"id":"msg","usage":{"input_tokens":14,"output_tokens":9}}`,
			want:    usage.TokenUsage{InputTokens: 14, OutputTokens: 9, TotalTokens: 23},
		},
		{
			name:    "llama token counts",
			surface: bedrockpath.InvokeModel,
			body:    `{"prompt_token_count":101,"generation_token_count":33}`,
			want:    usage.TokenUsage{InputTokens: 101, OutputTokens: 33, TotalTokens: 134},
		},
		{
			name:    "titan token counts",
			surface: bedrockpath.InvokeModel,
			body:    `{"inputTextTokenCount":7,"results":[{"tokenCount":2},{"tokenCount":3}]}`,
			want:    usage.TokenUsage{InputTokens: 7, OutputTokens: 5, TotalTokens: 12},
		},
		{
			name:    "cohere billed units",
			surface: bedrockpath.InvokeModel,
			body:    `{"meta":{"billed_units":{"input_tokens":5,"output_tokens":6}}}`,
			want:    usage.TokenUsage{InputTokens: 5, OutputTokens: 6, TotalTokens: 11},
		},
		{
			name:    "ai21 usage",
			surface: bedrockpath.InvokeModel,
			body:    `{"usage":{"prompt_tokens":17,"completion_tokens":19,"total_tokens":36}}`,
			want:    usage.TokenUsage{InputTokens: 17, OutputTokens: 19, TotalTokens: 36},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := NewJSONParser(tt.surface, 1024)
			p.Feed([]byte(tt.body))
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

func TestJSONParserErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		cap  int64
		want error
	}{
		{name: "missing usage", body: `{"output":"hello"}`, cap: 1024, want: usage.ErrUsageMissing},
		{name: "empty body", body: ``, cap: 1024, want: usage.ErrUsageMissing},
		{name: "malformed json", body: `{"usage":`, cap: 1024, want: usage.ErrMalformedJSON},
		{name: "body too large", body: strings.Repeat("x", 8), cap: 4, want: usage.ErrBodyTooLarge},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := NewJSONParser(bedrockpath.InvokeModel, tt.cap)
			p.Feed([]byte(tt.body))
			_, err := p.Close()
			if !errors.Is(err, tt.want) {
				t.Fatalf("Close() error = %v, want %v", err, tt.want)
			}
		})
	}
}
