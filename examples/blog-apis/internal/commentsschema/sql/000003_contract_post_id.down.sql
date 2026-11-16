BEGIN;
ALTER TABLE comments DROP CONSTRAINT comments_post_id_matches_doc;
ALTER TABLE comments ALTER COLUMN post_id DROP NOT NULL;
COMMIT;
