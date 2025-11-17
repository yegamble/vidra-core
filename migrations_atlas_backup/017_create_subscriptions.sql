-- Create user subscriptions table and maintain subscriber counts

-- Add denormalized subscriber_count to users for fast reads
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS subscriber_count BIGINT NOT NULL DEFAULT 0;

-- Subscriptions table
CREATE TABLE IF NOT EXISTS subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscriber_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(subscriber_id, channel_id)
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_subscriber ON subscriptions(subscriber_id);
CREATE INDEX IF NOT EXISTS idx_subscriptions_channel ON subscriptions(channel_id);

-- Trigger functions to maintain users.subscriber_count
CREATE OR REPLACE FUNCTION increment_subscriber_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE users SET subscriber_count = subscriber_count + 1, updated_at = NOW()
    WHERE id = NEW.channel_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION decrement_subscriber_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE users SET subscriber_count = GREATEST(subscriber_count - 1, 0), updated_at = NOW()
    WHERE id = OLD.channel_id;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_subscriptions_inc ON subscriptions;
CREATE TRIGGER trg_subscriptions_inc
AFTER INSERT ON subscriptions
FOR EACH ROW EXECUTE FUNCTION increment_subscriber_count();

DROP TRIGGER IF EXISTS trg_subscriptions_dec ON subscriptions;
CREATE TRIGGER trg_subscriptions_dec
AFTER DELETE ON subscriptions
FOR EACH ROW EXECUTE FUNCTION decrement_subscriber_count();

