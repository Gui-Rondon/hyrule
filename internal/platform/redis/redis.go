// Package redis connects to Redis with the settings read from config.Config:
//
//	redis:
//	  host: redis.example.com   # or cluster: ["host:port", ...]
//	  port: 6379
//	  socket_timeout: 1         # seconds
package redis

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/Gui-Rondon/hyrule/internal/platform/config"
	"github.com/Gui-Rondon/hyrule/internal/platform/health"
)

// Client is a single node or cluster client.
type Client = goredis.UniversalClient

// Connect builds the client; it does not dial until the first command.
func Connect(cfg config.Config) (Client, string, error) {
	r := cfg.Sub("redis")
	timeout := time.Second
	if v := r.String("socket_timeout", ""); v != "" {
		secs, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, "", fmt.Errorf("redis.socket_timeout: %w", err)
		}
		timeout = time.Duration(secs * float64(time.Second))
	}
	addrs := r.Strings("cluster")
	name := strings.Join(addrs, ",")
	if len(addrs) == 0 {
		host := r.String("host", "redis")
		addrs = []string{net.JoinHostPort(host, r.String("port", "6379"))}
		name = host
	}
	client := goredis.NewUniversalClient(&goredis.UniversalOptions{
		Addrs:         addrs,
		IsClusterMode: len(r.Strings("cluster")) > 0,
		DialTimeout:   timeout,
		ReadTimeout:   timeout,
		WriteTimeout:  timeout,
	})
	return client, name, nil
}

// Check is the redis-healthcheck of the Eve APIs.
func Check(client Client, name string) health.Checker {
	return health.Checker{
		Name: "redis-healthcheck",
		Check: func(ctx context.Context) (bool, string) {
			if err := client.Ping(ctx).Err(); err != nil {
				return false, "Redis Nok, " + name
			}
			return true, "Redis Ok, " + name
		},
	}
}
