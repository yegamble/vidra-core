-- Provider signups can be queued for approval, the way password signups have
-- been since W7.
--
-- Until this migration the approval queue could only hold a PASSWORD signup:
-- registration_requests stores a bcrypt hash and nothing else that identifies
-- the applicant, so an OIDC or ATProto first sign-in had no way to become a
-- pending request. The consequence was not that provider signups queued badly
-- — it is that they did not consult the policy at all, and an instance with
-- registration closed, or behind approval, still minted an account and a
-- session for anyone a configured provider would attest. These columns are what
-- let the provider paths file a request instead.
--
-- All nullable/defaulted: an existing row is a password request, which is
-- exactly what NULL provider means. password_hash stays NOT NULL and holds ''
-- for a provider request — the same empty-hash convention users.password_hash
-- already uses for provider-created accounts, and one bcrypt can never verify.
ALTER TABLE registration_requests
    -- The provider that attested the applicant: an OAUTH_PROVIDERS name, or
    -- 'atproto'. NULL = an ordinary password request.
    ADD COLUMN IF NOT EXISTS oauth_provider TEXT,
    -- The provider's stable subject (OIDC `sub`, or the ATProto DID). Together
    -- with oauth_provider this is the key oauth_identities is created under at
    -- approval, so the approved account signs in through the same identity that
    -- applied — not merely one carrying the same address.
    ADD COLUMN IF NOT EXISTS oauth_subject TEXT,
    -- Display handle for the ATProto path (oauth_identities.handle), NULL for
    -- OIDC, which has none.
    ADD COLUMN IF NOT EXISTS oauth_handle TEXT,
    -- The address to record ON THE IDENTITY row. It is NOT always the account
    -- email: an ATProto account's email is a synthetic unroutable placeholder
    -- while its identity row deliberately stores '' (A30). Keeping both means
    -- approval reproduces exactly what a direct sign-in would have created.
    ADD COLUMN IF NOT EXISTS oauth_email TEXT NOT NULL DEFAULT '',
    -- Whether the provider asserted the address verified, so approval can seed
    -- users.email_verified the way the direct create path does.
    ADD COLUMN IF NOT EXISTS oauth_email_verified BOOLEAN NOT NULL DEFAULT FALSE;

-- One pending request per provider identity. Without this a second sign-in
-- while the first is unresolved files a duplicate, and the applicant appears in
-- the queue once per attempt. Partial, like the username/email pending indexes
-- beside it: a resolved request must not block a later one.
CREATE UNIQUE INDEX IF NOT EXISTS registration_requests_pending_provider_idx
    ON registration_requests (oauth_provider, oauth_subject)
    WHERE status = 'pending' AND oauth_provider IS NOT NULL;
