// Package devices stores the push tokens of a user's phones.
package devices

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound   = errors.New("devices: not found")
	ErrValidation = errors.New("devices: invalid input")
)

// MaxPerUser caps how many phones one account can have; the least recently seen are dropped.
const MaxPerUser = 10

// Device is what the API returns. The push token itself is never sent back.
type Device struct {
	ID         uuid.UUID `json:"id"`
	Platform   string    `json:"platform"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}

type Input struct {
	Platform  string `json:"platform"`
	PushToken string `json:"pushToken"`
}

func (in Input) validate() error {
	if in.Platform != "android" && in.Platform != "ios" {
		return fmt.Errorf("%w: platform must be android or ios", ErrValidation)
	}
	if n := len(in.PushToken); n < 20 || n > 4096 {
		return fmt.Errorf("%w: pushToken has an invalid length", ErrValidation)
	}
	for _, r := range in.PushToken {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%w: pushToken contains whitespace or control characters", ErrValidation)
		}
	}
	return nil
}

type Service struct{ pool *pgxpool.Pool }

func NewService(p *pgxpool.Pool) *Service { return &Service{pool: p} }

// Register stores a phone's push token for the user. Registering a token that already exists
// refreshes it (and moves it to this user if another user had it). Safe to call at every app start.
func (s *Service) Register(ctx context.Context, userID uuid.UUID, in Input) (Device, error) {
	in.PushToken = strings.TrimSpace(in.PushToken)
	if err := in.validate(); err != nil {
		return Device{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Device{}, fmt.Errorf("devices: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	var d Device
	err = tx.QueryRow(ctx, `
INSERT INTO devices (user_id, platform, push_token) VALUES ($1, $2, $3)
ON CONFLICT (push_token) DO UPDATE SET user_id = EXCLUDED.user_id, platform = EXCLUDED.platform, last_seen_at = now()
RETURNING id, platform, created_at, last_seen_at`, userID, in.Platform, in.PushToken).
		Scan(&d.ID, &d.Platform, &d.CreatedAt, &d.LastSeenAt)
	if err != nil {
		return Device{}, fmt.Errorf("devices: register: %w", err)
	}
	if _, err := tx.Exec(ctx, `
DELETE FROM devices WHERE id IN (
  SELECT id FROM devices WHERE user_id = $1 ORDER BY last_seen_at DESC, id OFFSET $2)`, userID, MaxPerUser); err != nil {
		return Device{}, fmt.Errorf("devices: trim: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Device{}, fmt.Errorf("devices: commit: %w", err)
	}
	return d, nil
}

// Unregister removes one of the user's phones (e.g. on sign-out). Other users' devices are
// reported as not found.
func (s *Service) Unregister(ctx context.Context, userID, deviceID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM devices WHERE id = $1 AND user_id = $2`, deviceID, userID)
	if err != nil {
		return fmt.Errorf("devices: unregister: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TokensFor returns the user's push tokens, most recently seen first.
func (s *Service) TokensFor(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT push_token FROM devices WHERE user_id = $1 ORDER BY last_seen_at DESC, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("devices: tokens: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, fmt.Errorf("devices: scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RemoveToken deletes a token the push service says is dead.
func (s *Service) RemoveToken(ctx context.Context, token string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM devices WHERE push_token = $1`, token); err != nil {
		return fmt.Errorf("devices: remove token: %w", err)
	}
	return nil
}
