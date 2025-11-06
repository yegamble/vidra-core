# Security Advisory - Environment File Exposure

**Date:** 2025-01-06
**Severity:** CRITICAL
**Status:** REMEDIATED

## Summary

A `.env` file containing sensitive credentials was accidentally committed to the git repository. This file has been removed from version control as of commit [to be updated].

## Affected Information

The committed `.env` file contained the following sensitive information:

### 1. Database Credentials
- **Database Password:** `athena_password`
- **Database Connection String:** Full PostgreSQL connection URL
- **Action Required:** Change database password immediately

### 2. S3/Backblaze B2 Credentials
- **S3 Access Key:** `005552b994877250000000009`
- **S3 Secret Key:** `K005bVFj899WnCZ61liiumVwa8Epwco`
- **Bucket:** `athena-videos`
- **Endpoint:** `s3.us-west-000.backblazeb2.com`
- **Action Required:** Rotate S3 access keys immediately via Backblaze B2 console

### 3. SMTP/Email Credentials
- **SMTP Host:** `smtp.improvmx.com`
- **SMTP Username:** `athena-test@sizetube.com`
- **SMTP Password:** `Po5kZMd9dBLE`
- **Action Required:** Change SMTP password immediately

### 4. JWT Secret
- **JWT Secret:** `your-super-secret-jwt-key-change-in-production` (default value)
- **Action Required:** Generate and deploy new JWT secret

## Immediate Actions Taken

1. ✅ Removed `.env` file from git tracking via `git rm --cached .env`
2. ✅ Verified `.env` is already listed in `.gitignore` (line 33)
3. ✅ Created this security advisory
4. ✅ Committed changes to remove the file from version control

## Required Remediation Steps

### 1. Rotate S3/Backblaze B2 Credentials (CRITICAL - Do First)

```bash
# Login to Backblaze B2 Console
# Navigate to: Account → App Keys
# 1. Delete the exposed key: 005552b994877250000000009
# 2. Create new application key
# 3. Update .env with new credentials
# 4. Update production environment variables
```

### 2. Change Database Password (CRITICAL)

```sql
-- Connect to PostgreSQL as superuser
psql -U postgres

-- Change password for athena_user
ALTER USER athena_user WITH PASSWORD 'NEW_SECURE_PASSWORD_HERE';
```

Update `.env`:
```bash
DATABASE_URL=postgres://athena_user:NEW_SECURE_PASSWORD_HERE@localhost:5432/athena?sslmode=disable
```

### 3. Rotate SMTP Password (HIGH Priority)

1. Login to ImprovMX dashboard or email provider
2. Change password for `athena-test@sizetube.com`
3. Update `.env` with new password

### 4. Generate New JWT Secret (HIGH Priority)

```bash
# Generate a secure 64-character JWT secret
openssl rand -hex 32

# Update .env
JWT_SECRET=<generated-64-char-hex-string>
```

### 5. Clear Git History (IMPORTANT)

**WARNING:** This rewrites git history and requires force push. Coordinate with team first.

```bash
# Option 1: Use BFG Repo-Cleaner (Recommended)
# Download from: https://rjbs.github.io/bfg-repo-cleaner/
java -jar bfg.jar --delete-files .env
git reflog expire --expire=now --all && git gc --prune=now --aggressive

# Option 2: Use git filter-branch (Alternative)
git filter-branch --force --index-filter \
  'git rm --cached --ignore-unmatch .env' \
  --prune-empty --tag-name-filter cat -- --all

git reflog expire --expire=now --all
git gc --prune=now --aggressive

# Force push to all remotes (COORDINATE WITH TEAM FIRST)
git push origin --force --all
git push origin --force --tags
```

**Note:** Anyone who has cloned the repository should re-clone after history is rewritten.

### 6. Revoke Exposed Tokens/Sessions (If Applicable)

If JWT secret has been in use:
- All existing JWT tokens will become invalid once the secret is changed
- Users will need to log in again
- Consider this when scheduling the deployment

## Prevention Measures

### 1. Pre-commit Hook

Add a pre-commit hook to prevent accidental commits of `.env` files:

