package reminders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGRepo struct{ pool *pgxpool.Pool }

func NewPGRepo(p *pgxpool.Pool) *PGRepo { return &PGRepo{pool: p} }

const cols = `enabled, days_before, channel,
  to_char(quiet_hours_start, 'HH24:MI'), to_char(quiet_hours_end, 'HH24:MI'), timezone`

func scan(row pgx.Row) (Preferences, error) {
	var p Preferences
	var raw []byte
	if err := row.Scan(&p.Enabled, &raw, &p.Channel, &p.QuietHoursStart, &p.QuietHoursEnd, &p.Timezone); err != nil {
		return Preferences{}, err
	}
	if err := json.Unmarshal(raw, &p.DaysBefore); err != nil {
		return Preferences{}, fmt.Errorf("reminders: decode days_before: %w", err)
	}
	if p.DaysBefore == nil {
		p.DaysBefore = []int{}
	}
	return p, nil
}

func (r *PGRepo) Get(ctx context.Context, userID uuid.UUID) (Preferences, bool, error) {
	p, err := scan(r.pool.QueryRow(ctx, `SELECT `+cols+` FROM reminder_preferences WHERE user_id = $1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Preferences{}, false, nil
	}
	if err != nil {
		return Preferences{}, false, fmt.Errorf("reminders: get: %w", err)
	}
	return p, true, nil
}

func (r *PGRepo) Upsert(ctx context.Context, userID uuid.UUID, p Preferences) (Preferences, error) {
	days, err := json.Marshal(p.DaysBefore)
	if err != nil {
		return Preferences{}, fmt.Errorf("reminders: encode: %w", err)
	}
	out, err := scan(r.pool.QueryRow(ctx, `
INSERT INTO reminder_preferences (user_id, enabled, days_before, channel, quiet_hours_start, quiet_hours_end, timezone, updated_at)
VALUES ($1, $2, $3::jsonb, $4, $5::time, $6::time, $7, now())
ON CONFLICT (user_id) DO UPDATE SET enabled = EXCLUDED.enabled, days_before = EXCLUDED.days_before,
  channel = EXCLUDED.channel, quiet_hours_start = EXCLUDED.quiet_hours_start,
  quiet_hours_end = EXCLUDED.quiet_hours_end, timezone = EXCLUDED.timezone, updated_at = now()
RETURNING `+cols, userID, p.Enabled, string(days), p.Channel, p.QuietHoursStart, p.QuietHoursEnd, p.Timezone))
	if err != nil {
		return Preferences{}, fmt.Errorf("reminders: upsert: %w", err)
	}
	return out, nil
}
