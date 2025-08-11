# Postman Collection for Athena Auth API

This directory contains Postman collections and environments for testing the Athena PeerTube backend authentication endpoints.

## Files

- `athena-auth.postman_collection.json` - Main collection with auth endpoints
- `athena-auth.postman_environment.json` - Environment variables for testing

## Features

### Authentication Flow Testing
- **Register**: Create new user accounts with validation
- **Login**: Authenticate users and receive JWT tokens
- **Refresh Token**: Renew access tokens using refresh tokens
- **Logout**: Invalidate user sessions

### Error Scenarios
- Invalid credentials testing
- Missing required fields validation
- Invalid token handling
- Unauthorized access attempts

### CI/CD Integration
- Automated test execution in GitHub Actions
- Environment variable management
- Test result reporting
- HTML report generation

## Usage

### Local Testing with Postman GUI
1. Import `athena-auth.postman_collection.json` into Postman
2. Import `athena-auth.postman_environment.json` as environment
3. Set `base_url` to your local server (default: `http://localhost:8080`)
4. Run the collection

### Command Line Testing with Newman
```bash
# Install Newman
npm install -g newman

# Run tests
newman run athena-auth.postman_collection.json \
  --environment athena-auth.postman_environment.json \
  --env-var "base_url=http://localhost:8080"
```

### CI/CD Integration
The collection is automatically executed in GitHub Actions on:
- Push to `main` or `develop` branches
- Pull requests to `main`
- Manual workflow dispatch

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `base_url` | API base URL | `http://localhost:8080` |
| `access_token` | JWT access token | Auto-populated |
| `refresh_token` | JWT refresh token | Auto-populated |
| `user_id` | Current user ID | Auto-populated |
| `test_email` | Test user email | `test@example.com` |
| `test_password` | Test user password | `password123` |
| `test_username` | Test username | `testuser` |

## Test Automation Features

### Automatic Token Management
- Login/Register requests automatically store tokens
- Subsequent requests use stored tokens
- Logout clears stored tokens

### Dynamic Data Generation
- Uses Postman's `{{$randomEmail}}` for unique emails
- Generates random usernames and display names
- Prevents conflicts in repeated test runs

### Comprehensive Assertions
- Response time validation (< 1000ms)
- Status code verification
- Response structure validation
- Required field presence checks
- Content-Type header verification

## Best Practices Implemented

### Collection Organization
- Logical grouping of related endpoints
- Separate folder for error scenarios
- Clear naming conventions

### Test Coverage
- Happy path scenarios
- Error handling validation
- Edge case testing
- Authentication state management

### CI/CD Readiness
- Environment-agnostic configuration
- Automated test execution
- Result reporting and artifacts
- PR comment integration

### Security Considerations
- Sensitive data marked as secret
- Token auto-cleanup after logout
- No hardcoded credentials in collection

## GitHub Actions Integration

The workflow automatically:
1. Starts test database and Redis instances
2. Builds and runs the Go server
3. Executes the Postman collection
4. Generates HTML and JSON reports
5. Comments test results on PRs
6. Uploads artifacts for detailed analysis

## Extending the Collection

To add new endpoints:
1. Create new requests in appropriate folders
2. Add test scripts for validation
3. Update environment variables if needed
4. Maintain consistent naming and organization

## Troubleshooting

### Common Issues
- **Server not ready**: Increase wait time in GitHub Actions
- **Token expiration**: Ensure proper token refresh flow
- **Database connection**: Verify connection strings and migrations
- **Port conflicts**: Check if ports 8080, 5432, 6379 are available

### Local Development
```bash
# Start dependencies
docker-compose up postgres redis

# Run server
go run cmd/server/main.go

# Test collection
newman run postman/athena-auth.postman_collection.json \
  --environment postman/athena-auth.postman_environment.json
```