package parser

import (
	"encoding/json"
	"fmt"

	"krakendBedRockPlugin/internal/bedrockpath"
	"krakendBedRockPlugin/internal/usage"
)

type StreamParser struct {
	surface bedrockpath.APISurface
	decoder *EventStreamDecoder
	tokens  usage.TokenUsage
	err     error
}

func NewStreamParser(surface bedrockpath.APISurface) *StreamParser {
	p := &StreamParser{surface: surface}
	p.decoder = NewEventStreamDecoder(p.onFrame)
	return p
}

func (p *StreamParser) Feed(b []byte) {
	if p.err != nil || len(b) == 0 {
		return
	}
	p.decoder.Feed(b)
}

func (p *StreamParser) Close() (usage.TokenUsage, error) {
	if p.err != nil {
		return usage.TokenUsage{}, p.err
	}
	if err := p.decoder.Close(); err != nil {
		return usage.TokenUsage{}, err
	}
	if p.tokens.Empty() {
		return usage.TokenUsage{}, usage.ErrUsageMissing
	}
	return p.tokens.WithTotal(), nil
}

func (p *StreamParser) onFrame(frame Frame) error {
	switch p.surface {
	case bedrockpath.ConverseStream:
		if frame.Headers[":event-type"] != "metadata" {
			return nil
		}
		tokens, err := decodePayloadUsage(frame.Payload)
		if err != nil {
			p.err = err
			return err
		}
		p.tokens = tokens
		return nil
	case bedrockpath.InvokeModelWithResponseStream:
		tokens, ok, err := decodeInvokeStreamUsage(frame.Payload)
		if err != nil {
			p.err = err
			return err
		}
		if ok {
			if tokens.InputTokens != 0 {
				p.tokens.InputTokens = tokens.InputTokens
			}
			if tokens.OutputTokens != 0 {
				p.tokens.OutputTokens = tokens.OutputTokens
			}
			if tokens.TotalTokens != 0 {
				p.tokens.TotalTokens = tokens.TotalTokens
			}
		}
		return nil
	default:
		return nil
	}
}

func decodePayloadUsage(payload []byte) (usage.TokenUsage, error) {
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return usage.TokenUsage{}, fmt.Errorf("%w: %v", usage.ErrMalformedJSON, err)
	}
	u, ok := extractUsageMap(asMap(value))
	if !ok {
		return usage.TokenUsage{}, usage.ErrUsageMissing
	}
	return u.WithTotal(), nil
}

func decodeInvokeStreamUsage(payload []byte) (usage.TokenUsage, bool, error) {
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return usage.TokenUsage{}, false, fmt.Errorf("%w: %v", usage.ErrMalformedJSON, err)
	}
	root := asMap(value)
	if root == nil {
		return usage.TokenUsage{}, false, nil
	}

	if metrics := asMap(root["amazon-bedrock-invocationMetrics"]); metrics != nil {
		u, ok := usageFromKeys(metrics, "inputTokenCount", "outputTokenCount", "")
		return u, ok, nil
	}

	switch root["type"] {
	case "message_start":
		if message := asMap(root["message"]); message != nil {
			if usageMap := asMap(message["usage"]); usageMap != nil {
				input, inputOK := int64Value(usageMap["input_tokens"])
				output, outputOK := int64Value(usageMap["output_tokens"])
				if inputOK || outputOK {
					return usage.TokenUsage{InputTokens: input, OutputTokens: output}, true, nil
				}
			}
		}
	case "message_delta":
		if usageMap := asMap(root["usage"]); usageMap != nil {
			input, inputOK := int64Value(usageMap["input_tokens"])
			output, outputOK := int64Value(usageMap["output_tokens"])
			if inputOK || outputOK {
				return usage.TokenUsage{InputTokens: input, OutputTokens: output}, true, nil
			}
		}
	}

	if u, ok := extractUsageMap(root); ok {
		return u, true, nil
	}

	return usage.TokenUsage{}, false, nil
}
