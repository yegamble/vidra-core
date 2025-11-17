-- Fix subscriptions channel_id foreign key to reference channels table instead of users

-- Drop the incorrect foreign key constraint
ALTER TABLE subscriptions
DROP CONSTRAINT IF EXISTS subscriptions_channel_id_fkey;

-- Add the correct foreign key constraint to channels table
ALTER TABLE subscriptions
ADD CONSTRAINT subscriptions_channel_id_fkey
FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE;