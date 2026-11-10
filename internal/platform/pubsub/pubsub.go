// Package pubsub connects to Google Pub/Sub with the settings read from
// config.Config.
//
//	pubsub:
//	  project_id: my-project
//	  credentials_file: /secrets/pubsub/key.json   # optional
//	  service_account:                              # optional
//	    type: service_account
//	    client_email: ...
//	    private_key: ...
//
// The credentials are, in this order, credentials_file (a key mounted from
// a Secret), the inline service_account key, and the Application Default
// Credentials (Workload Identity on GKE, used only without a key in the
// config). With PUBSUB_EMULATOR_HOST set, the client talks to the emulator.
package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Gui-Rondon/hyrule/internal/platform/config"
	"github.com/Gui-Rondon/hyrule/internal/platform/health"
)

// Connect opens a client for pubsub.project_id.
func Connect(ctx context.Context, cfg config.Config) (*pubsub.Client, error) {
	project := cfg.String("pubsub.project_id", "")
	if project == "" {
		return nil, errors.New("pubsub: pubsub.project_id not set")
	}
	opts, source, err := credentials(cfg)
	if err != nil {
		return nil, err
	}
	if host := os.Getenv("PUBSUB_EMULATOR_HOST"); host != "" {
		// the client talks to the emulator whatever the credentials (e.g. a
		// local run with the staging config)
		source = "emulator " + host
	}
	slog.InfoContext(ctx, "pubsub credentials", "project", project, "source", source)
	client, err := pubsub.NewClient(ctx, project, opts...)
	if err != nil {
		return nil, fmt.Errorf("pubsub: connect: %w", err)
	}
	return client, nil
}

// credentials picks how the client authenticates: pubsub.credentials_file,
// then pubsub.service_account, then the Application Default Credentials.
// The key is an object in the three config-files profiles (properties-api
// flattens it and config.Load nests it again); a JSON string is accepted
// too. source names the choice for the log, without the key.
func credentials(cfg config.Config) (opts []option.ClientOption, source string, err error) {
	if file := cfg.String("pubsub.credentials_file", ""); file != "" {
		return []option.ClientOption{option.WithAuthCredentialsFile(option.ServiceAccount, file)}, "credentials_file", nil
	}
	v, ok := cfg.Get("pubsub.service_account")
	if !ok || v == nil || v == "" {
		return nil, "application default credentials", nil
	}
	var key []byte
	switch k := v.(type) {
	case map[string]any:
		if key, err = json.Marshal(k); err != nil {
			return nil, "", fmt.Errorf("pubsub.service_account: %w", err)
		}
	case string:
		key = []byte(k)
	default:
		return nil, "", fmt.Errorf("pubsub.service_account: unexpected %T", v)
	}
	var fields struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
	}
	if err := json.Unmarshal(key, &fields); err != nil || fields.Type != "service_account" {
		return nil, "", errors.New("pubsub.service_account: not a service account key")
	}
	return []option.ClientOption{option.WithAuthCredentialsJSON(option.ServiceAccount, key)}, "service_account " + fields.ClientEmail, nil
}

// Check is the pubsub_healthcheck of the Eve APIs. Python published a
// message to a "ping" topic on every check; here the topic is only looked
// up, without publishing. pubsub.healthcheck: false turns it off, as before.
func Check(cfg config.Config, client *pubsub.Client, topic string) health.Checker {
	name := fmt.Sprintf("projects/%s/topics/%s", client.Project(), topic)
	return check(cfg, topic, func(ctx context.Context) error {
		_, err := client.TopicAdminClient.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: name})
		return err
	})
}

// check reports Pub/Sub as reachable when the lookup succeeds or is denied:
// a service account with only roles/pubsub.publisher cannot read topics,
// but the denial proves the credentials and the network work.
func check(cfg config.Config, topic string, getTopic func(context.Context) error) health.Checker {
	enabled := cfg.Bool("pubsub.healthcheck", true)
	return health.Checker{
		Name: "pubsub_healthcheck",
		Check: func(ctx context.Context) (bool, string) {
			if !enabled {
				return true, "Healthcheck deactivated"
			}
			switch err := getTopic(ctx); status.Code(err) {
			case codes.OK, codes.PermissionDenied:
				return true, "PubsubPublisher Ok, " + topic
			default:
				return false, "PubsubPublisher Nok, " + topic
			}
		},
	}
}
