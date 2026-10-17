package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/mailer"
	"github.com/returnit/returnit/internal/push"
)

// DeviceStore is the part of the devices service delivery needs.
type DeviceStore interface {
	TokensFor(ctx context.Context, userID uuid.UUID) ([]string, error)
	RemoveToken(ctx context.Context, token string) error
}

// PhoneSender sends one push to one device token (FCM).
type PhoneSender interface {
	Send(ctx context.Context, token string, m push.Message) error
}

// Delivery is the Pusher used in production: channel "push" goes to the user's phones through
// FCM, channel "email" to their address over SMTP. Either part may be nil when that service is
// not configured; reminders still arrive in the in-app inbox.
type Delivery struct {
	Pool    *pgxpool.Pool // looks up the e-mail address for the email channel
	Devices DeviceStore
	Phones  PhoneSender
	Mailer  mailer.Mailer
	Log     *slog.Logger
}

func (d *Delivery) Send(ctx context.Context, userID uuid.UUID, channel string, m Message) error {
	switch channel {
	case "push":
		return d.sendPush(ctx, userID, m)
	case "email":
		return d.sendEmail(ctx, userID, m)
	default:
		return nil
	}
}

func (d *Delivery) sendPush(ctx context.Context, userID uuid.UUID, m Message) error {
	if d.Phones == nil || d.Devices == nil {
		d.Log.Debug("push not configured: reminder stays in-app", "notification_id", m.ID)
		return nil
	}
	tokens, err := d.Devices.TokensFor(ctx, userID)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return nil // no registered phone: the in-app copy is all there is
	}
	var delivered int
	var transient error
	for _, t := range tokens {
		err := d.Phones.Send(ctx, t, push.Message{ID: m.ID.String(), Title: m.Title, Body: m.Body})
		switch {
		case err == nil:
			delivered++
		case errors.Is(err, push.ErrUnregistered):
			if rerr := d.Devices.RemoveToken(ctx, t); rerr != nil {
				return rerr
			}
			d.Log.Info("removed dead push token", "notification_id", m.ID)
		case errors.Is(err, push.ErrRejected):
			d.Log.Error("push rejected by FCM", "notification_id", m.ID, "err", err)
		default:
			transient = err
		}
	}
	if delivered == 0 && transient != nil {
		return transient // nothing got through and the cause is temporary: let the queue retry
	}
	return nil // delivered somewhere, or nothing left to try; a retry would only duplicate
}

func (d *Delivery) sendEmail(ctx context.Context, userID uuid.UUID, m Message) error {
	if d.Mailer == nil || d.Pool == nil {
		return nil
	}
	var to string
	err := d.Pool.QueryRow(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&to)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: user no longer exists", ErrGone)
	}
	if err != nil {
		return fmt.Errorf("notifications: load e-mail address: %w", err)
	}
	err = d.Mailer.Send(ctx, mailer.Message{To: to, Subject: m.Title, Body: m.Body + "\n\n--\nThis is an automatic reminder from ReturnIt. You can change reminders in the app."})
	if errors.Is(err, mailer.ErrPermanent) {
		return fmt.Errorf("%w: %v", ErrPermanent, err)
	}
	return err
}
