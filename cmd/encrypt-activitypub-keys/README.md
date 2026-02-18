# ActivityPub Private Key Encryption Migration Tool

## Overview

This tool encrypts existing plaintext ActivityPub private keys stored in the database. It implements AES-256-GCM encryption to protect private keys at rest, addressing a critical security vulnerability.

## Security Context

**CRITICAL:** ActivityPub private keys were previously stored in plaintext in the database. This tool encrypts all existing keys to prevent unauthorized access.

### What This Tool Does

1. Connects to the Athena database
2. Identifies all unencrypted private keys
3. Encrypts each key using AES-256-GCM
4. Updates the database with encrypted keys
5. Marks keys as encrypted for tracking

### What This Tool Does NOT Do

- Does not change public keys (they remain unchanged)
- Does not affect ActivityPub functionality (transparent encryption)
- Does not require downtime (can run on live system)

## Prerequisites

### 1. Backup Your Database

**CRITICAL:** Always backup before running this tool!

```bash
pg_dump -U athena_user -d athena > athena_backup_$(date +%Y%m%d_%H%M%S).sql
```

### 2. Generate Encryption Key

Generate a strong random encryption key:

```bash
openssl rand -base64 48
```

**Save this key securely!** You will need it to:

- Decrypt keys in the future
- Run the application with ActivityPub enabled
- Recover from database restores

### 3. Set Environment Variables

```bash
export ACTIVITYPUB_KEY_ENCRYPTION_KEY="your-generated-key-here"
export DATABASE_URL="postgres://user:pass@localhost:5432/athena"
```

Or use a `.env` file in the project root.

## Usage

### Basic Usage

```bash
go run cmd/encrypt-activitypub-keys/main.go
```

### With Environment File

```bash
# Create .env file with your settings
cat > .env << EOF
DATABASE_URL=postgres://athena_user:password@localhost:5432/athena
ACTIVITYPUB_KEY_ENCRYPTION_KEY=your-generated-key-here
ENABLE_ACTIVITYPUB=true
EOF

# Run migration
go run cmd/encrypt-activitypub-keys/main.go
```

## Example Session

```
ActivityPub Private Key Encryption Migration Tool
==================================================
Found 15 private keys to encrypt

This will encrypt all plaintext private keys in the database.
This operation cannot be undone without the encryption key.
Make sure you have backed up your database!

Do you want to proceed? (yes/no): yes

Processing key 1/15 (actor: 550e8400-e29b-41d4-a716-446655440000)...
  Successfully encrypted
Processing key 2/15 (actor: 6ba7b810-9dad-11d1-80b4-00c04fd430c8)...
  Successfully encrypted
Processing key 3/15 (actor: 6ba7b814-9dad-11d1-80b4-00c04fd430c8)...
  Key appears to already be encrypted, marking as encrypted
...

==================================================
Migration complete!
  Encrypted: 12
  Skipped (already encrypted): 3
  Failed: 0

All private keys have been successfully encrypted!
```

## Verification

After running the migration, verify that keys are encrypted:

```bash
# Connect to your database
psql -U athena_user -d athena

# Check encryption status
SELECT
    actor_id,
    LEFT(private_key_pem, 50) as key_preview,
    keys_encrypted,
    CASE
        WHEN private_key_pem LIKE '%BEGIN RSA PRIVATE KEY%' THEN 'ERROR: PLAINTEXT'
        WHEN private_key_pem LIKE '%BEGIN PRIVATE KEY%' THEN 'ERROR: PLAINTEXT'
        ELSE 'OK: ENCRYPTED'
    END as security_status
FROM ap_actor_keys;
```

**Expected Output:**

```
                actor_id                |                   key_preview                    | keys_encrypted | security_status
----------------------------------------+--------------------------------------------------+----------------+-----------------
 550e8400-e29b-41d4-a716-446655440000   | Y2lwaGVydGV4dC1zdHJpbmctaGVyZS1iYXNlNjQtZW5j... | t              | OK: ENCRYPTED
```

**All keys should show `OK: ENCRYPTED`!**

## Idempotency

This tool is **idempotent** - you can safely run it multiple times:

- Already encrypted keys are detected and skipped
- Keys are marked as encrypted to prevent re-encryption
- No data loss if run multiple times

## Troubleshooting

### Error: "ACTIVITYPUB_KEY_ENCRYPTION_KEY is not set"

