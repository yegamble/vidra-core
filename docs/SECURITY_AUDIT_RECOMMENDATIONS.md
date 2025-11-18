# Security Audit: Additional XSS Protection Recommendations

## Current Implementation

✅ **COMPLETED**: Comment system XSS protection
- Comment body sanitization
- Comment flag details sanitization
- Comprehensive test coverage

## Additional Areas Requiring Sanitization

Based on code analysis, the following user-generated content endpoints should also implement HTML sanitization:

### 1. Video Metadata
**Risk Level**: MEDIUM
**Files to Update**:
- `/home/user/athena/internal/httpapi/handlers/video/videos.go`
- `/home/user/athena/internal/usecase/video/service.go`

**Fields Requiring Sanitization**:
- Video title - Use `SanitizeStrictText()` (no HTML allowed)
- Video description - Use `SanitizeCommentHTML()` (allow basic formatting)
- Video tags - Use `SanitizeStrictText()` (no HTML allowed)

### 2. Channel Information
**Risk Level**: MEDIUM
**Files to Update**:
- `/home/user/athena/internal/httpapi/handlers/channel/channels.go`
- `/home/user/athena/internal/usecase/channel/service.go`

**Fields Requiring Sanitization**:
- Channel name - Use `SanitizeStrictText()`
- Channel description - Use `SanitizeCommentHTML()`
- Channel bio - Use `SanitizeCommentHTML()`

### 3. Playlist Data
**Risk Level**: LOW-MEDIUM
**Files to Update**:
- `/home/user/athena/internal/httpapi/handlers/social/playlists.go`

**Fields Requiring Sanitization**:
- Playlist title - Use `SanitizeStrictText()`
- Playlist description - Use `SanitizeCommentHTML()`

### 4. User Profiles
**Risk Level**: MEDIUM
**Files to Update**:
- User profile handlers (if they exist)

**Fields Requiring Sanitization**:
- Username - Use `SanitizeStrictText()`
- User bio - Use `SanitizeCommentHTML()`
- Display name - Use `SanitizeStrictText()`

### 5. Messages/Notifications
**Risk Level**: HIGH (direct user-to-user communication)
**Files to Check**:
- `/home/user/athena/internal/httpapi/handlers/messaging/`

**Fields Requiring Sanitization**:
- Message content - Use `SanitizeCommentHTML()`
- Notification text - Use `SanitizeStrictText()`

## Implementation Strategy

### Phase 1: Critical User-Generated Content (HIGH Priority)
1. ✅ Comments (COMPLETED)
2. Messages/Notifications (if direct messaging exists)
3. Video descriptions

### Phase 2: Metadata and Profiles (MEDIUM Priority)
1. Channel information
2. User profiles
3. Video titles and tags

### Phase 3: Additional Content (LOW Priority)
1. Playlist information
2. Category descriptions
3. Any other user-generated text

## Quick Implementation Guide

For each identified endpoint:

```go
// In the service layer, before storing:
import "athena/internal/security"

// For plain text fields (no HTML):
sanitizedTitle := security.SanitizeStrictText(req.Title)

// For rich text fields (allow formatting):
sanitizedDescription := security.SanitizeCommentHTML(req.Description)

// Validate after sanitization:
if len(sanitizedTitle) == 0 {
    return fmt.Errorf("title is empty after sanitization")
}
```

## Testing Requirements

For each sanitized field, add tests for:
1. Script tag injection
2. Event handler injection
3. JavaScript URL injection
4. Legitimate content preservation
5. Maximum length validation after sanitization

## Security Headers

Recommend adding these headers to all responses:
```go
w.Header().Set("X-Content-Type-Options", "nosniff")
w.Header().Set("X-Frame-Options", "DENY")
w.Header().Set("X-XSS-Protection", "1; mode=block")
w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'")
```

## Database Considerations

### Retroactive Sanitization
For existing data in production:
1. Create a migration script to sanitize existing content
2. Back up the database before running
3. Test on staging environment first
4. Monitor for data loss or corruption

Example migration:
```sql
-- Backup first!
CREATE TABLE comments_backup AS SELECT * FROM comments;

-- Then sanitize in batches using the Go application
-- Do NOT try to sanitize in SQL - use the Go sanitization functions
```

## Monitoring

Implement logging for:
1. Sanitization failures (content empty after sanitization)
2. Repeated XSS attempts from same user/IP
3. Unusual patterns in user content

## Regular Security Reviews

Schedule quarterly reviews to:
1. Update bluemonday library
2. Test against new XSS vectors
3. Review OWASP Top 10 updates
4. Audit new features for XSS risks

## Conclusion

The comment system XSS protection is now fully implemented. However, other user-generated content areas remain potentially vulnerable and should be addressed based on the priority levels indicated above.

**Immediate Next Steps**:
1. Review messaging/notification system for XSS vulnerabilities
2. Implement sanitization for video metadata
3. Add security headers to all API responses
4. Create a sanitization migration plan for existing data