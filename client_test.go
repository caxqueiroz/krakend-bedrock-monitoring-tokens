package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

func TestParseSigV4Config(t *testing.T) {
	tests := []struct {
		name    string
		raw     map[string]any
		want    sigv4Config
		wantErr bool
	}{
		{
			name:    "missing region",
			raw:     map[string]any{"service": "bedrock-runtime"},
			wantErr: true,
		},
		{
			name:    "missing service",
			raw:     map[string]any{"region": "us-east-1"},
			wantErr: true,
		},
		{
			name: "minimal flat",
			raw: map[string]any{
				"region":  "us-east-1",
				"service": "bedrock-runtime",
			},
			want: sigv4Config{region: "us-east-1", service: "bedrock-runtime"},
		},
		{
			name: "nested under plugin/http-client",
			raw: map[string]any{
				"plugin/http-client": map[string]any{
					"region":  "eu-west-1",
					"service": "bedrock",
					"host":    "vpce-bedrock.example",
				},
			},
			want: sigv4Config{region: "eu-west-1", service: "bedrock", host: "vpce-bedrock.example"},
		},
		{
			name: "full assume role",
			raw: map[string]any{
				"region":          "us-east-1",
				"service":         "bedrock-runtime",
				"assume_role_arn": "arn:aws:iam::123:role/x",
				"sts_region":      "us-west-2",
				"external_id":     "ext",
				"session_name":    "s",
			},
			want: sigv4Config{
				region:        "us-east-1",
				service:       "bedrock-runtime",
				assumeRoleARN: "arn:aws:iam::123:role/x",
				stsRegion:     "us-west-2",
				externalID:    "ext",
				sessionName:   "s",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSigV4Config(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

type capturedRequest struct {
	method string
	host   string
	path   string
	header http.Header
	body   []byte
}

func newSignedUpstream(t *testing.T, status int, respBody []byte) (*httptest.Server, *capturedRequest) {
	t.Helper()
	cap := &capturedRequest{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cap.method = r.Method
		cap.host = r.Host
		cap.path = r.URL.Path
		cap.header = r.Header.Clone()
		cap.body = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func staticCreds() aws.CredentialsProvider {
	return credentials.NewStaticCredentialsProvider("AKIDTEST", "SECRETTEST", "")
}

func hostFromURL(t *testing.T, urlStr string) string {
	t.Helper()
	for _, scheme := range []string{"https://", "http://"} {
		if rest, ok := strings.CutPrefix(urlStr, scheme); ok {
			return rest
		}
	}
	t.Fatalf("unexpected server URL: %s", urlStr)
	return ""
}

func TestSignAndForwardSignsRequestAndStreamsResponse(t *testing.T) {
	srv, got := newSignedUpstream(t, http.StatusOK, []byte(`{"ok":true}`))
	host := hostFromURL(t, srv.URL)

	req := httptest.NewRequest(http.MethodPost,
		"https://placeholder.invalid/model/foo/invoke",
		bytes.NewBufferString(`{"prompt":"hi"}`))
	rr := httptest.NewRecorder()

	err := signAndForward(
		context.Background(), v4.NewSigner(), staticCreds(), srv.Client(),
		"bedrock-runtime", "us-east-1", host, rr, req,
	)
	if err != nil {
		t.Fatalf("signAndForward: %v", err)
	}

	if got.method != http.MethodPost {
		t.Errorf("upstream method = %q, want POST", got.method)
	}
	if got.path != "/model/foo/invoke" {
		t.Errorf("upstream path = %q", got.path)
	}
	if string(got.body) != `{"prompt":"hi"}` {
		t.Errorf("upstream body = %q", got.body)
	}
	if got.host != host {
		t.Errorf("upstream Host = %q, want %q", got.host, host)
	}

	auth := got.header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		t.Errorf("Authorization missing AWS4-HMAC-SHA256 prefix: %q", auth)
	}
	if !strings.Contains(auth, "Credential=AKIDTEST/") {
		t.Errorf("Authorization missing access key id: %q", auth)
	}
	if !strings.Contains(auth, "/us-east-1/bedrock-runtime/aws4_request") {
		t.Errorf("Authorization missing scope: %q", auth)
	}
	if got.header.Get("X-Amz-Date") == "" {
		t.Error("X-Amz-Date missing")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("downstream status = %d", rr.Code)
	}
	if rr.Body.String() != `{"ok":true}` {
		t.Errorf("downstream body = %q", rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("downstream Content-Type = %q", ct)
	}
}

func TestSignAndForwardStripsHopByHopAndInboundAuth(t *testing.T) {
	srv, got := newSignedUpstream(t, http.StatusOK, []byte(`{}`))
	host := hostFromURL(t, srv.URL)

	req := httptest.NewRequest(http.MethodPost,
		"https://placeholder.invalid/x", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer client-token")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("X-Amz-Date", "20000101T000000Z")
	req.Header.Set("X-Custom", "keep-me")

	rr := httptest.NewRecorder()
	if err := signAndForward(
		context.Background(), v4.NewSigner(), staticCreds(), srv.Client(),
		"bedrock-runtime", "us-east-1", host, rr, req,
	); err != nil {
		t.Fatalf("signAndForward: %v", err)
	}

	auth := got.header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("Authorization not replaced with SigV4: %q", auth)
	}
	if strings.Contains(auth, "Bearer client-token") {
		t.Errorf("inbound bearer token leaked into upstream: %q", auth)
	}
	if c := got.header.Get("Connection"); c != "" && !strings.EqualFold(c, "close") {
		t.Errorf("Connection header should not propagate, got %q", c)
	}
	if got.header.Get("X-Amz-Date") == "20000101T000000Z" {
		t.Error("attacker X-Amz-Date forwarded to upstream")
	}
	if got.header.Get("X-Custom") != "keep-me" {
		t.Error("non-hop-by-hop header was lost")
	}
}

func TestSignAndForwardEmptyBody(t *testing.T) {
	srv, got := newSignedUpstream(t, http.StatusOK, []byte(`{}`))
	host := hostFromURL(t, srv.URL)

	req := httptest.NewRequest(http.MethodGet, "https://placeholder.invalid/ping", nil)
	rr := httptest.NewRecorder()
	if err := signAndForward(
		context.Background(), v4.NewSigner(), staticCreds(), srv.Client(),
		"bedrock-runtime", "us-east-1", host, rr, req,
	); err != nil {
		t.Fatalf("signAndForward: %v", err)
	}

	if len(got.body) != 0 {
		t.Errorf("upstream body should be empty: %q", got.body)
	}
	if !strings.HasPrefix(got.header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
		t.Errorf("empty-body request was not signed: %q", got.header.Get("Authorization"))
	}
}

func TestSignAndForwardStreamsLargeResponse(t *testing.T) {
	const chunks = 32
	const chunkSize = 16 * 1024
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := range chunks {
			_, _ = w.Write(bytes.Repeat([]byte{byte('a' + i%26)}, chunkSize))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	host := hostFromURL(t, srv.URL)

	req := httptest.NewRequest(http.MethodPost,
		"https://placeholder.invalid/stream", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	if err := signAndForward(
		context.Background(), v4.NewSigner(), staticCreds(), srv.Client(),
		"bedrock-runtime", "us-east-1", host, rr, req,
	); err != nil {
		t.Fatalf("signAndForward: %v", err)
	}
	if rr.Body.Len() != chunks*chunkSize {
		t.Errorf("downstream bytes = %d, want %d", rr.Body.Len(), chunks*chunkSize)
	}
}

type erroringCreds struct{ err error }

func (e erroringCreds) Retrieve(ctx context.Context) (aws.Credentials, error) {
	return aws.Credentials{}, e.err
}

func TestSignAndForwardCredentialError(t *testing.T) {
	srv, _ := newSignedUpstream(t, http.StatusOK, []byte(`{}`))
	host := hostFromURL(t, srv.URL)

	req := httptest.NewRequest(http.MethodPost,
		"https://placeholder.invalid/", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	err := signAndForward(
		context.Background(), v4.NewSigner(), erroringCreds{err: errors.New("boom")},
		srv.Client(), "bedrock-runtime", "us-east-1", host, rr, req,
	)
	if err == nil {
		t.Fatal("expected error from signAndForward")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err missing inner cause: %v", err)
	}
}
