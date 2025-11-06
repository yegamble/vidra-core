# Backblaze S3 Migration Setup Guide

This document explains the Backblaze S3 integration that has been implemented and provides instructions for completing the setup.

## Overview

The system now supports migrating videos from local storage to Backblaze B2 (S3-compatible storage). When S3 is enabled and videos are migrated, the streaming service automatically redirects to S3 URLs instead of serving from local storage.

## What Has Been Implemented

### 1. Storage Abstraction Layer (`internal/storage/`)

- **`backend.go`**: Interface for multiple storage backends (local, S3, IPFS)
- **`s3_backend.go`**: Full S3-compatible backend implementation supporting:
  - Upload/download operations
  - Signed URL generation for private videos
  - Metadata retrieval
  - Batch deletion
  - Support for Backblaze B2, AWS S3, DigitalOcean Spaces, etc.

### 2. Database Schema (`migrations/055_add_s3_storage_fields.sql`)

New fields added to the `videos` table:
- `s3_urls` (JSONB): Maps variant names to S3 URLs
- `storage_tier` (VARCHAR): Tracks storage tier (hot/warm/cold)
- `s3_migrated_at` (TIMESTAMP): Migration timestamp
- `local_deleted` (BOOLEAN): Indicates if local files were deleted

Includes optimized indexes for migration queries.

### 3. Migration Service (`internal/usecase/migration/s3_migration_service.go`)

Features:
- Migrate individual videos or batches
- Upload all video variants to S3
- Migrate HLS playlists and segments
- Migrate thumbnails and previews
- Optional local file deletion after successful migration
- Comprehensive error handling and logging

### 4. Video Serving with S3 Support (`internal/httpapi/handlers/video/hls_s3_handler.go`)

The handler now:
- Checks if S3 is enabled and video is migrated
- Redirects to S3 URLs for migrated videos
- Generates signed URLs for private videos (1-hour expiration)
- Falls back to local serving if needed
- Maintains proper caching headers

### 5. CLI Tools

#### S3 Migration Tool (`cmd/s3migrate/main.go`)
```bash
# Test S3 connection only
./bin/s3migrate --test

# Migrate a specific video
./bin/s3migrate --video-id=<UUID>

# Migrate a batch of videos
./bin/s3migrate --batch=10

# Dry run (see what would be migrated)
./bin/s3migrate --dry-run --batch=10

# Migrate and delete local files
./bin/s3migrate --delete-local --batch=10
```

#### S3 Diagnostic Tool (`cmd/s3test/main.go`)
```bash
# Check S3 configuration and connectivity
./bin/s3test
```

### 6. Configuration

S3 configuration in `.env`:
```bash
# Enable S3
ENABLE_S3=true

# Backblaze B2 Configuration
S3_ENDPOINT=https://s3.us-west-000.backblazeb2.com
S3_BUCKET=athena-videos
S3_ACCESS_KEY=005552b994877250000000009
S3_SECRET_KEY=K005bVFj899WnCZ61liiumVwa8Epwco
S3_REGION=us-west-000
```

## Setup Instructions

### Step 1: Create Backblaze B2 Bucket

The current credentials are failing with 403 Forbidden because either:
1. The bucket "athena-videos" doesn't exist
2. The application key doesn't have access to it

**To fix this:**

1. Log into your Backblaze account at https://www.backblaze.com/b2/
2. Navigate to "Buckets"
3. Either:
   - Create a new bucket named `athena-videos` (must be globally unique)
   - Find the existing bucket name and update `.env` with the correct name
4. Ensure the bucket is in the correct region (us-west-000)
5. Verify your application key has access to this bucket

**Alternative:** You can use a different bucket name. If "athena-videos" is taken, try:
- `athena-videos-<your-company>`
- `<your-company>-athena-videos`
- Any unique name you prefer

Update `.env` with the correct bucket name:
```bash
S3_BUCKET=your-actual-bucket-name
```

### Step 2: Verify Application Key Permissions

Ensure your Backblaze application key has these permissions:
- `listBuckets` (optional, for diagnostic)
- `listFiles`
- `readFiles`
- `writeFiles`
- `deleteFiles`

If using a bucket-scoped key (recommended for security), ensure it's scoped to your video bucket.

### Step 3: Apply Database Migration

