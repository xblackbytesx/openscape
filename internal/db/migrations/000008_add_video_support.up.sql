-- Add duration column (seconds) for video files; NULL for photos.
ALTER TABLE photos ADD COLUMN duration INTEGER;
