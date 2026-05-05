// Fake Bedrock backend for end-to-end testing of the bedrock-usage plugin.
// Returns canned responses with non-zero usage so the plugin has real
// numbers to extract. Listens on :9000 by default; override with PORT=...
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	canonicalUsageInput  = 12
	canonicalUsageOutput = 4
	canonicalUsageTotal  = 16
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9000"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", route)
	addr := ":" + port
	log.Printf("fake-bedrock listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func route(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Path
	model := extractModel(path)
	switch {
	case strings.HasSuffix(path, "/converse"):
		converse(w, model)
	case strings.HasSuffix(path, "/converse-stream"):
		converseStream(w, model)
	case strings.HasSuffix(path, "/invoke-with-response-stream"):
		invokeStream(w, model)
	case strings.HasSuffix(path, "/invoke"):
		invoke(w, model)
	default:
		http.NotFound(w, r)
	}
}

// /model/<model>/<surface> → <model>
func extractModel(p string) string {
	p = strings.TrimPrefix(p, "/")
	parts := strings.Split(p, "/")
	if len(parts) >= 3 && parts[0] == "model" {
		return parts[1]
	}
	return ""
}

func converse(w http.ResponseWriter, model string) {
	body := map[string]any{
		"output": map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": []map[string]string{{"text": "Hello from fake Bedrock"}},
			},
		},
		"stopReason": "end_turn",
		"usage": map[string]int{
			"inputTokens":  canonicalUsageInput,
			"outputTokens": canonicalUsageOutput,
			"totalTokens":  canonicalUsageTotal,
		},
		"metrics": map[string]int{"latencyMs": 100},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func invoke(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "application/json")
	var body map[string]any
	switch {
	case strings.HasPrefix(model, "anthropic."):
		body = map[string]any{
			"id":          "msg_fake",
			"type":        "message",
			"role":        "assistant",
			"content":     []map[string]string{{"type": "text", "text": "Hello"}},
			"stop_reason": "end_turn",
			"model":       model,
			"usage":       map[string]int{"input_tokens": canonicalUsageInput, "output_tokens": canonicalUsageOutput},
		}
	default:
		// Nova / generic Converse-shaped usage
		body = map[string]any{
			"output": map[string]any{
				"message": map[string]any{
					"role":    "assistant",
					"content": []map[string]string{{"text": "Hello"}},
				},
			},
			"stopReason": "end_turn",
			"usage": map[string]int{
				"inputTokens":  canonicalUsageInput,
				"outputTokens": canonicalUsageOutput,
				"totalTokens":  canonicalUsageTotal,
			},
		}
	}
	_ = json.NewEncoder(w).Encode(body)
}

func converseStream(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	w.WriteHeader(http.StatusOK)
	// Single metadata event with usage block matches the ConverseStream parser.
	payload, _ := json.Marshal(map[string]any{
		"usage": map[string]int{
			"inputTokens":  canonicalUsageInput,
			"outputTokens": canonicalUsageOutput,
			"totalTokens":  canonicalUsageTotal,
		},
		"metrics": map[string]int{"latencyMs": 100},
	})
	frame := encodeEventStreamFrame("metadata", payload)
	_, _ = w.Write(frame)
	flush(w)
}

func invokeStream(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	w.WriteHeader(http.StatusOK)
	// Anthropic-style streaming uses message_stop with amazon-bedrock-invocationMetrics
	// fallback. Emit that envelope so the stream parser can extract tokens.
	payload, _ := json.Marshal(map[string]any{
		"type": "message_stop",
		"amazon-bedrock-invocationMetrics": map[string]int{
			"inputTokenCount":  canonicalUsageInput,
			"outputTokenCount": canonicalUsageOutput,
		},
	})
	frame := encodeEventStreamFrame("chunk", wrapBedrockChunk(payload))
	_, _ = w.Write(frame)
	flush(w)
}

// Bedrock InvokeModelWithResponseStream wraps the model payload as
// {"bytes":"<base64 payload>"} inside each chunk-typed frame. Go's json
// package already base64-encodes []byte values, so wrapping with json.Marshal
// produces the correct shape automatically.
func wrapBedrockChunk(payload []byte) []byte {
	envelope := map[string]any{"bytes": payload}
	out, _ := json.Marshal(envelope)
	return out
}

// AWS event-stream frame:
//
//	prelude:  total_len(4) + headers_len(4) + prelude_crc32(4)
//	headers:  for each header: name_len(1) + name + value_type(1) + value_len(2) + value
//	payload:  raw bytes
//	message_crc32(4)
func encodeEventStreamFrame(eventType string, payload []byte) []byte {
	headers := encodeHeader(":event-type", eventType)
	headers = append(headers, encodeHeader(":content-type", "application/json")...)
	headers = append(headers, encodeHeader(":message-type", "event")...)

	totalLen := 12 + len(headers) + len(payload) + 4
	preludeAndHeaders := make([]byte, 0, totalLen-4)

	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(prelude[4:8], uint32(len(headers)))
	preludeAndHeaders = append(preludeAndHeaders, prelude...)

	preludeCRC := crc32.ChecksumIEEE(prelude)
	crcBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(crcBytes, preludeCRC)
	preludeAndHeaders = append(preludeAndHeaders, crcBytes...)

	preludeAndHeaders = append(preludeAndHeaders, headers...)
	preludeAndHeaders = append(preludeAndHeaders, payload...)

	msgCRC := crc32.ChecksumIEEE(preludeAndHeaders)
	binary.BigEndian.PutUint32(crcBytes, msgCRC)

	var buf bytes.Buffer
	buf.Write(preludeAndHeaders)
	buf.Write(crcBytes)
	return buf.Bytes()
}

func encodeHeader(name, value string) []byte {
	var buf bytes.Buffer
	buf.WriteByte(byte(len(name)))
	buf.WriteString(name)
	buf.WriteByte(7) // value type 7 = string
	lenBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBytes, uint16(len(value)))
	buf.Write(lenBytes)
	buf.WriteString(value)
	return buf.Bytes()
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
