# Email Verification API Documentation

## Overview

The email verification system ensures users have access to the email addresses they register with. After registration, users receive an email with a verification link and code. Users must verify their email before accessing protected features like uploading videos, commenting, or subscribing.

## Endpoints

### 1. Verify Email

**POST** `/api/v1/auth/verify-email`

Verifies a user's email address using either a token (from email link) or a verification code.

#### Request Body

```json
{
  "token": "string",  // Optional: verification token from email link
  "code": "string"    // Optional: 6-digit verification code (requires authentication)
}
```

Either `token` or `code` must be provided, not both.

#### Response

**Success (200 OK)**

```json
{
  "message": "Email verified successfully",
  "success": true
}
```

**Error Responses**

- `400 Bad Request` - Invalid or expired token/code
- `401 Unauthorized` - Authentication required when using code

### 2. Resend Verification Email

**POST** `/api/v1/auth/resend-verification`

Sends a new verification email to the specified address.

#### Request Body

```json
{
  "email": "user@example.com"
}
```

#### Response

**Success (200 OK)**

```json
{
  "message": "Verification email sent successfully",
  "success": true
}
```

**Error Responses**

- `400 Bad Request` - Email already verified
- `429 Too Many Requests` - Rate limit exceeded (max 1 request per 5 minutes)

### 3. Get Verification Status

**GET** `/api/v1/auth/verification-status`

Returns the current user's email verification status (requires authentication).

#### Response

**Success (200 OK)**

```json
{
  "email_verified": false,
  "message": "Email verification status retrieved"
}
```

**Error Response**

- `401 Unauthorized` - Authentication required

## Email Verification Flow

### Registration Flow

1. User registers with email and password
2. System creates user account with `email_verified = false`
3. System generates verification token and 6-digit code (valid for 24 hours)
4. Verification email is sent asynchronously
5. User receives auth tokens but has limited access

### Verification Options

#### Option 1: Link Verification

1. User clicks verification link in email
2. Frontend extracts token from URL
3. Frontend calls `/api/v1/auth/verify-email` with token
4. Email is marked as verified

#### Option 2: Code Verification

1. User logs into account
2. User enters 6-digit code from email
3. Frontend calls `/api/v1/auth/verify-email` with code (authenticated request)
4. Email is marked as verified

### Protected Endpoints

The following endpoints require email verification:

- **POST** `/api/v1/videos` - Upload video
- **POST** `/api/v1/videos/{id}/comments` - Post comment
- **POST** `/api/v1/users/{id}/subscribe` - Subscribe to user
- **POST** `/api/v1/messages` - Send message
- **PUT** `/api/v1/users/profile` - Update profile

Unverified users receive:

```json
{
  "error": {
    "code": "EMAIL_NOT_VERIFIED",
    "message": "Please verify your email address to access this resource"
  },
  "success": false
}
```

## Security Considerations

1. **Token Security**: Tokens are cryptographically secure random strings
2. **Rate Limiting**: Resend requests are limited to prevent spam
3. **Token Expiry**: Tokens expire after 24 hours
4. **Single Use**: Tokens can only be used once
5. **User Privacy**: Resend endpoint doesn't reveal if email exists

## Database Schema

### email_verification_tokens Table

```sql
CREATE TABLE email_verification_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token VARCHAR(255) NOT NULL UNIQUE,
    code VARCHAR(6) NOT NULL,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    used_at TIMESTAMP WITH TIME ZONE
);
```

### users Table (relevant fields)

```sql
ALTER TABLE users ADD COLUMN email_verified BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMP WITH TIME ZONE;
```

## Email Templates

### Verification Email

Subject: "Verify Your Email Address"

The email contains:

- Personalized greeting with username
- Verification link button
- 6-digit verification code
- 24-hour expiry notice
- Security notice for unintended recipients

### Resend Verification Email

Subject: "New Verification Code"

Similar to initial email but acknowledges this is a resend request.

## Testing

### Unit Tests

- Token generation and validation
- Code verification logic
- Expiry handling
- Rate limiting

### Integration Tests

- Complete registration and verification flow
- Unverified user restrictions
- Token expiry scenarios
- Concurrent verification attempts

## Configuration

Required environment variables:

```bash
# Email Service
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_USERNAME=noreply@example.com
SMTP_PASSWORD=secret
FROM_ADDRESS=noreply@example.com
FROM_NAME="Athena Platform"
BASE_URL=https://example.com

# Verification Settings
VERIFICATION_TOKEN_EXPIRY=24h
VERIFICATION_RESEND_COOLDOWN=5m
```
