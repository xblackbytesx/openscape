CREATE TABLE gallery_sessions (
  token      TEXT        PRIMARY KEY,
  gallery_id UUID        NOT NULL REFERENCES galleries(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  expires_at TIMESTAMPTZ NOT NULL
);
