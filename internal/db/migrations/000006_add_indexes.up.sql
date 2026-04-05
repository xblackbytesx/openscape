CREATE INDEX idx_galleries_owner      ON galleries(owner_id);
CREATE INDEX idx_galleries_visibility  ON galleries(visibility);
CREATE INDEX idx_photos_gallery        ON photos(gallery_id, sort_order);
CREATE INDEX idx_gallery_sessions_exp  ON gallery_sessions(expires_at);
