// Package users maps OIDC identities to local user rows.
package users

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrIdentityConflict means this e-mail address already belongs to a user with a different login
// identity (for example the person was deleted and re-created at the identity provider). The two are
// deliberately NOT merged automatically: matching by e-mail alone could hand one person's data to
// someone else.
var ErrIdentityConflict = errors.New("users: e-mail address is already linked to another login identity")

type Store struct{ pool *pgxpool.Pool }

func NewStore(p *pgxpool.Pool) *Store { return &Store{pool: p} }

// EnsureBySubject returns the local user ID for an identity subject, creating the row on first sight.
func (s *Store) EnsureBySubject(ctx context.Context, sub, email, name string) (uuid.UUID, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
INSERT INTO users (identity_subject, email, display_name)
VALUES ($1, $2, NULLIF($3, ''))
ON CONFLICT (identity_subject) DO UPDATE SET updated_at = now()
RETURNING id`, sub, email, name).Scan(&id)
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "users_email_key" {
		return uuid.Nil, ErrIdentityConflict
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("users: ensure: %w", err)
	}
	return id, nil
}
