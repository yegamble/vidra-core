-- Migration to make tags column nullable in videos table
-- This allows video creation without explicitly providing tags

ALTER TABLE videos ALTER COLUMN tags DROP NOT NULL;