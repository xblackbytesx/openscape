CREATE TYPE gallery_visibility AS ENUM (
  'public',
  'unlisted',
  'unlisted_protected',
  'private'
);

CREATE TABLE galleries (
  id             UUID               PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id       UUID               NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title          TEXT               NOT NULL,
  description    TEXT               NOT NULL DEFAULT '',
  slug           TEXT               UNIQUE NOT NULL,
  visibility     gallery_visibility NOT NULL DEFAULT 'private',
  password_hash  TEXT,
  cover_photo_id UUID,
  created_at     TIMESTAMPTZ        NOT NULL DEFAULT NOW(),
  updated_at     TIMESTAMPTZ        NOT NULL DEFAULT NOW()
);
