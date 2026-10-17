// Package queue is the Redis-backed job transport between the web and worker processes.
//
// Postgres remains the source of truth. Jobs are triggers or work items that can always be
// rebuilt from the database, so a Redis outage never fails an HTTP request: the web process
// enqueues best-effort and the worker's periodic jobs catch up.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// Task types.
const (
	TypePlanReminders     = "reminders:plan"     // periodic (worker scheduler)
	TypeDispatchReminders = "reminders:dispatch" // periodic (worker scheduler)
	TypePurgeObjects      = "retention:purge"    // periodic + kicked by web after deletes
	TypeSweepOrphans      = "retention:sweep"    // periodic
	TypePurgeAccounts     = "account:purge"      // periodic: erase accounts whose grace period ended
	TypeSendEmails        = "account:emails"     // periodic: deletion notice e-mails
	TypeEraseIdentities   = "account:identities" // periodic: delete erased users at the identity provider
	TypeSendPush          = "notify:push"        // enqueued by the dispatcher, one per notification
)

// Queue names and their relative weights for the worker.
const (
	QueueDefault = "default"
	QueuePush    = "push"
)

var QueueWeights = map[string]int{QueueDefault: 3, QueuePush: 2}

// Config locates Redis.
type Config struct {
	Addr     string
	Password string
	DB       int
}

func (c Config) RedisOpt() asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: c.Addr, Password: c.Password, DB: c.DB}
}

// Client enqueues jobs. Safe for concurrent use.
type Client struct{ c *asynq.Client }

func NewClient(cfg Config) *Client { return &Client{c: asynq.NewClient(cfg.RedisOpt())} }

func (c *Client) Close() error { return c.c.Close() }

// probeOpt is RedisOpt with short timeouts, for metrics collection.
func (c Config) probeOpt() asynq.RedisClientOpt {
	o := c.RedisOpt()
	o.DialTimeout, o.ReadTimeout, o.WriteTimeout = time.Second, time.Second, time.Second
	return o
}

// Ping verifies Redis is reachable (startup check and readiness probe). It is a single PING with
// retries disabled and a short timeout, so a dead Redis is reported in about a second.
func Ping(cfg Config) error {
	c := redis.NewClient(&redis.Options{
		Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
		MaxRetries: -1, // go-redis default is 3 retries with backoff, which would make this take seconds
	})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("queue: redis unreachable: %w", err)
	}
	return nil
}

// RequestPurge asks the worker to process the object-deletion outbox now instead of waiting for
// its next periodic run. Bursts are coalesced. The outbox row already exists in Postgres, so
// losing this message only delays deletion until the next periodic purge.
func (c *Client) RequestPurge(ctx context.Context) error {
	_, err := c.c.EnqueueContext(ctx, asynq.NewTask(TypePurgeObjects, nil),
		asynq.Queue(QueueDefault), asynq.Unique(2*time.Second), asynq.MaxRetry(3), asynq.Timeout(2*time.Minute))
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return nil
	}
	return err
}

// PushPayload identifies the notification to deliver over an external channel.
type PushPayload struct {
	NotificationID uuid.UUID `json:"notificationId"`
}

// EnqueuePush schedules external (push/email) delivery for an already-delivered in-app
// notification. The task ID makes enqueueing idempotent for 24 hours.
func (c *Client) EnqueuePush(ctx context.Context, notificationID uuid.UUID) error {
	body, err := json.Marshal(PushPayload{NotificationID: notificationID})
	if err != nil {
		return fmt.Errorf("queue: encode push payload: %w", err)
	}
	_, err = c.c.EnqueueContext(ctx, asynq.NewTask(TypeSendPush, body),
		asynq.Queue(QueuePush), asynq.TaskID("push:"+notificationID.String()),
		asynq.MaxRetry(5), asynq.Timeout(30*time.Second), asynq.Retention(24*time.Hour))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

// ParsePush decodes a notify:push task payload.
func ParsePush(t *asynq.Task) (uuid.UUID, error) {
	var p PushPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil || p.NotificationID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("queue: invalid push payload: %w", asynq.SkipRetry)
	}
	return p.NotificationID, nil
}
