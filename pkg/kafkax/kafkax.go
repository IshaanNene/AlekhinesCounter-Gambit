// Package kafkax wraps the Kafka client the services share.
//
// Kafka is the backbone that decouples live play from analysis: finishing a game
// must not wait on an engine evaluating fifty positions. The game-service emits
// an event and returns; a pool of workers consumes at its own pace. That is also
// what makes analysis capacity scale independently — add workers to the consumer
// group and the partitions redistribute, with no change to the producer.
package kafkax

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

// Topics carried by the platform.
const (
	// TopicGameFinished is the durable record that a game ended.
	TopicGameFinished = "game-finished"
	// TopicAnalysisRequested is the work queue the engine workers consume.
	TopicAnalysisRequested = "analysis-requested"
	// TopicAnalysisCompleted carries finished reports back.
	TopicAnalysisCompleted = "analysis-completed"
)

// Partitions per topic. More partitions than expected workers, so the pool can
// grow without repartitioning: a consumer group can never have more useful
// members than there are partitions.
const defaultPartitions = 6

// Producer publishes protobuf events.
type Producer struct {
	client *kgo.Client
	log    *slog.Logger
}

// NewProducer connects to Kafka. An empty brokers string returns a disabled
// producer whose Publish is a no-op, so the platform still plays chess without
// Kafka — it just stops analysing games.
func NewProducer(brokers string, log *slog.Logger) (*Producer, error) {
	if brokers == "" {
		return &Producer{log: log}, nil
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(splitBrokers(brokers)...),
		// Wait for all in-sync replicas: an analysis request that vanishes on a
		// broker restart is a game that never gets a report.
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchMaxBytes(1<<20),
		kgo.RecordRetries(5),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: %w", err)
	}
	return &Producer{client: client, log: log}, nil
}

// Enabled reports whether Kafka is configured.
func (p *Producer) Enabled() bool { return p != nil && p.client != nil }

// Close flushes and closes the client.
func (p *Producer) Close() {
	if p.Enabled() {
		p.client.Close()
	}
}

// Publish sends a protobuf message, keyed so related events keep their order.
//
// The key is the game id: Kafka guarantees order only within a partition, and
// keying by game puts every event for one game on the same partition. Two
// events for the same game can never be processed out of order.
func (p *Producer) Publish(ctx context.Context, topic, key string, msg proto.Message) error {
	if !p.Enabled() {
		return nil
	}
	payload, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", topic, err)
	}
	rec := &kgo.Record{Topic: topic, Key: []byte(key), Value: payload}

	// Synchronous: the caller decides what a failure means. The game-service
	// treats it as non-fatal (the game is already saved), but it must know.
	if err := p.client.ProduceSync(ctx, rec).FirstErr(); err != nil {
		return fmt.Errorf("produce to %s: %w", topic, err)
	}
	return nil
}

// Consumer reads protobuf events as part of a consumer group.
type Consumer struct {
	client *kgo.Client
	log    *slog.Logger
}

// NewConsumer joins `group` on `topics`. Members of one group share the
// partitions between them, so running N workers divides the work N ways with no
// coordination of our own.
func NewConsumer(brokers, group string, log *slog.Logger, topics ...string) (*Consumer, error) {
	if brokers == "" {
		return nil, errors.New("kafka brokers are required for a consumer")
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(splitBrokers(brokers)...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		// Commit only after a record is handled, so a crash mid-analysis replays
		// that game rather than silently dropping it. At-least-once: handlers
		// must tolerate seeing the same game twice.
		kgo.DisableAutoCommit(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer: %w", err)
	}
	return &Consumer{client: client, log: log}, nil
}

// Close leaves the group cleanly, so partitions are reassigned promptly rather
// than after a session timeout.
func (c *Consumer) Close() {
	if c.client != nil {
		c.client.Close()
	}
}

// Handler processes one record. Returning an error leaves the offset uncommitted
// so the record is retried.
type Handler func(ctx context.Context, key string, value []byte) error

// Run consumes until ctx is cancelled, committing after each successful batch.
func (c *Consumer) Run(ctx context.Context, handle Handler) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		fetches := c.client.PollFetches(ctx)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return nil
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				if errors.Is(e.Err, context.Canceled) {
					return nil
				}
				c.log.Error("kafka fetch", "topic", e.Topic, "partition", e.Partition, "error", e.Err)
			}
			// Back off rather than spinning on a broker that is unwell.
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
			continue
		}

		failed := false
		fetches.EachRecord(func(rec *kgo.Record) {
			if err := handle(ctx, string(rec.Key), rec.Value); err != nil {
				c.log.Error("handler failed; offset not committed",
					"topic", rec.Topic, "key", string(rec.Key), "error", err)
				failed = true
			}
		})
		// Commit only a clean batch: committing past a failure would lose it.
		if !failed {
			if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
				c.log.Error("commit offsets", "error", err)
			}
		}
	}
}

// EnsureTopics creates the platform's topics if they are absent.
//
// Auto-creation exists but gives one partition and no control, so a worker pool
// could never scale past a single consumer. Declaring them explicitly makes the
// partition count a deliberate decision.
func EnsureTopics(ctx context.Context, brokers string, log *slog.Logger) error {
	if brokers == "" {
		return nil
	}
	client, err := kgo.NewClient(kgo.SeedBrokers(splitBrokers(brokers)...))
	if err != nil {
		return fmt.Errorf("kafka admin: %w", err)
	}
	defer client.Close()

	admin := kadm.NewClient(client)
	topics := []string{TopicGameFinished, TopicAnalysisRequested, TopicAnalysisCompleted}
	resp, err := admin.CreateTopics(ctx, defaultPartitions, 1, nil, topics...)
	if err != nil {
		return fmt.Errorf("create topics: %w", err)
	}
	for _, t := range resp {
		switch {
		case t.Err == nil:
			log.Info("kafka topic created", "topic", t.Topic, "partitions", defaultPartitions)
		case errors.Is(t.Err, kerr.TopicAlreadyExists):
			// Expected on every restart after the first.
		default:
			return fmt.Errorf("create topic %s: %w", t.Topic, t.Err)
		}
	}
	return nil
}

// splitBrokers turns "a:9092,b:9092" into a slice.
func splitBrokers(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if part := trimSpace(s[start:i]); part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}

func trimSpace(s string) string {
	a, b := 0, len(s)
	for a < b && (s[a] == ' ' || s[a] == '\t') {
		a++
	}
	for b > a && (s[b-1] == ' ' || s[b-1] == '\t') {
		b--
	}
	return s[a:b]
}
