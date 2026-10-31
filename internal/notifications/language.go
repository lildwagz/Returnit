package notifications

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLanguage is returned for a language the app does not have.
var ErrLanguage = errors.New("notifications: unsupported language")

// rewriteLimit bounds how many of a person's existing reminders are rewritten when they change language.
const rewriteLimit = 1000

// LanguageService stores the language a person chose in the app and writes their reminders in it.
type LanguageService struct{ Pool *pgxpool.Pool }

// Set saves the language, then rewrites the title and body of the person's existing reminders (the inbox and the
// ones still waiting to be sent), so a reminder planned before the change does not stay in the old language. Safe to
// call at every app start: it does nothing when the language is unchanged.
func (s *LanguageService) Set(ctx context.Context, userID uuid.UUID, lang string) error {
	if !IsLanguage(lang) {
		return fmt.Errorf("%w: %q", ErrLanguage, lang)
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE users SET language = $2, updated_at = now() WHERE id = $1 AND language <> $2`, userID, lang)
	if err != nil {
		return fmt.Errorf("notifications: set language: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `
SELECT n.id, COALESCE(n.kind, 'warranty'), n.offset_days, n.warranty_end_date, p.product_name
FROM notifications n
JOIN purchases p ON p.id = n.purchase_id
WHERE n.user_id = $1 AND n.offset_days IS NOT NULL AND n.warranty_end_date IS NOT NULL AND n.status <> 'cancelled'
ORDER BY n.created_at DESC LIMIT $2`, userID, rewriteLimit)
	if err != nil {
		return fmt.Errorf("notifications: list for rewrite: %w", err)
	}
	type row struct {
		id      uuid.UUID
		kind    string
		days    int
		end     time.Time
		product string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.kind, &r.days, &r.end, &r.product); err != nil {
			rows.Close()
			return fmt.Errorf("notifications: scan for rewrite: %w", err)
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("notifications: rewrite rows: %w", err)
	}
	batch := &pgx.Batch{}
	for _, r := range all {
		title, body := Texts(lang, r.kind, r.product, r.days, r.end)
		batch.Queue(`UPDATE notifications SET title = $2, body = $3 WHERE id = $1`, r.id, title, body)
	}
	if batch.Len() == 0 {
		return nil
	}
	res := s.Pool.SendBatch(ctx, batch)
	defer res.Close()
	for range all {
		if _, err := res.Exec(); err != nil {
			return fmt.Errorf("notifications: rewrite: %w", err)
		}
	}
	return nil
}
