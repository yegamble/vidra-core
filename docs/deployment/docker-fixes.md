# Docker Setup Fixes

## Issues Fixed

### 1. IPFS Healthcheck Issue

**Problem**: The IPFS healthcheck was failing because the `/api/v0/version` endpoint requires a POST request, not GET.

**Solution**: Updated the healthcheck command in `docker-compose.yml`:

```yaml
healthcheck:
  test: ["CMD", "wget", "--post-data=''", "-qO-", "http://localhost:5001/api/v0/version"]
```

### 2. Database Initialization

**Problem**: The `init-shared-db.sql` file was missing the new messaging system tables.

**Solution**: Added the complete messaging system schema to `init-shared-db.sql`:

- `messages` table with proper indexes and triggers
- `conversations` table with participant ordering
- Fixed the `ensure_conversation_order()` function to properly handle INSERT operations

### 3. SQL Function Bug

**Problem**: The `ensure_conversation_order()` function was trying to access `OLD.participant_two_id` during INSERT operations where `OLD` doesn't exist.

**Solution**: Fixed the function to use a temporary variable for swapping:

```sql
CREATE OR REPLACE FUNCTION ensure_conversation_order()
RETURNS TRIGGER AS $$
DECLARE
    temp_id UUID;
BEGIN
    IF NEW.participant_one_id > NEW.participant_two_id THEN
        temp_id := NEW.participant_one_id;
        NEW.participant_one_id := NEW.participant_two_id;
        NEW.participant_two_id := temp_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
```

## Verification

After these fixes, `make docker-up` now works successfully:

1. ✅ PostgreSQL starts healthy with all tables and extensions
2. ✅ Redis starts healthy with persistence enabled
3. ✅ IPFS starts healthy with server profile
4. ✅ Application starts healthy and passes readiness checks

### Test Commands

```bash
# Check all services are running
docker-compose ps

# Test health endpoint
curl http://localhost:8080/health

# Test readiness endpoint
curl http://localhost:8080/ready
```

## Services Available

- **Application**: <http://localhost:8080>
- **PostgreSQL**: localhost:5432 (vidra_user/vidra_password/vidra)
- **Redis**: localhost:6379
- **IPFS API**: <http://localhost:5001>

The messaging system is now fully integrated and will be initialized automatically when the Docker stack starts.
