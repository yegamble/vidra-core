-- What a person accepted, in which version, where it lived, and when.
--
-- Until now a signup's only consent signal was the age-attestation flag on the
-- request body: checked, then thrown away. Nothing recorded that an account
-- holder accepted the instance's terms or privacy policy, so an operator asked
-- "what did this person agree to, and when?" had no row to point at.
--
-- One row is one acceptance of one document. The table is append-only: a later
-- acceptance (source 'reaccept') is a new row, never an edit of an old one, so
-- the history of what was accepted stays intact. Rows go with the account
-- (ON DELETE CASCADE); the account export carries them until then.
CREATE TABLE user_consents (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    document    TEXT NOT NULL CHECK (document IN ('terms', 'privacy', 'age')),
    -- The operator's label for the document's version when it was accepted
    -- ('' when the operator had set none). A label the operator types, not a
    -- hash of the page behind the URL.
    version     TEXT NOT NULL DEFAULT '' CHECK (char_length(version) <= 64),
    -- Where the document lived when it was accepted ('' for 'age').
    url         TEXT NOT NULL DEFAULT '' CHECK (char_length(url) <= 2000),
    -- The age attested ("I am at least N years old"); 'age' rows only. No
    -- birthdate is collected anywhere.
    minimum_age INT CHECK (minimum_age > 0),
    -- How the acceptance arrived: the signup form, a signup approved from the
    -- queue, a first sign-in through an OIDC provider or ATProto, or a later
    -- re-acceptance by an existing account.
    source      TEXT NOT NULL CHECK (source IN ('signup', 'approval', 'oidc', 'atproto', 'reaccept')),
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_consents_age_has_minimum CHECK ((document = 'age') = (minimum_age IS NOT NULL))
);

CREATE INDEX user_consents_user_id_idx ON user_consents (user_id, accepted_at);

-- Append-only in the store, not only by convention: an UPDATE would rewrite
-- evidence. DELETE stays possible because the account cascade needs it.
CREATE FUNCTION user_consents_refuse_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'user_consents is append-only: record a new acceptance instead of changing one'
        USING ERRCODE = 'restrict_violation';
END
$$;

CREATE TRIGGER user_consents_append_only
    BEFORE UPDATE ON user_consents
    FOR EACH ROW EXECUTE FUNCTION user_consents_refuse_update();

-- A signup held in the approval queue is accepted by the applicant when they
-- submit it and becomes an account only when an admin approves it, possibly
-- days later. The acceptance is kept on the request so the account's consent
-- rows carry the time the PERSON accepted, not the time the admin approved.
-- All nullable: a request filed before this migration, or while the instance
-- had no documents configured, has nothing to record.
ALTER TABLE registration_requests
    ADD COLUMN consent_accepted_at     TIMESTAMPTZ,
    ADD COLUMN consent_terms_url       TEXT,
    ADD COLUMN consent_terms_version   TEXT,
    ADD COLUMN consent_privacy_url     TEXT,
    ADD COLUMN consent_privacy_version TEXT,
    ADD COLUMN consent_minimum_age     INT CHECK (consent_minimum_age > 0);