**Solution:** Set the encryption key environment variable:

```bash
export ACTIVITYPUB_KEY_ENCRYPTION_KEY="your-key-here"
```

### Error: "Failed to connect to database"

**Solution:** Check your DATABASE_URL:

```bash
export DATABASE_URL="postgres://user:password@host:5432/database"
```

### Error: "Failed to encrypt private key"

**Possible causes:**

1. Invalid key format in database
2. Corrupted data
3. Insufficient permissions

**Solution:**

1. Check database integrity
2. Verify the key data is valid PEM format
3. Check application logs for details

### No keys found to encrypt

**Possible causes:**

1. All keys are already encrypted
2. No ActivityPub actors exist yet
3. Migration has already been run

**Verification:**

```sql
SELECT COUNT(*) FROM ap_actor_keys;
SELECT COUNT(*) FROM ap_actor_keys WHERE keys_encrypted = TRUE;
```

## Security Best Practices

### 1. Protect the Encryption Key

**DO:**

- ✅ Store in a secrets manager (AWS Secrets Manager, Vault, etc.)
- ✅ Use environment variables (never hardcode)
- ✅ Backup securely (encrypted backup)
- ✅ Restrict access (least privilege)
- ✅ Rotate periodically

**DON'T:**

- ❌ Commit to version control
- ❌ Store in plaintext files
- ❌ Share via email or chat
- ❌ Log the key value
- ❌ Hardcode in application

### 2. Database Backups

**Important:** Encrypted keys in backups require the encryption key!

When restoring from backup:

1. Restore database
2. Ensure encryption key is available
3. Application will decrypt keys automatically

**Lost encryption key = Lost private keys**

### 3. Key Rotation (Future Enhancement)

Currently, key rotation requires:

1. Decrypt all keys with old key
2. Re-encrypt with new key
3. Update ACTIVITYPUB_KEY_ENCRYPTION_KEY

**Automated key rotation is planned for a future release.**

## Production Deployment

### Recommended Process

**1. Test in Staging**

```bash
# Use staging database
export DATABASE_URL="postgres://...staging..."
go run cmd/encrypt-activitypub-keys/main.go
```

**2. Verify Staging**

```bash
# Run tests
go test -v ./internal/repository/...activitypub...

# Manual verification
# Start application and test ActivityPub functionality
```

**3. Schedule Production Migration**

```bash
# Plan for maintenance window (optional - can run live)
# Communicate with users if downtime expected
```

**4. Execute Production Migration**

```bash
# Backup first!
pg_dump ... > production_backup.sql

# Run migration
export DATABASE_URL="postgres://...production..."
go run cmd/encrypt-activitypub-keys/main.go
```

**5. Verify Production**

```bash
# Check encryption status (SQL query above)
# Monitor application logs
# Test ActivityPub federation
```

### Rollback Plan

**⚠️ There is no automatic rollback!**

If you need to revert:

1. You must have the encryption key
2. Restore from database backup (before encryption)
3. Contact security team for assistance

**Prevention is better than rollback:**

- Test in staging first
- Backup before production
- Verify encryption key is backed up

## Integration with Application

After running this tool:

1. **Set encryption key in production:**

   ```bash
   export ACTIVITYPUB_KEY_ENCRYPTION_KEY="your-key"
   ```

2. **Restart application:**

   ```bash
   systemctl restart athena
   ```

3. **Verify in logs:**

   ```bash
   journalctl -u athena | grep -i "activitypub.*encryption"
   ```

The application will automatically:

- Decrypt keys when needed
- Encrypt new keys when created
- Work transparently with encrypted storage

## Support

### Questions or Issues?

1. **Review Documentation:**
   - `/docs/security/ACTIVITYPUB_KEY_SECURITY_REPORT.md` - Full security report
   - `/docs/security/` - Other security documentation

2. **Check Logs:**

   ```bash
   journalctl -u athena -n 100
   ```

3. **Contact Security Team:**
   - Critical issues: <security@athena.example>
   - General questions: <support@athena.example>

### Reporting Security Issues

**Do NOT open public issues for security vulnerabilities!**

Report security issues to: <security@athena.example>

## License

This tool is part of the Athena platform and is subject to the same license terms.

---

**Last Updated:** 2025-11-17
**Version:** 1.0.0
**Status:** Production Ready
