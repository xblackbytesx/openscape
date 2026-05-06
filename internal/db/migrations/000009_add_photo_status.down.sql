DROP INDEX IF EXISTS idx_photos_status;
ALTER TABLE photos DROP COLUMN IF EXISTS processing_error;
ALTER TABLE photos DROP COLUMN IF EXISTS status;
