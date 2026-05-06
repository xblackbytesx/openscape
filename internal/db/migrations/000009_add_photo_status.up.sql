-- Track async processing state for uploaded photos/videos.
-- New rows are 'processing' until the background worker generates a thumbnail
-- (and ffprobes the file, for videos), then flip to 'ready'. Failed jobs land
-- on 'failed' with a human-readable message in processing_error.
ALTER TABLE photos ADD COLUMN status           TEXT NOT NULL DEFAULT 'ready';
ALTER TABLE photos ADD COLUMN processing_error TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_photos_status ON photos(status) WHERE status <> 'ready';
