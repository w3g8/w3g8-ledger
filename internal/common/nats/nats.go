package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"finplatform/internal/common/events"
)

// Config holds NATS configuration
type Config struct {
	URL           string        `envconfig:"NATS_URL" default:"nats://localhost:4222"`
	Name          string        `envconfig:"NATS_CLIENT_NAME" default:"finplatform"`
	MaxReconnects int           `envconfig:"NATS_MAX_RECONNECTS" default:"10"`
	ReconnectWait time.Duration `envconfig:"NATS_RECONNECT_WAIT" default:"2s"`
}

// Client wraps NATS connection with JetStream support
type Client struct {
	conn   *nats.Conn
	js     jetstream.JetStream
	logger *slog.Logger
}

// New creates a new NATS client
func New(ctx context.Context, cfg Config, logger *slog.Logger) (*Client, error) {
	opts := []nats.Option{
		nats.Name(cfg.Name),
		nats.MaxReconnects(cfg.MaxReconnects),
		nats.ReconnectWait(cfg.ReconnectWait),
		nats.DisconnectErrHandler(func(c *nats.Conn, err error) {
			logger.Warn("NATS disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			logger.Info("NATS reconnected", "url", c.ConnectedUrl())
		}),
		nats.ErrorHandler(func(c *nats.Conn, s *nats.Subscription, err error) {
			logger.Error("NATS error", "error", err, "subject", s.Subject)
		}),
	}

	conn, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("connecting to NATS: %w", err)
	}

	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("creating JetStream context: %w", err)
	}

	logger.Info("NATS connection established", "url", conn.ConnectedUrl())

	return &Client{
		conn:   conn,
		js:     js,
		logger: logger,
	}, nil
}

// Close closes the NATS connection
func (c *Client) Close() {
	c.conn.Close()
}

// Conn returns the underlying NATS connection
func (c *Client) Conn() *nats.Conn {
	return c.conn
}

// JetStream returns the JetStream context
func (c *Client) JetStream() jetstream.JetStream {
	return c.js
}

// StreamConfig defines a JetStream stream
type StreamConfig struct {
	Name        string
	Description string
	Subjects    []string
	MaxAge      time.Duration
	MaxBytes    int64
	Replicas    int
}

// DefaultStreamConfig returns default stream configuration
func DefaultStreamConfig(name string, subjects []string) StreamConfig {
	return StreamConfig{
		Name:        name,
		Subjects:    subjects,
		MaxAge:      7 * 24 * time.Hour, // 7 days
		MaxBytes:    1 << 30,            // 1 GB
		Replicas:    1,
	}
}

// EnsureStream creates or updates a stream
func (c *Client) EnsureStream(ctx context.Context, cfg StreamConfig) (jetstream.Stream, error) {
	streamCfg := jetstream.StreamConfig{
		Name:        cfg.Name,
		Description: cfg.Description,
		Subjects:    cfg.Subjects,
		MaxAge:      cfg.MaxAge,
		MaxBytes:    cfg.MaxBytes,
		Replicas:    cfg.Replicas,
		Retention:   jetstream.LimitsPolicy,
		Storage:     jetstream.FileStorage,
	}

	stream, err := c.js.CreateOrUpdateStream(ctx, streamCfg)
	if err != nil {
		return nil, fmt.Errorf("creating/updating stream %s: %w", cfg.Name, err)
	}

	c.logger.Info("stream ensured",
		"name", cfg.Name,
		"subjects", cfg.Subjects,
	)

	return stream, nil
}

// ConsumerConfig defines a JetStream consumer
type ConsumerConfig struct {
	Name          string
	Stream        string
	FilterSubject string
	MaxDeliver    int
	AckWait       time.Duration
}

// DefaultConsumerConfig returns default consumer configuration
func DefaultConsumerConfig(name, stream, filterSubject string) ConsumerConfig {
	return ConsumerConfig{
		Name:          name,
		Stream:        stream,
		FilterSubject: filterSubject,
		MaxDeliver:    5,
		AckWait:       30 * time.Second,
	}
}

// EnsureConsumer creates or updates a consumer
func (c *Client) EnsureConsumer(ctx context.Context, cfg ConsumerConfig) (jetstream.Consumer, error) {
	consumerCfg := jetstream.ConsumerConfig{
		Name:          cfg.Name,
		Durable:       cfg.Name,
		FilterSubject: cfg.FilterSubject,
		MaxDeliver:    cfg.MaxDeliver,
		AckWait:       cfg.AckWait,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	}

	consumer, err := c.js.CreateOrUpdateConsumer(ctx, cfg.Stream, consumerCfg)
	if err != nil {
		return nil, fmt.Errorf("creating/updating consumer %s: %w", cfg.Name, err)
	}

	c.logger.Info("consumer ensured",
		"name", cfg.Name,
		"stream", cfg.Stream,
		"filter", cfg.FilterSubject,
	)

	return consumer, nil
}

// Publisher publishes events to NATS
type Publisher struct {
	client *Client
	logger *slog.Logger
}

// NewPublisher creates a new event publisher
func NewPublisher(client *Client, logger *slog.Logger) *Publisher {
	return &Publisher{
		client: client,
		logger: logger,
	}
}

// Publish publishes an event
func (p *Publisher) Publish(ctx context.Context, event *events.Event) error {
	subject := fmt.Sprintf("events.%s", event.Type)

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}

	_, err = p.client.js.Publish(ctx, subject, data)
	if err != nil {
		return fmt.Errorf("publishing event: %w", err)
	}

	p.logger.Debug("event published",
		"event_id", event.ID,
		"type", event.Type,
		"subject", subject,
	)

	return nil
}

// PublishBatch publishes multiple events
func (p *Publisher) PublishBatch(ctx context.Context, evts []*events.Event) error {
	for _, event := range evts {
		if err := p.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

// Subscriber subscribes to events
type Subscriber struct {
	client   *Client
	consumer jetstream.Consumer
	logger   *slog.Logger
}

// NewSubscriber creates a new event subscriber
func NewSubscriber(client *Client, consumer jetstream.Consumer, logger *slog.Logger) *Subscriber {
	return &Subscriber{
		client:   client,
		consumer: consumer,
		logger:   logger,
	}
}

// MessageHandler handles incoming messages
type MessageHandler func(ctx context.Context, event *events.Event) error

// Start starts consuming messages
func (s *Subscriber) Start(ctx context.Context, handler MessageHandler) error {
	iter, err := s.consumer.Messages()
	if err != nil {
		return fmt.Errorf("getting message iterator: %w", err)
	}

	go func() {
		<-ctx.Done()
		iter.Stop()
	}()

	for {
		msg, err := iter.Next()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.logger.Error("error getting next message", "error", err)
			continue
		}

		var event events.Event
		if err := json.Unmarshal(msg.Data(), &event); err != nil {
			s.logger.Error("error unmarshaling event", "error", err)
			_ = msg.Nak()
			continue
		}

		if err := handler(ctx, &event); err != nil {
			s.logger.Error("error handling event",
				"error", err,
				"event_id", event.ID,
				"type", event.Type,
			)
			_ = msg.Nak()
			continue
		}

		if err := msg.Ack(); err != nil {
			s.logger.Error("error acknowledging message", "error", err)
		}
	}
}

// HealthCheck checks NATS connection health
func (c *Client) HealthCheck() error {
	if !c.conn.IsConnected() {
		return fmt.Errorf("NATS not connected")
	}
	return nil
}
