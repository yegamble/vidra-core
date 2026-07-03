DROP TABLE IF EXISTS e2ee_messages;
DROP TABLE IF EXISTS e2ee_one_time_keys;
DROP TABLE IF EXISTS e2ee_devices;
ALTER TABLE conversations DROP COLUMN IF EXISTS encrypted;
