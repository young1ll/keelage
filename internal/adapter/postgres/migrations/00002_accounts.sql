-- +goose Up
CREATE TABLE member (
  org_id  TEXT NOT NULL REFERENCES org(id),
  user_id TEXT NOT NULL,            -- e.g. the GitHub login or an email
  role    TEXT NOT NULL DEFAULT 'member',
  added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, user_id)
);

-- bearer tokens: only the sha256 of the secret is stored
CREATE TABLE api_token (
  token_hash TEXT PRIMARY KEY,
  org_id     TEXT NOT NULL REFERENCES org(id),
  user_id    TEXT NOT NULL,
  kind       TEXT NOT NULL,          -- human | agent | ci
  owner      TEXT,                   -- responsible human for agent/ci tokens
  label      TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ
);

-- daemons that push: their public key verifies every synced record
CREATE TABLE daemon (
  org_id        TEXT NOT NULL REFERENCES org(id),
  id            TEXT NOT NULL,
  public_key    TEXT NOT NULL,
  user_id       TEXT NOT NULL,
  registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, id)
);

-- +goose Down
DROP TABLE daemon;
DROP TABLE api_token;
DROP TABLE member;
