package reporter

import (
	"context"
	"io"
	"log/slog"

	"krakendBedRockPlugin/internal/usage"
)

type Stdout struct {
	logger *slog.Logger
}

func NewStdout(w io.Writer) *Stdout {
	handler := slog.NewJSONHandler(defaultWriter(w), &slog.HandlerOptions{Level: slog.LevelInfo})
	return &Stdout{logger: slog.New(handler)}
}

func (r *Stdout) Record(ctx context.Context, u usage.Usage) error {
	r.logger.InfoContext(ctx, "bedrock_usage",
		"user_key_hash", u.UserKeyHash,
		"model", u.Model,
		"api_surface", u.APISurface,
		"input_tokens", u.InputTokens,
		"output_tokens", u.OutputTokens,
		"total_tokens", u.TotalTokens,
		"duration_ms", u.DurationMs,
		"request_id", u.RequestID,
		"parse_error", u.ParseError,
		"parse_failure", u.ParseFailure,
		"status_code", u.StatusCode,
	)
	return nil
}

func (r *Stdout) Close() error {
	return nil
}