```bash
# Using Atlas (if configured)
atlas migrate apply --dir "file://migrations" --url "postgres://..."

# Or using psql directly
psql -h localhost -U athena_user -d athena -f migrations/055_add_s3_storage_fields.sql
```

### Step 4: Test S3 Connection

```bash
# Build the diagnostic tool
go build -o bin/s3test ./cmd/s3test/

# Run the test
./bin/s3test
```

Expected output:
```
✓ Bucket exists and is accessible
✓ Bucket contains X objects
✓ All basic tests passed!
```

### Step 5: Test Migration Tool

```bash
# Build the migration tool
go build -o bin/s3migrate ./cmd/s3migrate/

# Test connection only
./bin/s3migrate --test

# Expected output:
# ✓ S3 backend created successfully
# ✓ Upload successful
# ✓ Download successful
# ✓ S3 connection test successful!
```

### Step 6: Perform Test Migration

```bash
# First, do a dry run to see what would be migrated
./bin/s3migrate --dry-run --batch=5

# If the output looks correct, migrate a small batch
./bin/s3migrate --batch=1

# Check the logs for success
# Check your Backblaze bucket to verify files were uploaded
```

### Step 7: Integrate into Main Application

Once testing is successful, you need to integrate the S3 handler into your main application:

1. Update `cmd/server/main.go` to initialize the S3 backend:

```go
// Create S3 backend if enabled
var s3Backend storage.StorageBackend
if cfg.EnableS3 {
    s3Backend, err = storage.NewS3Backend(storage.S3Config{
        Endpoint:  cfg.S3Endpoint,
        Bucket:    cfg.S3Bucket,
        AccessKey: cfg.S3AccessKey,
        SecretKey: cfg.S3SecretKey,
        Region:    cfg.S3Region,
        PathStyle: true,
    })
    if err != nil {
        log.Fatalf("Failed to create S3 backend: %v", err)
    }
    log.Info("✓ S3 backend initialized")
}
```

2. Update your HLS routes to use the new handler:

```go
// Replace the old HLS handler with the new S3-aware one
r.Get("/api/v1/hls/*", video.HLSHandlerWithS3(videoRepo, cfg, s3Backend))
```

## Production Deployment

### Automatic Migration

To automatically migrate videos, you can:

1. **Create a scheduled job** (cron or k8s CronJob):
```bash
# Migrate 10 videos every hour
0 * * * * /app/bin/s3migrate --batch=10 --delete-local
```

2. **Create a background worker** that runs continuously:
```go
// In your main application
if cfg.EnableS3 {
    go func() {
        ticker := time.NewTicker(5 * time.Minute)
        for range ticker.C {
            migrated, _ := migrationService.MigrateBatch(ctx, 10)
            log.Infof("Migrated %d videos to S3", migrated)
        }
    }()
}
```

### Migration Strategy

Recommended approach:
1. Start with `--delete-local=false` to keep backups
2. Migrate in small batches (10-50 videos)
3. Monitor S3 costs and bandwidth
4. Verify streaming works from S3
5. Once confident, enable `--delete-local=true`

### Cost Optimization

Backblaze B2 pricing (as of 2025):
- Storage: $0.005/GB/month (first 10GB free)
- Download: $0.01/GB (first 1GB/day free)
- API calls: Free for Class C (uploads), $0.004/10k for Class B (downloads)

**To minimize costs:**
- Use signed URLs for private videos (already implemented)
- Set appropriate cache headers (already implemented)
- Consider CDN in front of Backblaze for high-traffic videos
- Monitor bandwidth usage

### Monitoring

Key metrics to monitor:
- Migration success rate
- S3 upload/download errors
- Storage costs
- Bandwidth usage
- API call counts

Add these to your Prometheus metrics:
```go
s3_migration_total{status="success|failed"}
s3_upload_duration_seconds
s3_download_duration_seconds
s3_signed_url_generations_total
```

## Troubleshooting

### 403 Forbidden Errors

**Problem:** Cannot access bucket
**Solutions:**
1. Verify bucket exists and name is correct
2. Check application key permissions
3. Ensure key is scoped to correct bucket
4. Verify endpoint URL matches bucket region

### Slow Migration

