-- +goose Up
-- GitHub App installation ↔ org (spec §4.2: the user ↔ org mapping starts here)
CREATE TABLE installation (
  id        BIGINT PRIMARY KEY,
  org_id    TEXT NOT NULL REFERENCES org(id),
  linked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE installation;
