CREATE TYPE member_role AS ENUM ('editor', 'viewer');

CREATE TABLE gallery_members (
  gallery_id UUID        NOT NULL REFERENCES galleries(id) ON DELETE CASCADE,
  user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role       member_role NOT NULL DEFAULT 'viewer',
  PRIMARY KEY (gallery_id, user_id)
);
