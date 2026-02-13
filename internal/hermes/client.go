package hermes

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Config holds the Hermes client configuration.
type Config struct {
	URL            string        `yaml:"url"`
	Token          string        `yaml:"token"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	ReconnectWait  time.Duration `yaml:"reconnect_wait"`
	MaxReconnects  int           `yaml:"max_reconnects"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		URL:            "nats://localhost:4222",
		ConnectTimeout: 5 * time.Second,
		ReconnectWait:  2 * time.Second,
		MaxReconnects:  -1, // infinite
	}
}

// Client is the Hermes NATS messaging client.
type Client struct {
	nc     *nats.Conn
	js     jetstream.JetStream
	source string
	logger *slog.Logger
}

// Connect creates a new Hermes client and connects to NATS.
func Connect(cfg Config, source string, logger *slog.Logger) (*Client, error) {
	opts := []nats.Option{
		nats.Name(source),
		nats.Timeout(cfg.ConnectTimeout),
		nats.ReconnectWait(cfg.ReconnectWait),
		nats.MaxReconnects(cfg.MaxReconnects),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				logger.Warn("hermes disconnected", "error", err)
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			logger.Info("hermes reconnected")
		}),
	}
	if cfg.Token != "" {
		opts = append(opts, nats.Token(cfg.Token))
	}

	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("hermes connect: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("hermes jetstream: %w", err)
	}

	return &Client{
		nc:     nc,
		js:     js,
		source: source,
		logger: logger.With("component", "hermes"),
	}, nil
}

// JetStream returns the underlying JetStream context.
func (c *Client) JetStream() jetstream.JetStream {
	return c.js
}

// Publish publishes an event to the given subject.
func (c *Client) Publish(subject string, event Event) error {
	data, err := event.Marshal()
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return c.nc.Publish(subject, data)
}

// PublishEvent creates and publishes an event in one call.
func (c *Client) PublishEvent(subject, eventType string, payload any) error {
	ev, err := NewEvent(eventType, c.source, payload)
	if err != nil {
		return err
	}
	return c.Publish(subject, ev)
}

// Subscribe subscribes to a subject and calls the handler for each event.
func (c *Client) Subscribe(subject string, handler func(Event)) (*nats.Subscription, error) {
	return c.nc.Subscribe(subject, func(msg *nats.Msg) {
		ev, err := UnmarshalEvent(msg.Data)
		if err != nil {
			c.logger.Error("failed to unmarshal event", "subject", subject, "error", err)
			return
		}
		handler(ev)
	})
}

// Request sends a request and waits for a reply.
func (c *Client) Request(subject string, event Event, timeout time.Duration) (*Event, error) {
	data, err := event.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	msg, err := c.nc.Request(subject, data, timeout)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	reply, err := UnmarshalEvent(msg.Data)
	if err != nil {
		return nil, fmt.Errorf("unmarshal reply: %w", err)
	}
	return &reply, nil
}

// PublishDiscovery publishes a discovery event that Alexandria will auto-capture.
func (c *Client) PublishDiscovery(agentID, content string, tags []string) error {
	subject := AgentSubject(SubjectAgentDiscovery, agentID)
	return c.PublishEvent(subject, "agent.discovery", DiscoveryData{
		Agent:   agentID,
		Content: content,
		Tags:    tags,
	})
}

// ProvisionStreams creates or updates all JetStream streams.
func (c *Client) ProvisionStreams(ctx context.Context) error {
	for _, cfg := range StreamConfigs {
		if _, err := c.js.CreateOrUpdateStream(ctx, cfg); err != nil {
			return fmt.Errorf("provision stream %s: %w", cfg.Name, err)
		}
	}
	return nil
}

// ProvisionKVBuckets creates all KeyValue buckets.
func (c *Client) ProvisionKVBuckets(ctx context.Context) error {
	for _, cfg := range KVBucketConfigs {
		if _, err := c.js.CreateOrUpdateKeyValue(ctx, cfg); err != nil {
			return fmt.Errorf("provision KV bucket %s: %w", cfg.Bucket, err)
		}
	}
	return nil
}

// Close drains and closes the NATS connection.
func (c *Client) Close() error {
	if c.nc != nil {
		return c.nc.Drain()
	}
	return nil
}
