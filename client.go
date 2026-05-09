package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const ClientPluginName = "bedrock-usage-sigv4"

var ClientRegisterer = clientRegisterer(ClientPluginName)

type clientRegisterer string

func (r clientRegisterer) RegisterClients(
	register func(name string, handler func(context.Context, map[string]interface{}) (http.Handler, error)),
) {
	register(string(r), NewClient)
}

type sigv4Config struct {
	region        string
	service       string
	host          string
	assumeRoleARN string
	stsRegion     string
	externalID    string
	sessionName   string
}

func NewClient(ctx context.Context, raw map[string]any) (http.Handler, error) {
	cfg, err := parseSigV4Config(raw)
	if err != nil {
		return nil, err
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.region))
	if err != nil {
		return nil, fmt.Errorf("%s: load aws config: %w", ClientPluginName, err)
	}

	if cfg.assumeRoleARN != "" {
		stsRegion := cfg.stsRegion
		if stsRegion == "" {
			stsRegion = cfg.region
		}
		stsCfg := awsCfg.Copy()
		stsCfg.Region = stsRegion
		stsClient := sts.NewFromConfig(stsCfg)

		provider := stscreds.NewAssumeRoleProvider(stsClient, cfg.assumeRoleARN, func(o *stscreds.AssumeRoleOptions) {
			if cfg.externalID != "" {
				o.ExternalID = aws.String(cfg.externalID)
			}
			if cfg.sessionName != "" {
				o.RoleSessionName = cfg.sessionName
			} else {
				o.RoleSessionName = "krakend-" + ClientPluginName
			}
		})
		awsCfg.Credentials = aws.NewCredentialsCache(provider)
	}

	host := cfg.host
	if host == "" {
		host = cfg.service + "." + cfg.region + ".amazonaws.com"
	}

	signer := v4.NewSigner()
	httpClient := &http.Client{}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := signAndForward(r.Context(), signer, awsCfg.Credentials, httpClient, cfg.service, cfg.region, host, w, r); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
	}), nil
}

func signAndForward(
	ctx context.Context,
	signer *v4.Signer,
	creds aws.CredentialsProvider,
	client *http.Client,
	service, region, host string,
	w http.ResponseWriter,
	r *http.Request,
) error {
	var bodyBytes []byte
	if r.Body != nil {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return fmt.Errorf("read request body: %w", err)
		}
		bodyBytes = b
		_ = r.Body.Close()
	}
	sum := sha256.Sum256(bodyBytes)
	payloadHash := hex.EncodeToString(sum[:])

	out := r.Clone(ctx)
	out.URL.Scheme = "https"
	out.URL.Host = host
	out.Host = host
	out.RequestURI = ""
	out.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	out.ContentLength = int64(len(bodyBytes))
	stripRequestHopByHop(out.Header)

	resolved, err := creds.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}
	if err := signer.SignHTTP(ctx, resolved, out, payloadHash, service, region, time.Now().UTC()); err != nil {
		return fmt.Errorf("sign request: %w", err)
	}

	resp, err := client.Do(out)
	if err != nil {
		return fmt.Errorf("forward request: %w", err)
	}
	defer resp.Body.Close()

	copyResponseHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("stream response: %w", err)
	}
	return nil
}

func parseSigV4Config(raw map[string]any) (sigv4Config, error) {
	src := raw
	if nested := mapValue(raw["plugin/http-client"]); nested != nil {
		src = nested
	}

	cfg := sigv4Config{
		region:        stringValue(src["region"]),
		service:       stringValue(src["service"]),
		host:          stringValue(src["host"]),
		assumeRoleARN: stringValue(src["assume_role_arn"]),
		stsRegion:     stringValue(src["sts_region"]),
		externalID:    stringValue(src["external_id"]),
		sessionName:   stringValue(src["session_name"]),
	}
	if cfg.region == "" {
		return cfg, fmt.Errorf("%s: region is required", ClientPluginName)
	}
	if cfg.service == "" {
		return cfg, fmt.Errorf("%s: service is required", ClientPluginName)
	}
	return cfg, nil
}

func stripRequestHopByHop(h http.Header) {
	for _, k := range []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
		"Authorization",
		"X-Amz-Date",
		"X-Amz-Security-Token",
		"X-Amz-Content-Sha256",
	} {
		h.Del(k)
	}
}

func copyResponseHeader(dst, src http.Header) {
	for k, vs := range src {
		if strings.EqualFold(k, "Connection") || strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
