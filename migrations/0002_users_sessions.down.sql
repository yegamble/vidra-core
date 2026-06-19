-- 0002 down: drop sessions and users (sessions first due to FK).
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
