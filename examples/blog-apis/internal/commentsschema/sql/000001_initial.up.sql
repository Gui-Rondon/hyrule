BEGIN;
CREATE TABLE IF NOT EXISTS comments (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 doc jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 version bigint NOT NULL DEFAULT 1,
 deleted boolean NOT NULL DEFAULT false
);
-- Adoption validates the legacy shape before recording a clean version.
LOCK TABLE comments IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
 IF (SELECT count(*) FROM pg_attribute WHERE attrelid='comments'::regclass AND attnum>0 AND NOT attisdropped) <> 6
 OR (SELECT count(*) FROM pg_attribute a WHERE attrelid='comments'::regclass AND attnum>0 AND NOT attisdropped AND attnotnull AND
   (attname, atttypid, pg_get_expr((SELECT adbin FROM pg_attrdef WHERE adrelid=a.attrelid AND adnum=a.attnum), a.attrelid)) IN (
    ('id','uuid'::regtype::oid,'gen_random_uuid()'),
    ('doc','jsonb'::regtype::oid,'''{}''::jsonb'),
    ('created_at','timestamptz'::regtype::oid,'now()'),
    ('updated_at','timestamptz'::regtype::oid,'now()'),
    ('version','bigint'::regtype::oid,'1'),
    ('deleted','boolean'::regtype::oid,'false')
   )) <> 6
 OR NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='comments'::regclass AND contype='p' AND conkey=ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid='comments'::regclass AND attname='id')]::smallint[])
 THEN RAISE EXCEPTION 'comments legacy schema differs from the supported baseline; inspect before adoption'; END IF;
END $$;
COMMIT;
