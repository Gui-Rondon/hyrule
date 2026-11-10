package redis

import (
	"context"
	"net"
	"os"
	"testing"

	"github.com/Gui-Rondon/hyrule/internal/platform/config"
)

func TestConnectAndCheck(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR not set")
	}
	host, port, _ := net.SplitHostPort(addr)
	client, name, err := Connect(config.Config{"redis": map[string]any{"host": host, "port": port, "socket_timeout": "0.5"}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if ok, out := Check(client, name).Check(context.Background()); !ok || out != "Redis Ok, "+host {
		t.Errorf("check = %v %q", ok, out)
	}
}

func TestCheckFailsWhenUnreachable(t *testing.T) {
	client, name, err := Connect(config.Config{"redis": map[string]any{"host": "127.0.0.1", "port": "1", "socket_timeout": 0.2}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if ok, out := Check(client, name).Check(context.Background()); ok || out != "Redis Nok, 127.0.0.1" {
		t.Errorf("check = %v %q", ok, out)
	}
}

func TestConnectConfig(t *testing.T) {
	if _, name, err := Connect(config.Config{"redis": map[string]any{"cluster": []any{"a:1", "b:2"}}}); err != nil || name != "a:1,b:2" {
		t.Errorf("cluster = %q %v", name, err)
	}
	if _, name, _ := Connect(config.Config{}); name != "redis" {
		t.Errorf("default host = %q", name)
	}
	if _, _, err := Connect(config.Config{"redis": map[string]any{"socket_timeout": "x"}}); err == nil {
		t.Error("bad socket_timeout accepted")
	}
}
