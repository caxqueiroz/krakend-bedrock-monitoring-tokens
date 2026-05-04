package reporter

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type NetRedisClient struct {
	addr     string
	username string
	password string
	db       int
	useTLS   bool
	timeout  time.Duration
}

func NewNetRedisClient(rawURL string) (*NetRedisClient, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parsing redis url: %w", err)
	}
	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return nil, fmt.Errorf("unsupported redis scheme %q", u.Scheme)
	}
	db := 0
	if path := strings.TrimPrefix(u.Path, "/"); path != "" {
		db, err = strconv.Atoi(path)
		if err != nil {
			return nil, fmt.Errorf("parsing redis db: %w", err)
		}
	}
	username := u.User.Username()
	password, _ := u.User.Password()
	return &NetRedisClient{
		addr:     u.Host,
		username: username,
		password: password,
		db:       db,
		useTLS:   u.Scheme == "rediss",
		timeout:  200 * time.Millisecond,
	}, nil
}

func (c *NetRedisClient) HIncrBy(ctx context.Context, key, field string, value int64) error {
	_, err := c.command(ctx, "HINCRBY", key, field, strconv.FormatInt(value, 10))
	return err
}

func (c *NetRedisClient) Expire(ctx context.Context, key string, ttl time.Duration) error {
	_, err := c.command(ctx, "EXPIRE", key, strconv.FormatInt(int64(ttl.Seconds()), 10))
	return err
}

func (c *NetRedisClient) Close() error {
	return nil
}

func (c *NetRedisClient) command(ctx context.Context, args ...string) (string, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = conn.Close()
	}()

	if c.password != "" {
		authArgs := []string{"AUTH"}
		if c.username != "" {
			authArgs = append(authArgs, c.username)
		}
		authArgs = append(authArgs, c.password)
		if _, err := writeCommand(conn, authArgs...); err != nil {
			return "", err
		}
		if _, err := readSimple(conn); err != nil {
			return "", err
		}
	}
	if c.db != 0 {
		if _, err := writeCommand(conn, "SELECT", strconv.Itoa(c.db)); err != nil {
			return "", err
		}
		if _, err := readSimple(conn); err != nil {
			return "", err
		}
	}
	if _, err := writeCommand(conn, args...); err != nil {
		return "", err
	}
	return readSimple(conn)
}

func (c *NetRedisClient) dial(ctx context.Context) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: c.timeout}
	if deadline, ok := ctx.Deadline(); ok {
		dialer.Timeout = time.Until(deadline)
	}
	if c.useTLS {
		return tls.DialWithDialer(dialer, "tcp", c.addr, &tls.Config{MinVersion: tls.VersionTLS12})
	}
	return dialer.DialContext(ctx, "tcp", c.addr)
}

func writeCommand(conn net.Conn, args ...string) (int, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(arg), arg)
	}
	return conn.Write([]byte(b.String()))
}

func readSimple(conn net.Conn) (string, error) {
	reader := bufio.NewReader(conn)
	prefix, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	switch prefix {
	case '+', ':':
		return line, nil
	case '-':
		return "", errors.New(line)
	default:
		return "", fmt.Errorf("unexpected redis response prefix %q", prefix)
	}
}