**Problem:** Migration takes too long
**Solutions:**
1. Increase batch size with `--batch=50`
2. Run multiple migration processes in parallel (different video IDs)
3. Check network bandwidth
4. Monitor Backblaze API rate limits

### Streaming Issues After Migration

**Problem:** Videos don't play after S3 migration
**Solutions:**
1. Check CORS configuration on Backblaze bucket
2. Verify S3 URLs are publicly accessible (or signed for private videos)
3. Check browser console for CORS errors
4. Ensure HLS playlists reference correct S3 URLs

### Local Files Not Deleted

**Problem:** Local files remain after migration with `--delete-local`
**Solutions:**
1. Check migration service logs for errors
2. Verify filesystem permissions
3. Ensure video.LocalDeleted flag is set in database
4. Check for file locks (videos being accessed during migration)

## Architecture Diagram

```
┌──────────────────────────────────────────────────────────┐
│                     Video Upload                          │
└───────────────────────┬──────────────────────────────────┘
                        │
                        v
┌───────────────────────────────────────────────────────────┐
│            Local Storage (Hot Tier)                       │
│  ./storage/web-videos/                                    │
│  ./storage/streaming-playlists/hls/                       │
└───────────────────────┬──────────────────────────────────┘
                        │
                        │ S3 Migration Service
                        │ (Batch or Individual)
                        v
┌───────────────────────────────────────────────────────────┐
│            Backblaze B2 (Cold Tier)                       │
│  s3://athena-videos/videos/{id}/                          │
│    - {variant}.mp4                                        │
│    - hls/master.m3u8                                      │
│    - hls/{variant}/index.m3u8                             │
│    - hls/{variant}/segment_*.ts                           │
│    - thumbnail.jpg                                        │
│    - preview.webp                                         │
└───────────────────────┬──────────────────────────────────┘
                        │
                        │ HLS Handler
                        │ (Redirect or Serve)
                        v
┌───────────────────────────────────────────────────────────┐
│               Video Player (Client)                       │
│  - Receives S3 URLs for migrated videos                  │
│  - Signed URLs for private videos                         │
│  - Falls back to local if not migrated                    │
└───────────────────────────────────────────────────────────┘
```

## Next Steps

1. **Create/verify Backblaze bucket** with correct name
2. **Run diagnostic tests** to confirm connectivity
3. **Apply database migration** (055_add_s3_storage_fields.sql)
4. **Test migration tool** with 1-2 videos
5. **Integrate S3 handler** into main application
6. **Set up automatic migration** (cron or background worker)
7. **Monitor costs and performance**
8. **Consider CDN integration** for high-traffic scenarios

## Support

For issues or questions:
1. Check Backblaze B2 documentation: https://www.backblaze.com/b2/docs/
2. Review AWS SDK v2 for Go docs: https://aws.github.io/aws-sdk-go-v2/
3. Check application logs for detailed error messages
4. Use the diagnostic tool (`./bin/s3test`) to verify configuration

## Files Modified/Created

### New Files
- `internal/storage/backend.go` - Storage abstraction interface
- `internal/storage/s3_backend.go` - S3 backend implementation
- `internal/usecase/migration/s3_migration_service.go` - Migration service
- `internal/httpapi/handlers/video/hls_s3_handler.go` - S3-aware HLS handler
- `cmd/s3migrate/main.go` - CLI migration tool
- `cmd/s3test/main.go` - CLI diagnostic tool
- `migrations/055_add_s3_storage_fields.sql` - Database migration
- `docs/S3_MIGRATION_SETUP.md` - This documentation

### Modified Files
- `.env` - Added S3 configuration
- `internal/domain/video.go` - Added S3-related fields
- `internal/repository/video_repository.go` - Added GetVideosForMigration method and updated Update method
- `internal/port/video.go` - Added GetVideosForMigration to interface
- `go.mod` / `go.sum` - Added AWS SDK v2 dependencies

## Security Considerations

1. **Never commit credentials** to version control
2. **Use environment variables** for sensitive configuration
3. **Implement signed URLs** for private videos (✓ done)
4. **Set appropriate bucket policies** in Backblaze
5. **Use HTTPS** for all S3 communication (✓ done)
6. **Rotate access keys** periodically
7. **Monitor access logs** for suspicious activity
8. **Use bucket-scoped application keys** when possible
