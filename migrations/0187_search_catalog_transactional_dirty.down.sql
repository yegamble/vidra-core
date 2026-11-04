DO $$
DECLARE
    tab TEXT;
BEGIN
    FOREACH tab IN ARRAY ARRAY['videos','channels','users','video_metadata','video_tags','video_blocks'] LOOP
        EXECUTE format('DROP TRIGGER search_catalog_dirty_insert ON public.%I', tab);
        EXECUTE format('DROP TRIGGER search_catalog_dirty_update ON public.%I', tab);
        EXECUTE format('DROP TRIGGER search_catalog_dirty_delete ON public.%I', tab);
        EXECUTE format('DROP TRIGGER search_catalog_dirty_truncate ON public.%I', tab);
    END LOOP;
END;
$$;
DROP FUNCTION search_catalog_mark_dirty();
