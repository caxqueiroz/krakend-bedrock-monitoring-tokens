package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

var defaultKeyHeaders = []string{"x-api-key", "Authorization"}

type Result struct {
	Hash      string
	Source    string
	Anonymous bool
}

func Extract(headers http.Header, keyHeaders []string) Result {
	if len(keyHeaders) == 0 {
		keyHeaders = defaultKeyHeaders
	}

	for _, key := range keyHeaders {
		value := strings.TrimSpace(headers.Get(key))
		if value == "" {
			continue
		}
		if strings.EqualFold(key, "Authorization") {
			value = bearerValue(value)
			if value == "" {
				continue
			}
		}
		return Result{
			Hash:   hash(value),
			Source: key,
		}
	}

	return Result{Hash: "anonymous", Anonymous: true}
}

func bearerValue(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	if len(fields) >= 2 && strings.EqualFold(fields[0], "Bearer") {
		return strings.Join(fields[1:], " ")
	}
	if len(fields) == 1 && !strings.EqualFold(fields[0], "Bearer") {
		return fields[0]
	}
	return ""
}

func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
