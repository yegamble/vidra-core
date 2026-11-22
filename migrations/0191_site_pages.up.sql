-- Each save gets a new revision so an editor opened before another admin's
-- update (or deletion and re-creation of the same slug) cannot overwrite it.
CREATE TABLE site_pages (
    slug text PRIMARY KEY CHECK (length(slug) <= 80 AND slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    title text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 150),
    body text NOT NULL CHECK (octet_length(body) BETWEEN 1 AND 102400),
    menu_label text NOT NULL DEFAULT '' CHECK (length(menu_label) <= 150),
    in_menu boolean NOT NULL DEFAULT true,
    menu_order integer NOT NULL DEFAULT 0 CHECK (menu_order >= 0),
    revision uuid NOT NULL DEFAULT gen_random_uuid()
);
