package reporter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"krakendBedRockPlugin/internal/usage"
)

type RedisConfig struct {
	URL         string
	KeyPrefix   string
	FieldFormat string
	TTLDays     int
}

type RedisClient interface {
	HIncrBy(ctx context.Context, key, field string, value int64) error
	Expire(ctx context.Context, key string, ttl time.Duration) error
	Close() error
}

type Redis struct {
	client RedisClient
	cfg    RedisConfig
	now    func() time.Time
}

type redisIncrement struct {
	key   string
	field string
	value int64
}

func NewRedisWithClient(client RedisClient, cfg RedisConfig) *Redis {
	cfg = normalizeRedisConfig(cfg)
	return &Redis{client: client, cfg: cfg, now: time.Now}
}

func NewRedis(cfg RedisConfig) (*Redis, error) {
	cfg = normalizeRedisConfig(cfg)
	if cfg.URL == "" {
		return nil, errors.New("redis url is required")
	}
	client, err := NewRueidisClient(cfg.URL)
	if err != nil {
		return nil, err
	}
	return NewRedisWithClient(client, cfg), nil
}

func (r *Redis) Record(ctx context.Context, u usage.Usage) error {
	if r.client == nil {
		return errors.New("redis client is nil")
	}
	timestamp := u.Timestamp
	if timestamp.IsZero() {
		timestamp = r.now().UTC()
	}
	key := r.cfg.KeyPrefix + ":" + u.UserKeyHash
	for _, inc := range []redisIncrement{
		{key: key, field: redisField(timestamp, r.cfg.FieldFormat, "input_tokens"), value: u.InputTokens},
		{key: key, field: redisField(timestamp, r.cfg.FieldFormat, "output_tokens"), value: u.OutputTokens},
		{key: key, field: redisField(timestamp, r.cfg.FieldFormat, "total_tokens"), value: u.TotalTokens},
	} {
		if err := r.client.HIncrBy(ctx, inc.key, inc.field, inc.value); err != nil {
			return err
		}
	}
	if r.cfg.TTLDays > 0 {
		if err := r.client.Expire(ctx, key, time.Duration(r.cfg.TTLDays)*24*time.Hour); err != nil {
			return err
		}
	}
	return nil
}

func (r *Redis) Close() error {
	if r.client == nil {
		return nil
	}
	return r.client.Close()
}

func normalizeRedisConfig(cfg RedisConfig) RedisConfig {
	if cfg.KeyPrefix == "" {
		cfg.KeyPrefix = "bedrock:usage"
	}
	if strings.Count(cfg.FieldFormat, "%s") != 1 {
		cfg.FieldFormat = "2006-01-02:%s"
	}
	return cfg
}

func redisField(t time.Time, format, metric string) string {
	return fmt.Sprintf(t.UTC().Format(format), metric)
}
