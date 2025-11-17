-- Create messages table for user messaging system
CREATE TABLE messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    message_type VARCHAR(20) NOT NULL DEFAULT 'text' CHECK (message_type IN ('text', 'system')),
    is_read BOOLEAN NOT NULL DEFAULT false,
    is_deleted_by_sender BOOLEAN NOT NULL DEFAULT false,
    is_deleted_by_recipient BOOLEAN NOT NULL DEFAULT false,
    parent_message_id UUID REFERENCES messages(id) ON DELETE SET NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    read_at TIMESTAMP WITH TIME ZONE
);

-- Create conversations table to track messaging threads
CREATE TABLE conversations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    participant_one_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    participant_two_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    last_message_id UUID REFERENCES messages(id) ON DELETE SET NULL,
    last_message_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    UNIQUE(participant_one_id, participant_two_id)
);

-- Create indexes for efficient querying
CREATE INDEX idx_messages_sender_id ON messages(sender_id);
CREATE INDEX idx_messages_recipient_id ON messages(recipient_id);
CREATE INDEX idx_messages_conversation ON messages(sender_id, recipient_id);
CREATE INDEX idx_messages_created_at ON messages(created_at);
CREATE INDEX idx_messages_is_read ON messages(is_read);
CREATE INDEX idx_messages_parent_id ON messages(parent_message_id);

CREATE INDEX idx_conversations_participant_one ON conversations(participant_one_id);
CREATE INDEX idx_conversations_participant_two ON conversations(participant_two_id);
CREATE INDEX idx_conversations_last_message_at ON conversations(last_message_at);

-- Create composite index for user's conversations
CREATE INDEX idx_conversations_participants ON conversations(participant_one_id, participant_two_id);

-- Create trigger to update messages updated_at
CREATE TRIGGER update_messages_updated_at 
    BEFORE UPDATE ON messages 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

-- Create trigger to update conversations updated_at
CREATE TRIGGER update_conversations_updated_at 
    BEFORE UPDATE ON conversations 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

-- Function to ensure conversation participants are ordered consistently
CREATE OR REPLACE FUNCTION ensure_conversation_order() 
RETURNS TRIGGER AS $$
DECLARE
    temp_id UUID;
BEGIN
    -- Ensure participant_one_id is always less than participant_two_id for consistency
    IF NEW.participant_one_id > NEW.participant_two_id THEN
        -- Swap the participants
        temp_id := NEW.participant_one_id;
        NEW.participant_one_id := NEW.participant_two_id;
        NEW.participant_two_id := temp_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Apply the ordering trigger
CREATE TRIGGER ensure_conversation_order_trigger
    BEFORE INSERT OR UPDATE ON conversations
    FOR EACH ROW
    EXECUTE FUNCTION ensure_conversation_order();