package kafka

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type Publisher struct {
	client *kgo.Client
}

func NewPublisher(brokers []string, deliveryTimeout time.Duration) (*Publisher, error) {
	if len(brokers) == 0 {
		return nil, errors.New("at least one kafka broker is required")
	}
	if deliveryTimeout <= 0 {
		return nil, errors.New("delivery timeout must be greater than zero")
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordDeliveryTimeout(deliveryTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}

	return &Publisher{client: client}, nil
}

func (p *Publisher) Publish(
	ctx context.Context,
	topic string,
	key string,
	payload []byte,
) error {
	if topic == "" {
		return errors.New("topic is required")
	}
	if key == "" {
		return errors.New("message key is required")
	}
	if len(payload) == 0 {
		return errors.New("message payload is required")
	}

	result := p.client.ProduceSync(ctx, &kgo.Record{
		Topic: topic,
		Key:   []byte(key),
		Value: payload,
		Headers: []kgo.RecordHeader{
			{Key: "event-id", Value: []byte(key)},
		},
	})
	if err := result.FirstErr(); err != nil {
		return fmt.Errorf("publish kafka record: %w", err)
	}
	return nil
}

func (p *Publisher) Ping(ctx context.Context) error {
	if err := p.client.Ping(ctx); err != nil {
		return fmt.Errorf("ping kafka: %w", err)
	}
	return nil
}

func (p *Publisher) Close() {
	p.client.Close()
}
