-- 0175: login hardening (PeerTube parity) — per-account failed-login backoff
-- and new-device sign-in notices.
--
-- login_throttles: failed-password counters for an EXISTING account, one row
-- per (account, scope). scope is 'all' (the softer account-wide cap) or
-- 'ip:<coarse prefix>' (IPv4 /24, IPv6 /48 — never a raw address). Per-prefix
-- rows are the primary brake: an attacker elsewhere cannot lock the owner out
-- from the owner's own network. Unknown identifiers have no row by
-- construction, so nothing here can tell a caller whether an account exists.
-- scope 'ok:<prefix>' rows (window_start = last success) record prefixes the
-- account signs in from, which the account-wide cap never locks out.
CREATE TABLE login_throttles (
    user_id      UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    scope        TEXT        NOT NULL,
    fail_count   INTEGER     NOT NULL DEFAULT 0,
    window_start TIMESTAMPTZ NOT NULL,
    locked_until TIMESTAMPTZ,
    PRIMARY KEY (user_id, scope)
);

-- user_known_devices: devices a successful sign-in has been seen from. The key
-- is a SHA-256 of a long-lived random device cookie plus UA family + OS (a client
-- that keeps no cookies is a fresh device each sign-in; rows are capped and
-- pruned per user by the application). The raw cookie and any IP are never stored;
-- label is the coarse "Chrome on macOS" shown back to the user.
CREATE TABLE user_known_devices (
    id            UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id       UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_hash   TEXT        NOT NULL,
    label         TEXT        NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, device_hash)
);

-- user_security_prefs: the new-device email opt-out (default ON — it is a
-- security notice, like the password-changed mail) and the rate-limit clock
-- that bounds how often one account can be mailed.
CREATE TABLE user_security_prefs (
    user_id                     UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    new_device_email            BOOLEAN NOT NULL DEFAULT TRUE,
    last_new_device_email_at    TIMESTAMPTZ
);
