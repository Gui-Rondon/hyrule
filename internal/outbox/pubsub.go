package outbox

import (
	"context"
	"sync"

	"cloud.google.com/go/pubsub/v2"
)

// PubSub publishes outbox messages to Google Pub/Sub with message ordering
// on, so events with the same key reach an ordered subscription in order.
type PubSub struct {
	Client *pubsub.Client

	mu   sync.Mutex
	pubs map[string]*pubsub.Publisher
}

// Publish implements Publisher; it waits for the server to accept the
// message.
func (p *PubSub) Publish(ctx context.Context, msg Message) error {
	pub := p.publisher(msg.Topic)
	_, err := pub.Publish(ctx, &pubsub.Message{
		Data:        msg.Data,
		Attributes:  msg.Attributes,
		OrderingKey: msg.OrderingKey,
	}).Get(ctx)
	if err != nil && msg.OrderingKey != "" {
		// after an error the client pauses the key until it is resumed;
		// the relay retries the same event first
		pub.ResumePublish(msg.OrderingKey)
	}
	return err
}

func (p *PubSub) publisher(topic string) *pubsub.Publisher {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pubs == nil {
		p.pubs = map[string]*pubsub.Publisher{}
	}
	pub, ok := p.pubs[topic]
	if !ok {
		pub = p.Client.Publisher(topic)
		pub.EnableMessageOrdering = true
		p.pubs[topic] = pub
	}
	return pub
}

// Stop flushes and stops the publishers.
func (p *PubSub) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pub := range p.pubs {
		pub.Stop()
	}
}
