# OAuth2 (Minimal) for Athena

This adds a minimal OAuth2 token endpoint compatible with password and refresh_token grants.

- Token endpoint: `POST /oauth/token`
- Auth: HTTP Basic (preferred) or form fields `client_id` and `client_secret`.
- Grants:
  - `grant_type=password` with `username` (or email) and `password`.
  - `grant_type=refresh_token` with `refresh_token`.

Response:

```
{
  "access_token": "...",
  "token_type": "bearer",
  "expires_in": 900,
  "refresh_token": "..."
}
```

## Creating a Client

Clients are stored in the `oauth_clients` table. Example SQL to create a confidential client:

```sql
-- Replace 11111111-1111-1111-1111-111111111111 with a real UUID
INSERT INTO oauth_clients (
  id, client_id, client_secret_hash, name, grant_types, scopes, redirect_uris, is_confidential
) VALUES (
  '11111111-1111-1111-1111-111111111111',
  'athena-local',
  -- bcrypt hash of 'secret' (use your own strong secret!)
  '$2a$10$7yYI5S0sVZ5OkbF3vO7yyuS3ZzB1n0x5t8fWq8i/btO1Y9QThDgX2',
  'Athena Local Client',
  ARRAY['password','refresh_token'],
  ARRAY['basic'],
  ARRAY[]::text[],
  TRUE
);
```

To generate a bcrypt hash in Go:

```go
hash, _ := bcrypt.GenerateFromPassword([]byte("your-secret"), bcrypt.DefaultCost)
fmt.Println(string(hash))
```

## Notes

- Access tokens are JWTs signed with the same HS256 secret (`JWT_SECRET`).
- Refresh tokens reuse the existing `refresh_tokens` table and Redis sessions.
- For now, client scopes and grant checks are permissive; refine as needed.
