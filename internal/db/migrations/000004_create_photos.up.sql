CREATE TABLE photos (
  id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  gallery_id      UUID        NOT NULL REFERENCES galleries(id) ON DELETE CASCADE,
  uploaded_by     UUID        NOT NULL REFERENCES users(id),
  title           TEXT        NOT NULL DEFAULT '',
  description     TEXT        NOT NULL DEFAULT '',
  filename        TEXT        NOT NULL,
  storage_path    TEXT        NOT NULL UNIQUE,
  thumb_path      TEXT        NOT NULL,
  width           INTEGER,
  height          INTEGER,
  file_size       BIGINT,
  mime_type       TEXT        NOT NULL DEFAULT 'image/jpeg',
  is_360          BOOLEAN     NOT NULL DEFAULT FALSE,
  projection_type TEXT,
  exif_data       JSONB,
  captured_at     TIMESTAMPTZ,
  sort_order      INTEGER     NOT NULL DEFAULT 0,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE galleries ADD CONSTRAINT fk_cover_photo
  FOREIGN KEY (cover_photo_id) REFERENCES photos(id) ON DELETE SET NULL;
