-- name: ReadCatalogPublicProof :one
-- The source fence, static obligations and actual viewer's unsupported policy
-- share ONE MVCC snapshot. Repeat after HTTP+hydration before buffering output.
WITH viewer AS (
    SELECT sqlc.narg(viewer_id)::uuid AS id,
        COALESCE((SELECT NULLIF(btrim(u.sensitive_content_policy),'')
                  FROM users u WHERE u.id=sqlc.narg(viewer_id)::uuid),
                 (SELECT value FROM instance_settings WHERE key='sensitive_content_policy'),
                 'hide') AS policy
)
SELECT s.epoch,h.generation,h.page_count,h.document_count,
       h.membership_sha256,h.pages_sha256,(v.policy='hide')::boolean AS hide_sensitive
FROM search_catalog_state s
JOIN search_catalog_snapshots h
  ON h.scope=s.scope AND h.epoch=s.epoch AND h.generation=s.verified_generation
CROSS JOIN viewer v
WHERE s.scope='local' AND NOT s.force_full AND NOT h.pending
  AND h.generation>s.force_full_after_generation
  AND s.last_error_code IS NULL AND s.last_error_id IS NULL
  AND NOT EXISTS(SELECT 1 FROM search_catalog_dirty WHERE scope='local')
  -- Retry leases and dead letters are still outstanding static obligations.
  AND NOT EXISTS(SELECT 1 FROM search_outbox WHERE state IN ('pending','dead')
      AND event_type IN ('video.upsert','video.suppress','channel.upsert','channel.delete',
                         'user.suppress','reconcile.begin','reconcile.page','reconcile.end'))
  AND v.policy IN ('hide','warn','blur','display')
  AND (v.id IS NULL OR EXISTS(SELECT 1 FROM users WHERE id=v.id))
  -- The wire has one hide_sensitive boolean; it cannot represent these filters.
  AND NOT EXISTS(SELECT 1 FROM user_content_prefs p
      WHERE p.user_id=v.id AND p.sensitive_flag_policies<>'{}'::jsonb)
  AND NOT EXISTS(SELECT 1 FROM user_blocks b WHERE b.blocker_id=v.id)
  AND NOT EXISTS(SELECT 1 FROM muted_accounts m WHERE m.muter_id=v.id);
