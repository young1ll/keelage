package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/young1ll/keelage/internal/port"
)

// norm is the canonical user id: GitHub logins are case-insensitive.
func norm(user string) string { return strings.ToLower(strings.TrimSpace(user)) }

// AddMember adds a user to an org (idempotent; role updated).
func (s *Store) AddMember(ctx context.Context, org, user, role string) error {
	user = norm(user)
	if role == "" {
		role = "member"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO member (org_id, user_id, role) VALUES ($1,$2,$3) ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role`, org, user, role)
	return err
}

// IsMember reports membership.
func (s *Store) IsMember(ctx context.Context, org, user string) (bool, error) {
	user = norm(user)
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(1) FROM member WHERE org_id = $1 AND user_id = $2`, org, user).Scan(&n)
	return n > 0, err
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// IssueToken creates a bearer token for a member and returns the secret
// once. Agent/ci tokens carry the responsible human as owner.
func (s *Store) IssueToken(ctx context.Context, org, user, kind, owner, label string, ttl time.Duration) (string, error) {
	switch kind {
	case "human", "agent", "ci":
	default:
		return "", errors.New("postgres: token kind is human, agent or ci")
	}
	if kind != "human" && owner == "" {
		return "", errors.New("postgres: agent and ci tokens need an owner")
	}
	user, owner = norm(user), norm(owner)
	// the responsible human must be a member: the user for human tokens,
	// the owner for agent and ci tokens
	member := user
	if kind != "human" {
		member = owner
	}
	ok, err := s.IsMember(ctx, org, member)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("postgres: " + member + " is not a member of " + org)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := "kl_" + hex.EncodeToString(raw)
	var expires any
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO api_token (token_hash, org_id, user_id, kind, owner, label, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		hashToken(secret), org, user, kind, nullable(owner), nullable(label), expires)
	if err != nil {
		return "", err
	}
	return secret, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// RevokeToken revokes a secret.
func (s *Store) RevokeToken(ctx context.Context, secret string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_token SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, hashToken(secret))
	return err
}

// Authenticate implements port.Authenticator.
func (s *Store) Authenticate(ctx context.Context, secret string) (port.Principal, error) {
	var p port.Principal
	var owner sql.NullString
	var expires, revoked sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT org_id, user_id, kind, owner, expires_at, revoked_at FROM api_token WHERE token_hash = $1`, hashToken(secret)).
		Scan(&p.Org, &p.User, &p.Kind, &owner, &expires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return port.Principal{}, port.ErrUnauthenticated
	}
	if err != nil {
		return port.Principal{}, err
	}
	if revoked.Valid || (expires.Valid && expires.Time.Before(time.Now())) {
		return port.Principal{}, port.ErrUnauthenticated
	}
	p.Owner = owner.String
	if p.Kind == "human" {
		p.Owner = p.User
	}
	return p, nil
}

// RegisterDaemon records (or confirms) a daemon's public key for an org.
// A different key for the same daemon id is refused: keys rotate through
// a new id in v0.
func (s *Store) RegisterDaemon(ctx context.Context, org, id, publicKey, user string) error {
	var existing string
	err := s.db.QueryRowContext(ctx, `SELECT public_key FROM daemon WHERE org_id = $1 AND id = $2`, org, id).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = s.db.ExecContext(ctx, `INSERT INTO daemon (org_id, id, public_key, user_id) VALUES ($1,$2,$3,$4)`, org, id, publicKey, user)
		return err
	case err != nil:
		return err
	case existing != publicKey:
		return port.ErrDaemonKeyMismatch
	}
	return nil
}

// DaemonKey implements port.DaemonRegistry.
func (s *Store) DaemonKey(ctx context.Context, org, id string) (string, string, error) {
	var key, user string
	err := s.db.QueryRowContext(ctx, `SELECT public_key, user_id FROM daemon WHERE org_id = $1 AND id = $2`, org, id).Scan(&key, &user)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", port.ErrNotFound
	}
	return key, user, err
}

var (
	_ port.Authenticator  = (*Store)(nil)
	_ port.DaemonRegistry = (*Store)(nil)
	_ port.TokenIssuer    = (*Store)(nil)
	_ port.Membership     = (*Store)(nil)
)
