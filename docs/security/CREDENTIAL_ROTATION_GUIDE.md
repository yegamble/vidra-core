# Credential Rotation Guide

**Date:** 2025-11-17
**Status:** CRITICAL - Immediate Action Required
**Priority:** P0

## Executive Summary

This document outlines the credential rotation strategy for the Athena platform following the discovery of `.env` files in the git history. While the exposed credentials appear to be development/example values, we must rotate all credentials as a security best practice.

## Credentials Found in Git History

### Investigation Results

```bash
git log --all --full-history --source --pretty=format:"" --name-only -- .env
```

**Finding:** `.env` file was committed to git history (3 instances found)

### Exposed Credential Types

Based on `.env.example`, the following credential types may have been exposed:

1. **Database Credentials**
   - PostgreSQL connection strings
   - Shadow database credentials

2. **JWT Secrets**
   - JWT signing keys

3. **IOTA Configuration**
   - Node URLs (not sensitive)

4. **Redis URLs**
   - Redis connection strings (if password-protected)

## Immediate Actions Required

### 1. Database Credentials Rotation ⚠️

**If Production Credentials Were Exposed:**

```bash
# Step 1: Create new database user
sudo -u postgres psql
CREATE USER athena_user_new WITH PASSWORD 'new-secure-password-here';
GRANT ALL PRIVILEGES ON DATABASE athena TO athena_user_new;

# Step 2: Update application configuration
export DATABASE_URL="postgres://athena_user_new:new-secure-password@host:5432/athena?sslmode=require"

# Step 3: Test new credentials
psql $DATABASE_URL -c "SELECT 1;"

# Step 4: Update all deployment configurations
# - Kubernetes secrets
# - Docker Compose files
# - CI/CD environment variables

# Step 5: Revoke old credentials (AFTER verifying new ones work)
sudo -u postgres psql
DROP USER athena_user;
```

### 2. JWT Secret Rotation ⚠️

**Critical:** Rotating JWT secrets will invalidate all existing user sessions.

```bash
# Generate new JWT secret (64 bytes recommended)
export NEW_JWT_SECRET=$(openssl rand -base64 64)

# Update configuration
echo "JWT_SECRET=$NEW_JWT_SECRET" >> .env

# Restart application
# Note: All users will be logged out
systemctl restart athena
```

**User Impact:**
- All active sessions invalidated
- Users must re-authenticate
- Consider scheduling during maintenance window

### 3. Redis Credentials Rotation

**If Redis is password-protected:**

```bash
# Step 1: Set new Redis password
redis-cli
CONFIG SET requirepass "new-secure-redis-password"
CONFIG REWRITE

# Step 2: Update application configuration
export REDIS_URL="redis://:new-secure-redis-password@localhost:6379/0"

# Step 3: Restart application
systemctl restart athena
```

### 4. ActivityPub Key Encryption Key 🔐

**NEW REQUIREMENT** from security fixes:

```bash
# Generate encryption key for ActivityPub private keys
export ACTIVITYPUB_KEY_ENCRYPTION_KEY=$(openssl rand -base64 48)

# Add to secure configuration management
# This key is required for the new encryption-at-rest feature
```

### 5. IOTA Wallet Master Key 🔐

**NEW REQUIREMENT** from security fixes:

```bash
# For production (AWS CloudHSM recommended)
export HSM_PROVIDER=cloudhsm
export AWS_CLOUDHSM_CLUSTER_ID=cluster-abc123
export WALLET_MASTER_KEY_ID=wallet-master-key-2024

# For development/staging (Software HSM)
export HSM_PROVIDER=software
export WALLET_MASTER_KEY_BASE64=$(openssl rand -base64 32)
```

## Git History Cleanup

### Option 1: BFG Repo-Cleaner (Recommended)

```bash
# Install BFG
brew install bfg  # macOS
# or download from https://rtyley.github.io/bfg-repo-cleaner/

# Clone a fresh mirror
git clone --mirror https://github.com/yegamble/athena.git athena-mirror
cd athena-mirror

# Remove .env files from history
bfg --delete-files .env

# Clean up and push
git reflog expire --expire=now --all
git gc --prune=now --aggressive
git push --force

# All collaborators must re-clone the repository
```

### Option 2: git-filter-repo (Alternative)

```bash
# Install git-filter-repo
pip3 install git-filter-repo

# Remove .env from history
git filter-repo --path .env --invert-paths

# Force push
git push origin --force --all
git push origin --force --tags
```

