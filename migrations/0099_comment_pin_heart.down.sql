-- Drop the FK-bearing column first, then the comment flag.
ALTER TABLE videos DROP COLUMN pinned_comment_id;
ALTER TABLE comments DROP COLUMN hearted;
