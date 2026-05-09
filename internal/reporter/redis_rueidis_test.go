package reporter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/rueidis"
)

func TestParseRueidisOptions(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantAddr    string
		wantUser    string
		wantPass    string
		wantDB      int
		wantTLS     bool
		wantErrFrag string
	}{
		{name: "minimal", url: "redis://localhost:6379", wantAddr: "localhost:6379"},
		{name: "with-db", url: "redis://localhost:6379/3", wantAddr: "localhost:6379", wantDB: 3},
		{
			name:     "with-auth-and-db",
			url:      "redis://carlos:s3cr3t@redis.example:6379/1",
			wantAddr: "redis.example:6379",
			wantUser: "carlos",
			wantPass: "s3cr3t",
			wantDB:   1,
		},
		{name: "rediss-tls", url: "rediss://redis.example:6380", wantAddr: "redis.example:6380", wantTLS: true},
		{name: "password-only", url: "redis://:nopass@host:6379", wantAddr: "host:6379", wantPass: "nopass"},
		{name: "bad-scheme", url: "http://localhost:6379", wantErrFrag: "unsupported"},
		{name: "bad-db", url: "redis://localhost:6379/notanumber", wantErrFrag: "parsing redis db"},
		{name: "malformed-url", url: "redis://%zz/", wantErrFrag: "parsing redis url"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := parseRueidisOptions(tc.url)
			if tc.wantErrFrag != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrFrag)
				}
				if !strings.Contains(err.Error(), tc.wantErrFrag) {
					t.Fatalf("error %q missing %q", err, tc.wantErrFrag)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(opts.InitAddress) != 1 || opts.InitAddress[0] != tc.wantAddr {
				t.Errorf("InitAddress = %v, want [%s]", opts.InitAddress, tc.wantAddr)
			}
			if opts.Username != tc.wantUser {
				t.Errorf("Username = %q, want %q", opts.Username, tc.wantUser)
			}
			if opts.Password != tc.wantPass {
				t.Errorf("Password = %q, want %q", opts.Password, tc.wantPass)
			}
			if opts.SelectDB != tc.wantDB {
				t.Errorf("SelectDB = %d, want %d", opts.SelectDB, tc.wantDB)
			}
			if (opts.TLSConfig != nil) != tc.wantTLS {
				t.Errorf("TLS configured = %v, want %v", opts.TLSConfig != nil, tc.wantTLS)
			}
			if opts.DisableCache != true {
				t.Errorf("DisableCache should be true for write-only reporter")
			}
		})
	}
}

func TestRueidisClientExpireRejectsNonPositiveTTL(t *testing.T) {
	c := &RueidisClient{}
	for _, ttl := range []time.Duration{0, -time.Second, -time.Hour} {
		if err := c.Expire(context.Background(), "k", ttl); err == nil {
			t.Errorf("ttl=%v: expected error, got nil", ttl)
		}
	}
}

func newMiniRedisClient(t *testing.T) (*RueidisClient, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress:  []string{mr.Addr()},
		DisableCache: true,
	})
	if err != nil {
		t.Fatalf("rueidis.NewClient: %v", err)
	}
	t.Cleanup(c.Close)
	return &RueidisClient{client: c}, mr
}

func TestRueidisClientHIncrByAccumulates(t *testing.T) {
	c, mr := newMiniRedisClient(t)
	ctx := context.Background()
	const key = "bedrock:usage:abc"

	for _, v := range []int64{7, 3, 5} {
		if err := c.HIncrBy(ctx, key, "input_tokens", v); err != nil {
			t.Fatalf("HIncrBy: %v", err)
		}
	}
	if got := mr.HGet(key, "input_tokens"); got != "15" {
		t.Errorf("HGet = %q, want %q", got, "15")
	}
}

func TestRueidisClientExpireSetsTTL(t *testing.T) {
	c, mr := newMiniRedisClient(t)
	ctx := context.Background()
	const key = "bedrock:usage:ttl"

	mr.HSet(key, "x", "1")
	if err := c.Expire(ctx, key, 5*time.Minute); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if got := mr.TTL(key); got != 5*time.Minute {
		t.Errorf("TTL = %v, want %v", got, 5*time.Minute)
	}
}

func TestRueidisClientCloseIsIdempotent(t *testing.T) {
	c, _ := newMiniRedisClient(t)
	if err := c.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	// rueidis.Client.Close is idempotent; calling our wrapper twice must not error or panic.
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
