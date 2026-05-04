package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

func hashKey(t *testing.T, key string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func TestExtract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		headers   http.Header
		keys      []string
		wantHash  string
		wantSrc   string
		anonymous bool
	}{
		{
			name: "x api key",
			headers: http.Header{
				"X-Api-Key": []string{"secret-key"},
			},
			keys:     []string{"x-api-key", "Authorization"},
			wantHash: hashKey(t, "secret-key"),
			wantSrc:  "x-api-key",
		},
		{
			name: "bearer authorization strips scheme",
			headers: http.Header{
				"Authorization": []string{"bEaReR bearer-key"},
			},
			keys:     []string{"x-api-key", "Authorization"},
			wantHash: hashKey(t, "bearer-key"),
			wantSrc:  "Authorization",
		},
		{
			name: "configured priority wins",
			headers: http.Header{
				"X-Api-Key":     []string{"secret-key"},
				"Authorization": []string{"Bearer bearer-key"},
			},
			keys:     []string{"Authorization", "x-api-key"},
			wantHash: hashKey(t, "bearer-key"),
			wantSrc:  "Authorization",
		},
		{
			name: "header lookup is case insensitive",
			headers: http.Header{
				"X-Api-Key": []string{"secret-key"},
			},
			keys:     []string{"X-API-KEY"},
			wantHash: hashKey(t, "secret-key"),
			wantSrc:  "X-API-KEY",
		},
		{
			name: "empty bearer value is anonymous",
			headers: http.Header{
				"Authorization": []string{"Bearer   "},
			},
			keys:      []string{"Authorization"},
			wantHash:  "anonymous",
			wantSrc:   "",
			anonymous: true,
		},
		{
			name:      "missing key is anonymous",
			headers:   http.Header{},
			keys:      []string{"x-api-key", "Authorization"},
			wantHash:  "anonymous",
			wantSrc:   "",
			anonymous: true,
		},
		{
			name: "empty configured keys uses defaults",
			headers: http.Header{
				"X-Api-Key": []string{"secret-key"},
			},
			keys:     nil,
			wantHash: hashKey(t, "secret-key"),
			wantSrc:  "x-api-key",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Extract(tt.headers, tt.keys)
			if got.Hash != tt.wantHash {
				t.Fatalf("Hash = %q, want %q", got.Hash, tt.wantHash)
			}
			if got.Source != tt.wantSrc {
				t.Fatalf("Source = %q, want %q", got.Source, tt.wantSrc)
			}
			if got.Anonymous != tt.anonymous {
				t.Fatalf("Anonymous = %v, want %v", got.Anonymous, tt.anonymous)
			}
		})
	}
}
