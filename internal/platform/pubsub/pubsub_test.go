package pubsub

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Gui-Rondon/hyrule/internal/platform/config"
)

func TestCheck(t *testing.T) {
	if os.Getenv("PUBSUB_EMULATOR_HOST") == "" {
		t.Skip("PUBSUB_EMULATOR_HOST not set")
	}
	ctx := context.Background()
	cfg := config.Config{"pubsub": map[string]any{"project_id": "local-dev"}}
	client, err := Connect(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	topic := fmt.Sprintf("check-%d", time.Now().UnixNano())

	if ok, out := Check(cfg, client, topic).Check(ctx); ok || out != "PubsubPublisher Nok, "+topic {
		t.Errorf("missing topic = %v %q", ok, out)
	}
	name := "projects/local-dev/topics/" + topic
	if _, err := client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{Name: name}); err != nil {
		t.Fatal(err)
	}
	defer client.TopicAdminClient.DeleteTopic(ctx, &pubsubpb.DeleteTopicRequest{Topic: name})
	if ok, out := Check(cfg, client, topic).Check(ctx); !ok || out != "PubsubPublisher Ok, "+topic {
		t.Errorf("existing topic = %v %q", ok, out)
	}

	off := config.Config{"pubsub": map[string]any{"project_id": "local-dev", "healthcheck": false}}
	if ok, out := Check(off, client, "nope").Check(ctx); !ok || out != "Healthcheck deactivated" {
		t.Errorf("disabled = %v %q", ok, out)
	}
}

func TestConnectCredentialsFile(t *testing.T) {
	cfg := config.Config{"pubsub": map[string]any{"project_id": "p", "credentials_file": "/nonexistent/key.json"}}
	t.Setenv("PUBSUB_EMULATOR_HOST", "")
	if _, err := Connect(context.Background(), cfg); err == nil {
		t.Error("missing credentials file accepted")
	}
}

// R4: a publisher-only service account cannot read topics.
func TestCheckPermissionDenied(t *testing.T) {
	cfg := config.Config{}
	denied := check(cfg, "t", func(context.Context) error { return status.Error(codes.PermissionDenied, "no topics.get") })
	if ok, out := denied.Check(context.Background()); !ok || out != "PubsubPublisher Ok, t" {
		t.Errorf("denied = %v %q", ok, out)
	}
	down := check(cfg, "t", func(context.Context) error { return status.Error(codes.Unavailable, "down") })
	if ok, _ := down.Check(context.Background()); ok {
		t.Error("unavailable reported as ok")
	}
}

// The order is credentials_file, the service_account of the config-files
// pubsub profile (a JSON string, as Python reads it, or an object) and the
// Application Default Credentials.
func TestCredentials(t *testing.T) {
	key := `{"type": "service_account", "project_id": "p", "client_email": "sa@p.iam.gserviceaccount.com", "private_key": "x"}`
	var asMap map[string]any
	json.Unmarshal([]byte(key), &asMap)
	cases := []struct {
		name   string
		pubsub map[string]any
		source string
		// opts is the number of client options: 0 for the ADC
		opts int
		err  bool
	}{
		{"none", map[string]any{}, "application default credentials", 0, false},
		{"empty", map[string]any{"service_account": ""}, "application default credentials", 0, false},
		{"file first", map[string]any{"credentials_file": "/k.json", "service_account": key}, "credentials_file", 1, false},
		{"object", map[string]any{"service_account": asMap}, "service_account sa@p.iam.gserviceaccount.com", 1, false},
		{"string", map[string]any{"service_account": key}, "service_account sa@p.iam.gserviceaccount.com", 1, false},
		{"not JSON", map[string]any{"service_account": "{"}, "", 0, true},
		{"not a service account", map[string]any{"service_account": `{"type": "authorized_user"}`}, "", 0, true},
		{"wrong type", map[string]any{"service_account": 1}, "", 0, true},
	}
	for _, c := range cases {
		opts, source, err := credentials(config.Config{"pubsub": c.pubsub})
		if source != c.source || len(opts) != c.opts || (err != nil) != c.err {
			t.Errorf("%s: %d options, %q, %v", c.name, len(opts), source, err)
		}
	}
}

// A key with a real private key opens the client (no request is made until
// a call): as an object, the form of the config-files profiles, and as a
// JSON string.
func TestConnectServiceAccount(t *testing.T) {
	t.Setenv("PUBSUB_EMULATOR_HOST", "")
	pk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(pk)
	key := map[string]any{
		"type": "service_account", "project_id": "p", "private_key_id": "k1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "sa@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token",
	}
	asJSON, _ := json.Marshal(key)
	for name, v := range map[string]any{"object": key, "string": string(asJSON)} {
		cfg := config.Config{"pubsub": map[string]any{"project_id": "p", "service_account": v}}
		client, err := Connect(context.Background(), cfg)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		client.Close()
	}
}

// With the emulator the log names it, not the key of the config, which the
// client does not use.
func TestConnectLogsEmulator(t *testing.T) {
	t.Setenv("PUBSUB_EMULATOR_HOST", "127.0.0.1:8085")
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	key := `{"type": "service_account", "project_id": "p", "client_email": "sa@p.iam.gserviceaccount.com"}`
	client, err := Connect(context.Background(), config.Config{"pubsub": map[string]any{"project_id": "p", "service_account": key}})
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	if !strings.Contains(buf.String(), `source="emulator 127.0.0.1:8085"`) {
		t.Errorf("log = %s", buf.String())
	}
}