### Option 3: GitHub Repository Reset (Nuclear Option)

If credentials are highly sensitive:

1. Create new repository
2. Push only the latest code (not history)
3. Archive old repository
4. Update all references

## Prevention Measures

### 1. Update .gitignore

Ensure `.gitignore` contains:

```gitignore
# Environment files
.env
.env.local
.env.*.local
.env.production
*.env

# Credentials
**/credentials.json
**/secrets.yaml
**/*secret*
**/*credential*
**/*password*

# Security keys
*.pem
*.key
*.p12
*.pfx
id_rsa*
```

### 2. Pre-commit Hooks

Install git-secrets to prevent credential commits:

```bash
# Install git-secrets
brew install git-secrets  # macOS
# or follow: https://github.com/awslabs/git-secrets

# Initialize in repository
cd /path/to/athena
git secrets --install
git secrets --register-aws  # Prevents AWS credentials
git secrets --add-provider -- cat .gitignore  # Prevents patterns in .gitignore
```

### 3. Environment Variable Management

**Development:**
- Use `.env.local` (git-ignored)
- Copy from `.env.example` and customize
- Never commit `.env.local`

**Production:**
- Use secret management systems:
  - **AWS:** AWS Secrets Manager / Parameter Store
  - **Kubernetes:** Sealed Secrets / External Secrets Operator
  - **Docker:** Docker Secrets
  - **Cloud:** Google Secret Manager / Azure Key Vault

### 4. CI/CD Secrets

**GitHub Actions:**
```yaml
# Use repository secrets
steps:
  - name: Deploy
    env:
      DATABASE_URL: ${{ secrets.DATABASE_URL }}
      JWT_SECRET: ${{ secrets.JWT_SECRET }}
```

**GitLab CI:**
```yaml
# Use CI/CD variables (masked)
variables:
  DATABASE_URL: $CI_DATABASE_URL
  JWT_SECRET: $CI_JWT_SECRET
```

## Verification Checklist

After rotation, verify:

- [ ] Database connection works with new credentials
- [ ] Application starts successfully
- [ ] Users can authenticate (JWT working)
- [ ] Redis connection established
- [ ] ActivityPub keys encrypted at rest
- [ ] IOTA wallet seeds encrypted with HSM
- [ ] Old credentials revoked/deleted
- [ ] `.env` removed from git history
- [ ] Pre-commit hooks installed
- [ ] Team notified of changes
- [ ] Documentation updated

## Timeline for Rotation

**Immediate (Within 24 hours):**
- [ ] Assess impact (production vs development credentials)
- [ ] Generate new credentials
- [ ] Plan rotation schedule

**Short-term (Within 7 days):**
- [ ] Rotate all database credentials
- [ ] Rotate JWT secrets (during maintenance window)
- [ ] Clean git history with BFG
- [ ] Install pre-commit hooks

**Long-term (Within 30 days):**
- [ ] Implement secret management system (AWS Secrets Manager)
- [ ] Automate credential rotation (90-day policy)
- [ ] Security audit of all credential storage
- [ ] Team training on credential management

## Impact Assessment

### If Development Credentials Only

**Risk Level:** LOW
**Action Required:** Preventive measures (git history cleanup, pre-commit hooks)

### If Production Credentials Exposed

**Risk Level:** CRITICAL
**Action Required:** Immediate rotation, security incident response

## Monitoring and Alerting

After rotation, monitor for:

1. **Failed Authentication Attempts**
   - Spike in failed logins (old credentials)
   - Monitor application logs

2. **Database Access**
   - Monitor for connections from unexpected IPs
   - Check PostgreSQL logs

3. **Suspicious Activity**
   - Unusual API calls
   - Data exfiltration attempts

## Support and Questions

**Security Team Contact:**
- Email: security@athena.example.com
- Slack: #security-incidents

**Escalation:**
- P0 (Critical): Page on-call engineer
- P1 (High): 4-hour response SLA

## References

- [OWASP Credential Stuffing Prevention](https://cheatsheetseries.owasp.org/cheatsheets/Credential_Stuffing_Prevention_Cheat_Sheet.html)
- [GitHub: Removing Sensitive Data](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/removing-sensitive-data-from-a-repository)
- [NIST SP 800-63B: Digital Identity Guidelines](https://pages.nist.gov/800-63-3/sp800-63b.html)

---

**Document Version:** 1.0
**Last Updated:** 2025-11-17
**Next Review:** 2025-12-17
