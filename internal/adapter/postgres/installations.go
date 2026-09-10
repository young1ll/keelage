package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/young1ll/keelage/internal/port"
)

// LinkInstallation implements port.InstallationMap (creates the org).
func (s *Store) LinkInstallation(ctx context.Context, id int64, org string) error {
	if err := s.EnsureOrg(ctx, org); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO installation (id, org_id) VALUES ($1,$2) ON CONFLICT (id) DO UPDATE SET org_id = EXCLUDED.org_id, linked_at = now()`, id, org)
	return err
}

// OrgForInstallation implements port.InstallationMap.
func (s *Store) OrgForInstallation(ctx context.Context, id int64) (string, error) {
	var org string
	err := s.db.QueryRowContext(ctx, `SELECT org_id FROM installation WHERE id = $1`, id).Scan(&org)
	if errors.Is(err, sql.ErrNoRows) {
		return "", port.ErrNotFound
	}
	return org, err
}

var _ port.InstallationMap = (*Store)(nil)
