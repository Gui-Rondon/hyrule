package outbox_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"

	"github.com/Gui-Rondon/hyrule/outbox"
	"github.com/Gui-Rondon/hyrule/platform/config"
	ps "github.com/Gui-Rondon/hyrule/platform/pubsub"
)

// Against the emulator: PUBSUB_EMULATOR_HOST=localhost:8085.
func TestPubSubOrderedDelivery(t *testing.T) {
	if os.Getenv("PUBSUB_EMULATOR_HOST") == "" {
		t.Skip("PUBSUB_EMULATOR_HOST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const project = "local-dev"
	client, err := ps.Connect(ctx, config.Config{"pubsub": map[string]any{"project_id": project}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	id := fmt.Sprintf("outbox-test-%d", time.Now().UnixNano())
	topic := fmt.Sprintf("projects/%s/topics/%s", project, id)
	sub := fmt.Sprintf("projects/%s/subscriptions/%s", project, id)
	if _, err := client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{Name: topic}); err != nil {
		t.Fatal(err)
	}
	defer client.TopicAdminClient.DeleteTopic(context.Background(), &pubsubpb.DeleteTopicRequest{Topic: topic})
	if _, err := client.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name: sub, Topic: topic, EnableMessageOrdering: true,
	}); err != nil {
		t.Fatal(err)
	}
	defer client.SubscriptionAdminClient.DeleteSubscription(context.Background(), &pubsubpb.DeleteSubscriptionRequest{Subscription: sub})

	pub := &outbox.PubSub{Client: client}
	defer pub.Stop()
	for i := range 3 {
		err := pub.Publish(ctx, outbox.Message{Topic: id, Data: fmt.Appendf(nil, "%d", i),
			Attributes: map[string]string{outbox.AttrEventID: fmt.Sprint(i)}, OrderingKey: "v1"})
		if err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	var got []string
	rctx, stop := context.WithCancel(ctx)
	err = client.Subscriber(id).Receive(rctx, func(_ context.Context, m *pubsub.Message) {
		mu.Lock()
		defer mu.Unlock()
		if m.Attributes[outbox.AttrEventID] != string(m.Data) || m.OrderingKey != "v1" {
			t.Errorf("message = %s %v %q", m.Data, m.Attributes, m.OrderingKey)
		}
		got = append(got, string(m.Data))
		m.Ack()
		if len(got) == 3 {
			stop()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !equal(got, []string{"0", "1", "2"}) {
		t.Errorf("received = %v", got)
	}
}

func TestPubSubConnectNeedsProject(t *testing.T) {
	if _, err := ps.Connect(context.Background(), config.Config{}); err == nil {
		t.Error("missing project_id must fail")
	}
}
