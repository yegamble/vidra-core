-- Reverses 0131. Dropping the column drops users_single_owner_idx with it.
ALTER TABLE users DROP COLUMN is_owner;
