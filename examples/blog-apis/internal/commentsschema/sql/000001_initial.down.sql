DO $$ BEGIN RAISE EXCEPTION 'baseline rollback would delete comments; restore data or use a reviewed forward migration'; END $$;
