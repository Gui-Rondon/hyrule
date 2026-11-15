BEGIN;
ALTER TABLE comments ADD COLUMN post_id text;
CREATE FUNCTION comments_sync_post_id() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.post_id := NEW.doc->>'post_id';
 RETURN NEW;
END $$;
CREATE TRIGGER comments_sync_post_id BEFORE INSERT OR UPDATE ON comments
 FOR EACH ROW EXECUTE FUNCTION comments_sync_post_id();
COMMIT;
