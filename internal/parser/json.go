package parser

import (
	"encoding/json"
	"fmt"

	"krakendBedRockPlugin/internal/bedrockpath"
	"krakendBedRockPlugin/internal/usage"
)

type JSONParser struct {
	surface      bedrockpath.APISurface
	maxBodyBytes int64
	body         []byte
	err          error
}

func NewJSONParser(surface bedrockpath.APISurface, maxBodyBytes int64) *JSONParser {
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}
	return &JSONParser{surface: surface, maxBodyBytes: maxBodyBytes}
}

func (p *JSONParser) Feed(b []byte) {
	if p.err != nil || len(b) == 0 {
		return
	}
	if int64(len(p.body)+len(b)) > p.maxBodyBytes {
		p.err = usage.ErrBodyTooLarge
		return
	}
	p.body = append(p.body, b...)
}

func (p *JSONParser) Close() (usage.TokenUsage, error) {
	if p.err != nil {
		return usage.TokenUsage{}, p.err
	}
	if len(p.body) == 0 {
		return usage.TokenUsage{}, usage.ErrUsageMissing
	}

	var value any
	if err := json.Unmarshal(p.body, &value); err != nil {
		return usage.TokenUsage{}, fmt.Errorf("%w: %v", usage.ErrMalformedJSON, err)
	}

	u, ok := extractUsageMap(asMap(value))
	if !ok {
		return usage.TokenUsage{}, usage.ErrUsageMissing
	}
	return u.WithTotal(), nil
}

func extractUsageMap(root map[string]any) (usage.TokenUsage, bool) {
	if root == nil {
		return usage.TokenUsage{}, false
	}

	if m := asMap(root["usage"]); m != nil {
		if u, ok := usageFromKeys(m, "inputTokens", "outputTokens", "totalTokens"); ok {
			return u, true
		}
		if u, ok := usageFromKeys(m, "prompt_tokens", "completion_tokens", "total_tokens"); ok {
			return u, true
		}
		if u, ok := usageFromKeys(m, "input_tokens", "output_tokens", "total_tokens"); ok {
			return u, true
		}
	}

	if u, ok := usageFromKeys(root, "prompt_token_count", "generation_token_count", ""); ok {
		return u, true
	}

	if input, ok := int64Value(root["inputTextTokenCount"]); ok {
		var output int64
		for _, item := range asSlice(root["results"]) {
			if n, ok := int64Value(asMap(item)["tokenCount"]); ok {
				output += n
			}
		}
		if output > 0 {
			return usage.TokenUsage{InputTokens: input, OutputTokens: output}.WithTotal(), true
		}
	}

	if meta := asMap(root["meta"]); meta != nil {
		if billed := asMap(meta["billed_units"]); billed != nil {
			if u, ok := usageFromKeys(billed, "input_tokens", "output_tokens", "total_tokens"); ok {
				return u, true
			}
		}
	}

	if metrics := asMap(root["amazon-bedrock-invocationMetrics"]); metrics != nil {
		if u, ok := usageFromKeys(metrics, "inputTokenCount", "outputTokenCount", ""); ok {
			return u, true
		}
	}

	return usage.TokenUsage{}, false
}

func usageFromKeys(m map[string]any, inputKey, outputKey, totalKey string) (usage.TokenUsage, bool) {
	input, inputOK := int64Value(m[inputKey])
	output, outputOK := int64Value(m[outputKey])
	var total int64
	if totalKey != "" {
		total, _ = int64Value(m[totalKey])
	}
	if !inputOK && !outputOK {
		return usage.TokenUsage{}, false
	}
	return usage.TokenUsage{InputTokens: input, OutputTokens: output, TotalTokens: total}.WithTotal(), true
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func int64Value(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}
