-- Revert 0175. Lockout counters, known devices and the opt-out are discarded;
-- the next sign-in is treated as a first device (silently re-learned).
DROP TABLE IF EXISTS user_security_prefs;
DROP TABLE IF EXISTS user_known_devices;
DROP TABLE IF EXISTS login_throttles;
