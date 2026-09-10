-- +goose Up
CREATE TABLE org (
  id         TEXT PRIMARY KEY,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- per-org chain head: serialises appends and carries the previous hash
CREATE TABLE org_head (
  org_id TEXT PRIMARY KEY REFERENCES org(id),
  seq    BIGINT NOT NULL DEFAULT 0,
  hash   BYTEA
);

CREATE TABLE event (
  gid       BIGSERIAL PRIMARY KEY,
  org_id    TEXT NOT NULL REFERENCES org(id),
  seq       BIGINT NOT NULL,
  stream    TEXT NOT NULL,
  ver       BIGINT NOT NULL,
  kind      TEXT NOT NULL,
  v         INT NOT NULL,
  ts        TIMESTAMPTZ NOT NULL,
  actor     JSONB NOT NULL,
  meta      JSONB NOT NULL,
  body      TEXT NOT NULL,   -- exact bytes: body_hash must re-verify; JSONB would normalise them
  body_json JSONB GENERATED ALWAYS AS (body::jsonb) STORED,
  meta_hash BYTEA NOT NULL,
  body_hash BYTEA NOT NULL,
  prev_hash BYTEA,
  hash      BYTEA NOT NULL,
  leaf_hash BYTEA NOT NULL,
  sig       BYTEA,
  origin    JSONB,
  idem      TEXT,
  UNIQUE (org_id, seq),
  UNIQUE (org_id, stream, ver),
  UNIQUE (org_id, stream, idem),
  UNIQUE (org_id, origin)
);
CREATE INDEX event_org_stream ON event (org_id, stream, ver);

CREATE TABLE merkle_node (
  org_id TEXT NOT NULL REFERENCES org(id),
  level  INT NOT NULL,
  idx    BIGINT NOT NULL,
  hash   BYTEA NOT NULL,
  PRIMARY KEY (org_id, level, idx)
);

CREATE TABLE checkpoint (
  org_id    TEXT NOT NULL REFERENCES org(id),
  tree_size BIGINT NOT NULL,
  root      BYTEA NOT NULL,
  ts        TIMESTAMPTZ NOT NULL,
  sig       BYTEA NOT NULL,
  key_id    TEXT NOT NULL,
  external  JSONB,
  PRIMARY KEY (org_id, tree_size)
);

-- tenant isolation: every query runs with app.org_id set for the transaction.
-- (a superuser bypasses RLS; the service role in production is not one.)
ALTER TABLE event ENABLE ROW LEVEL SECURITY;
ALTER TABLE event FORCE ROW LEVEL SECURITY;
CREATE POLICY event_org ON event USING (org_id = current_setting('app.org_id', true)) WITH CHECK (org_id = current_setting('app.org_id', true));
ALTER TABLE merkle_node ENABLE ROW LEVEL SECURITY;
ALTER TABLE merkle_node FORCE ROW LEVEL SECURITY;
CREATE POLICY merkle_org ON merkle_node USING (org_id = current_setting('app.org_id', true)) WITH CHECK (org_id = current_setting('app.org_id', true));
ALTER TABLE checkpoint ENABLE ROW LEVEL SECURITY;
ALTER TABLE checkpoint FORCE ROW LEVEL SECURITY;
CREATE POLICY checkpoint_org ON checkpoint USING (org_id = current_setting('app.org_id', true)) WITH CHECK (org_id = current_setting('app.org_id', true));

-- +goose Down
DROP TABLE checkpoint;
DROP TABLE merkle_node;
DROP TABLE event;
DROP TABLE org_head;
DROP TABLE org;
