package hermes

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Stream configuration for JetStream.
var StreamConfigs = []jetstream.StreamConfig{
	{
		Name:        "AGENT_LIFECYCLE",
		Description: "Agent lifecycle events: start, stop, ready, degraded, scale",
		Subjects:    []string{"swarm.agent.>"},
		Retention:   jetstream.LimitsPolicy,
		MaxAge:      7 * 24 * time.Hour, // 7 days
		Storage:     jetstream.FileStorage,
		Replicas:    1,
		Discard:     jetstream.DiscardOld,
	},
	{
		Name:        "TASK_EVENTS",
		Description: "Task assignment, completion, and failure events",
		Subjects:    []string{"swarm.task.>"},
		Retention:   jetstream.LimitsPolicy,
		MaxAge:      30 * 24 * time.Hour, // 30 days
		Storage:     jetstream.FileStorage,
		Replicas:    1,
		Discard:     jetstream.DiscardOld,
	},
	{
		Name:        "SYSTEM_EVENTS",
		Description: "System-level events: health, config, shutdown",
		Subjects:    []string{"swarm.system.>"},
		Retention:   jetstream.LimitsPolicy,
		MaxAge:      7 * 24 * time.Hour, // 7 days
		Storage:     jetstream.FileStorage,
		Replicas:    1,
		Discard:     jetstream.DiscardOld,
	},
	{
		Name:        "SLACK_EVENTS",
		Description: "Slack message and reaction events from the forwarder",
		Subjects:    []string{"swarm.slack.>"},
		Retention:   jetstream.LimitsPolicy,
		MaxAge:      14 * 24 * time.Hour, // 14 days
		Storage:     jetstream.FileStorage,
		Replicas:    1,
		Discard:     jetstream.DiscardOld,
	},
}

// KVBucketConfigs defines KeyValue buckets to provision.
var KVBucketConfigs = []jetstream.KeyValueConfig{
	{
		Bucket:  "THREAD_OWNERSHIP",
		TTL:     24 * time.Hour,
		Storage: jetstream.MemoryStorage,
	},
}

// ProvisionStreams creates or updates all JetStream streams.
func ProvisionStreams(js jetstream.JetStream) error {
	for _, cfg := range StreamConfigs {
		if _, err := js.CreateOrUpdateStream(context.TODO(), cfg); err != nil {
			return fmt.Errorf("provision stream %s: %w", cfg.Name, err)
		}
	}
	return nil
}

// ProvisionKVBuckets creates all KeyValue buckets if they don't already exist.
func ProvisionKVBuckets(js jetstream.JetStream) error {
	for _, cfg := range KVBucketConfigs {
		if _, err := js.CreateOrUpdateKeyValue(context.TODO(), cfg); err != nil {
			return fmt.Errorf("provision KV bucket %s: %w", cfg.Bucket, err)
		}
	}
	return nil
}
