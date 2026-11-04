-- Invalidation is part of the canonical write, including old CLI writers and
-- transactions that began before bootstrap. Never predicate it on epoch/state
-- or another table's current eligibility: a concurrent publication can race it.
CREATE FUNCTION search_catalog_mark_dirty() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    changed BOOLEAN;
    columns TEXT;
BEGIN
    IF TG_OP = 'TRUNCATE' THEN
        changed := true;
    ELSIF TG_OP = 'INSERT' THEN
        EXECUTE 'SELECT EXISTS (SELECT 1 FROM new_rows)' INTO changed;
    ELSIF TG_OP = 'DELETE' THEN
        EXECUTE 'SELECT EXISTS (SELECT 1 FROM old_rows)' INTO changed;
    ELSE
        -- IDs bind values to their canonical entity. EXCEPT treats NULLs as
        -- equal and ignores row ordering; unchanged and empty statements emit 0.
        columns := CASE TG_TABLE_NAME
            WHEN 'videos' THEN 'id,channel_id,title,description,privacy,state,category,language,license,is_sensitive,sensitive_flags,created_at,updated_at,publish_at'
            WHEN 'channels' THEN 'id,owner_id,handle,display_name'
            WHEN 'users' THEN 'id,unlisted'
            WHEN 'video_metadata' THEN 'video_id,duration_seconds'
            WHEN 'video_tags' THEN 'video_id,tag'
            WHEN 'video_blocks' THEN 'video_id'
        END;
        IF columns IS NULL THEN
            RAISE EXCEPTION 'unsupported canonical catalog trigger table';
        END IF;
        EXECUTE format('SELECT EXISTS (
            (SELECT %1$s FROM new_rows EXCEPT SELECT %1$s FROM old_rows)
            UNION ALL
            (SELECT %1$s FROM old_rows EXCEPT SELECT %1$s FROM new_rows))', columns)
            INTO changed;
    END IF;
    IF changed THEN
        -- A new immutable UUID per statement; no singleton conflict/lock and no
        -- sequence high-water that can skip a later-committing transaction.
        INSERT INTO public.search_catalog_dirty(scope) VALUES ('local');
    END IF;
    RETURN NULL;
END;
$$;

DO $$
DECLARE
    tab TEXT;
BEGIN
    FOREACH tab IN ARRAY ARRAY['videos','channels','users','video_metadata','video_tags','video_blocks'] LOOP
        EXECUTE format('CREATE TRIGGER search_catalog_dirty_insert AFTER INSERT ON public.%I
            REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT
            EXECUTE FUNCTION public.search_catalog_mark_dirty()', tab);
        EXECUTE format('CREATE TRIGGER search_catalog_dirty_update AFTER UPDATE ON public.%I
            REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT
            EXECUTE FUNCTION public.search_catalog_mark_dirty()', tab);
        EXECUTE format('CREATE TRIGGER search_catalog_dirty_delete AFTER DELETE ON public.%I
            REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT
            EXECUTE FUNCTION public.search_catalog_mark_dirty()', tab);
        EXECUTE format('CREATE TRIGGER search_catalog_dirty_truncate AFTER TRUNCATE ON public.%I FOR EACH STATEMENT EXECUTE FUNCTION public.search_catalog_mark_dirty()', tab);
    END LOOP;
END;
$$;

COMMENT ON FUNCTION search_catalog_mark_dirty() IS 'Unconditional transactional static/eligibility invalidation; restore must retain this function and all 24 statement triggers.';
