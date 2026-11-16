BEGIN;
DROP TRIGGER comments_sync_post_id ON comments;
DROP FUNCTION comments_sync_post_id();
ALTER TABLE comments DROP COLUMN post_id;
COMMIT;