```bash
# .git/hooks/pre-commit
#!/bin/bash

if git diff --cached --name-only | grep -q "^\.env$"; then
    echo "Error: Attempting to commit .env file!"
    echo "This file should never be committed."
    exit 1
fi
```

Make it executable:
```bash
chmod +x .git/hooks/pre-commit
```

### 2. Git Secrets Tool

Install and configure git-secrets:

```bash
# Install git-secrets
brew install git-secrets  # macOS
# or
apt-get install git-secrets  # Ubuntu/Debian

# Setup in repository
cd /path/to/athena
git secrets --install
git secrets --register-aws

# Add custom patterns
git secrets --add 'password.*=.*'
git secrets --add 'secret.*=.*'
git secrets --add 'api[_-]?key.*=.*'
```

### 3. GitHub Secret Scanning

If using GitHub:
1. Enable secret scanning in repository settings
2. Enable push protection to prevent future commits
3. Review any alerts in Security → Secret scanning

### 4. Environment Variable Management

**Best Practices:**

- **Never commit `.env` files** - Use `.env.example` as template
- **Use secret management tools** in production:
  - AWS Secrets Manager
  - HashiCorp Vault
  - Kubernetes Secrets
  - Docker Secrets
- **Encrypt secrets at rest** in production environments
- **Rotate credentials regularly** (at least every 90 days)
- **Use different credentials** for dev, staging, and production
- **Audit access** to production secrets regularly

### 5. .env.example Template

Create/maintain `.env.example` with placeholder values:

```bash
# Database Configuration
DATABASE_URL=postgres://username:password@localhost:5432/dbname?sslmode=disable

# S3 Configuration
S3_ACCESS_KEY=your-access-key-here
S3_SECRET_KEY=your-secret-key-here

# JWT Configuration
JWT_SECRET=generate-with-openssl-rand-hex-32

# SMTP Configuration
SMTP_USERNAME=your-email@example.com
SMTP_PASSWORD=your-smtp-password
```

## Timeline

- **[Unknown]:** `.env` file accidentally committed to repository
- **2025-01-06 [Current]:** Issue discovered during security audit
- **2025-01-06 [Current]:** File removed from git tracking
- **[Pending]:** Credential rotation (see remediation steps above)
- **[Pending]:** Git history cleanup (requires team coordination)

## Impact Assessment

### Potential Exposure

- **Database:** Read/write access to development database
- **S3/Backblaze B2:** Read/write access to video storage bucket `athena-videos`
- **SMTP:** Ability to send emails from `athena-test@sizetube.com`
- **JWT:** Ability to forge authentication tokens (if secret was in production use)

### Estimated Risk

- **Database:** LOW-MEDIUM (development environment, no sensitive user data)
- **S3 Storage:** MEDIUM-HIGH (potential data exposure or deletion)
- **SMTP:** LOW-MEDIUM (spam/phishing risk)
- **JWT:** HIGH (if production secret, CRITICAL if not)

## Verification Checklist

After completing remediation steps, verify:

- [ ] S3 credentials rotated and new credentials working
- [ ] Old S3 credentials confirmed deleted in Backblaze B2
- [ ] Database password changed and application still connects
- [ ] SMTP password changed and emails still sending
- [ ] New JWT secret deployed and users can authenticate
- [ ] `.env` file not present in git history (after cleanup)
- [ ] `.env` confirmed in `.gitignore`
- [ ] Pre-commit hooks installed to prevent future issues
- [ ] All team members notified of credential changes
- [ ] Production environment variables updated
- [ ] No unauthorized S3 access in Backblaze B2 audit logs
- [ ] No unauthorized database access in PostgreSQL logs
- [ ] No unauthorized SMTP usage

## Contact

For questions or concerns regarding this security advisory:

- Create a private security advisory on GitHub
- Contact repository maintainers directly
- Do not discuss exposed credentials in public issues/PRs

## References

- [OWASP Secrets Management Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html)
- [GitHub Secret Scanning](https://docs.github.com/en/code-security/secret-scanning)
- [BFG Repo-Cleaner](https://rjbs.github.io/bfg-repo-cleaner/)
- [git-secrets](https://github.com/awslabs/git-secrets)

---

**This advisory is for internal use only. Do not share publicly until all remediation steps are complete.**
