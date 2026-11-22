-- name: ListSitePages :many
SELECT slug, title, menu_label, in_menu, menu_order, revision
FROM site_pages
WHERE NOT sqlc.arg(menu_only)::boolean OR in_menu
ORDER BY menu_order, slug;

-- name: GetSitePage :one
SELECT * FROM site_pages WHERE slug = $1;

-- name: CreateSitePage :one
INSERT INTO site_pages (slug, title, body, menu_label, in_menu, menu_order)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (slug) DO NOTHING
RETURNING *;

-- name: UpdateSitePage :one
UPDATE site_pages
SET title = sqlc.arg(title), body = sqlc.arg(body), menu_label = sqlc.arg(menu_label),
    in_menu = sqlc.arg(in_menu),
    menu_order = sqlc.arg(menu_order), revision = gen_random_uuid()
WHERE slug = sqlc.arg(slug) AND revision = sqlc.arg(revision)
RETURNING *;

-- name: DeleteSitePage :execrows
DELETE FROM site_pages WHERE slug = $1 AND revision = $2;
