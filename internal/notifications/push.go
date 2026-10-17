package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrPermanent marks a delivery failure that must not be retried (e.g. invalid device token).
	ErrPermanent = errors.New("notifications: permanent delivery failure")
	// ErrNotReady means the notification is not (yet) in the delivered state; retrying is right.
	ErrNotReady = errors.New("notifications: not delivered yet")
	// ErrGone means the notification no longer needs an external message; do not retry.
	ErrGone = errors.New("notifications: nothing to send")
)

// Pusher delivers a notification over an external channel (push or email). The in-app record
// always exists regardless; a Pusher only adds an extra delivery. Implementations must be safe
// for concurrent use and must not log message bodies.
type Pusher interface {
	Send(ctx context.Context, userID uuid.UUID, channel string, m Message) error
}

// Message is the content of one external delivery. ID is the notification's id, so the app can
// open the right entry when the user taps the push.
type Message struct {
	ID    uuid.UUID
	Title string
	Body  string
}

// NoopPusher is used until a push/email provider (FCM, APNs, SMTP...) is chosen. Reminders for
// users on the push/email channels are still delivered to their in-app inbox.
type NoopPusher struct {
	Log  *slog.Logger
	once sync.Once
}

func (n *NoopPusher) Send(context.Context, uuid.UUID, string, Message) error {
	n.once.Do(func() {
		n.Log.Warn("no push/email provider configured: reminders are delivered in-app only")
	})
	return nil
}

// PushSender performs the external delivery for one notification. The worker calls it from the
// notify:push job; retries and backoff belong to the queue.
type PushSender struct {
	Pool   *pgxpool.Pool
	Pusher Pusher
}

func (p *PushSender) Send(ctx context.Context, id uuid.UUID) error {
	var (
		userID              uuid.UUID
		channel             *string
		title, body, status string
	)
	err := p.Pool.QueryRow(ctx,
		`SELECT user_id, channel, title, body, status FROM notifications WHERE id = $1`, id).
		Scan(&userID, &channel, &title, &body, &status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("%w: notification %s does not exist", ErrGone, id)
	case err != nil:
		return fmt.Errorf("notifications: load for push: %w", err)
	}
	switch status {
	case "sent":
	case "scheduled":
		return ErrNotReady
	default:
		return fmt.Errorf("%w: status %s", ErrGone, status)
	}
	if channel == nil || *channel == "in_app" {
		return nil
	}
	return p.Pusher.Send(ctx, userID, *channel, Message{ID: id, Title: title, Body: body})
}
