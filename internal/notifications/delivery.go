package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/returnit/returnit/internal/emailtpl"
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
	// AssetsURL is where the e-mail logos are served; empty shows the name as text.
	AssetsURL string
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
	// The address, the person's language and what the reminder is about, in one read.
	var (
		to, language, product string
		kind                  string
		days                  *int
		end                   *time.Time
	)
	err := d.Pool.QueryRow(ctx, `
SELECT u.email, u.language, COALESCE(n.kind, 'warranty'), n.offset_days, n.warranty_end_date, COALESCE(p.product_name, '')
FROM users u
LEFT JOIN notifications n ON n.id = $2 AND n.user_id = u.id
LEFT JOIN purchases p ON p.id = n.purchase_id
WHERE u.id = $1`, userID, m.ID).Scan(&to, &language, &kind, &days, &end, &product)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: user no longer exists", ErrGone)
	}
	if err != nil {
		return fmt.Errorf("notifications: load e-mail address: %w", err)
	}
	logo, logoDark := emailtpl.AssetURLs(d.AssetsURL)
	msg := mailer.Message{To: to, Subject: m.Title}
	if days != nil && end != nil && product != "" {
		mail := emailtpl.Reminder(language, kind == KindReturn, m.Title, m.Body, product, *end, *days, logo, logoDark)
		msg.Subject, msg.Body, msg.HTML = mail.Subject, mail.Text, mail.HTML
	} else {
		msg.Body = m.Body // the purchase is gone: the stored text still says what it was about
	}
	err = d.Mailer.Send(ctx, msg)
	if errors.Is(err, mailer.ErrPermanent) {
		return fmt.Errorf("%w: %v", ErrPermanent, err)
	}
	return err
}
