BEGIN;
ALTER TABLE comments ADD CONSTRAINT comments_post_id_present CHECK (post_id IS NOT NULL) NOT VALID;
ALTER TABLE comments VALIDATE CONSTRAINT comments_post_id_present;
ALTER TABLE comments ALTER COLUMN post_id SET NOT NULL;
ALTER TABLE comments DROP CONSTRAINT comments_post_id_present;
ALTER TABLE comments ADD CONSTRAINT comments_post_id_matches_doc CHECK (
 (jsonb_typeof(doc->'post_id') = 'string') IS TRUE AND post_id IS NOT DISTINCT FROM doc->>'post_id'
) NOT VALID;
ALTER TABLE comments VALIDATE CONSTRAINT comments_post_id_matches_doc;
COMMIT;
