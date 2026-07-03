-- name: CreateDonationAddress :one
INSERT INTO donation_addresses (owner_id, channel_id, network, address, label)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, owner_id, channel_id, network, address, label, verified,
    verification_nonce, verification_expires_at, created_at;

-- name: GetDonationAddress :one
SELECT id, owner_id, channel_id, network, address, label, verified,
    verification_nonce, verification_expires_at, created_at
FROM donation_addresses
WHERE id = $1;

-- name: ListDonationAddressesByOwner :many
SELECT id, owner_id, channel_id, network, address, label, verified,
    verification_nonce, verification_expires_at, created_at
FROM donation_addresses
WHERE owner_id = $1
ORDER BY created_at, id;

-- name: ListAccountDonationAddresses :many
SELECT id, owner_id, channel_id, network, address, label, verified,
    verification_nonce, verification_expires_at, created_at
FROM donation_addresses
WHERE owner_id = $1 AND channel_id IS NULL
ORDER BY created_at, id;

-- name: ListChannelDonationAddresses :many
SELECT id, owner_id, channel_id, network, address, label, verified,
    verification_nonce, verification_expires_at, created_at
FROM donation_addresses
WHERE channel_id = $1
ORDER BY created_at, id;

-- name: DeleteDonationAddress :exec
DELETE FROM donation_addresses WHERE id = $1;

-- name: SetDonationAddressChallenge :one
UPDATE donation_addresses
SET verification_nonce = sqlc.arg('verification_nonce'),
    verification_expires_at = sqlc.arg('verification_expires_at')
WHERE id = sqlc.arg('id')
RETURNING id, owner_id, channel_id, network, address, label, verified,
    verification_nonce, verification_expires_at, created_at;

-- name: MarkDonationAddressVerified :one
UPDATE donation_addresses
SET verified = TRUE,
    verification_nonce = NULL,
    verification_expires_at = NULL
WHERE id = $1
RETURNING id, owner_id, channel_id, network, address, label, verified,
    verification_nonce, verification_expires_at, created_at;
