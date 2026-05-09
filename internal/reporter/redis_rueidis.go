package reporter

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/redis/rueidis"
)

type RueidisClient struct {
	client rueidis.Client
}

func NewRueidisClient(rawURL string) (*RueidisClient, error) {
	opts, err := parseRueidisOptions(rawURL)
	if err != nil {
		return nil, err
	}
	c, err := rueidis.NewClient(opts)
	if err != nil {
		return nil, fmt.Errorf("connect redis: %w", err)
	}
	return &RueidisClient{client: c}, nil
}

func parseRueidisOptions(rawURL string) (rueidis.ClientOption, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rueidis.ClientOption{}, fmt.Errorf("parsing redis url: %w", err)
	}
	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return rueidis.ClientOption{}, fmt.Errorf("unsupported redis scheme %q", u.Scheme)
	}
	opts := rueidis.ClientOption{
		InitAddress:  []string{u.Host},
		DisableCache: true,
	}
	if u.Scheme == "rediss" {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if u.User != nil {
		opts.Username = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			opts.Password = pw
		}
	}
	if path := strings.TrimPrefix(u.Path, "/"); path != "" {
		db, err := strconv.Atoi(path)
		if err != nil {
			return rueidis.ClientOption{}, fmt.Errorf("parsing redis db: %w", err)
		}
		opts.SelectDB = db
	}
	return opts, nil
}

func (c *RueidisClient) HIncrBy(ctx context.Context, key, field string, value int64) error {
	cmd := c.client.B().Hincrby().Key(key).Field(field).Increment(value).Build()
	return c.client.Do(ctx, cmd).Error()
}

func (c *RueidisClient) Expire(ctx context.Context, key string, ttl time.Duration) error {
	seconds := int64(ttl.Seconds())
	if seconds <= 0 {
		return errors.New("ttl must be positive")
	}
	cmd := c.client.B().Expire().Key(key).Seconds(seconds).Build()
	return c.client.Do(ctx, cmd).Error()
}

func (c *RueidisClient) Close() error {
	c.client.Close()
	return nil
}
