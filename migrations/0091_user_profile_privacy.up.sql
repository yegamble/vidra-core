-- Account profile visibility is independent from discovery visibility.
-- Private by default: users explicitly opt into a public profile.
ALTER TABLE users ADD COLUMN profile_public BOOLEAN NOT NULL DEFAULT FALSE;
