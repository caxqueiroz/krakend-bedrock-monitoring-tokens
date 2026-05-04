package reporter

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"krakendBedRockPlugin/internal/usage"
)

type EMFConfig struct {
	Namespace           string
	Dimensions          []string
	ParseFailuresMetric string
}

type EMF struct {
	mu     sync.Mutex
	writer io.Writer
	cfg    EMFConfig
}

func NewEMF(w io.Writer, cfg EMFConfig) *EMF {
	if cfg.Namespace == "" {
		cfg.Namespace = "BedrockUsage"
	}
	if len(cfg.Dimensions) == 0 {
		cfg.Dimensions = []string{"UserKeyHash", "Model"}
	}
	if cfg.ParseFailuresMetric == "" {
		cfg.ParseFailuresMetric = "BedrockUsageParseFailures"
	}
	return &EMF{writer: defaultWriter(w), cfg: cfg}
}

func (r *EMF) Record(_ context.Context, u usage.Usage) error {
	timestamp := u.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	record := map[string]any{
		"_aws": map[string]any{
			"Timestamp": timestamp.UnixMilli(),
			"CloudWatchMetrics": []map[string]any{
				{
					"Namespace":  r.cfg.Namespace,
					"Dimensions": [][]string{r.cfg.Dimensions},
					"Metrics": []map[string]string{
						{"Name": "InputTokens", "Unit": "Count"},
						{"Name": "OutputTokens", "Unit": "Count"},
						{"Name": "TotalTokens", "Unit": "Count"},
						{"Name": r.cfg.ParseFailuresMetric, "Unit": "Count"},
					},
				},
			},
		},
		"UserKeyHash":  u.UserKeyHash,
		"Model":        u.Model,
		"ApiSurface":   u.APISurface,
		"InputTokens":  u.InputTokens,
		"OutputTokens": u.OutputTokens,
		"TotalTokens":  u.TotalTokens,
		"RequestId":    u.RequestID,
		"DurationMs":   u.DurationMs,
	}
	if u.ParseFailure {
		record[r.cfg.ParseFailuresMetric] = int64(1)
	} else {
		record[r.cfg.ParseFailuresMetric] = int64(0)
	}
	if u.ParseError != "" {
		record["ParseError"] = u.ParseError
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return json.NewEncoder(r.writer).Encode(record)
}

func (r *EMF) Close() error {
	return nil
}
