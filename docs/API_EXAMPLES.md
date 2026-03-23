# Vidra Core API Examples

This document provides comprehensive examples for working with the Vidra Core API, with a focus on the video categories system.

## Table of Contents

- [Authentication](#authentication)
- [Video Categories](#video-categories)
- [Videos with Categories](#videos-with-categories)
- [Admin Operations](#admin-operations)

## Authentication

All admin endpoints require authentication. First, obtain a JWT token:

```bash
# Login
curl -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "admin@example.com",
    "password": "yourpassword"
  }'

# Response
{
  "access_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "expires_in": 900,
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "admin@example.com",
    "role": "admin"
  }
}
```

Use the `access_token` in subsequent requests:

```bash
export TOKEN="eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."
```

## Video Categories

### List All Categories

Get all active video categories sorted by display order:

```bash
curl -X GET "http://localhost:8080/api/v1/categories"

# Response
[
  {
    "id": "a7808f7e-6762-4c9a-a42a-923d8a7fc770",
    "name": "Music",
    "slug": "music",
    "description": "Music videos, concerts, and audio content",
    "icon": "🎵",
    "color": "#FF0000",
    "display_order": 1,
    "is_active": true,
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z"
  },
  {
    "id": "b736675b-2444-4e60-a5c9-faac96dbc7a6",
    "name": "Gaming",
    "slug": "gaming",
    "description": "Gaming videos, walkthroughs, and streams",
    "icon": "🎮",
    "color": "#00FF00",
    "display_order": 2,
    "is_active": true,
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z"
  }
  // ... more categories
]
```

### List Categories with Filters

```bash
# Get only active categories, sorted by name
curl -X GET "http://localhost:8080/api/v1/categories?active_only=true&order_by=name&order_dir=asc"

# Paginate results
curl -X GET "http://localhost:8080/api/v1/categories?limit=10&offset=0"

# Sort by creation date (newest first)
curl -X GET "http://localhost:8080/api/v1/categories?order_by=created_at&order_dir=desc"
```

### Get Category by ID

```bash
curl -X GET "http://localhost:8080/api/v1/categories/a7808f7e-6762-4c9a-a42a-923d8a7fc770"

# Response
{
  "id": "a7808f7e-6762-4c9a-a42a-923d8a7fc770",
  "name": "Music",
  "slug": "music",
  "description": "Music videos, concerts, and audio content",
  "icon": "🎵",
  "color": "#FF0000",
  "display_order": 1,
  "is_active": true,
  "created_at": "2024-01-01T00:00:00Z",
  "updated_at": "2024-01-01T00:00:00Z"
}
```

### Get Category by Slug

```bash
curl -X GET "http://localhost:8080/api/v1/categories/music"

# Returns the same response as getting by ID
```

## Videos with Categories

### Create Video with Category

```bash
curl -X POST "http://localhost:8080/api/v1/videos" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "My Awesome Music Video",
    "description": "A great music video",
    "privacy": "public",
    "category_id": "a7808f7e-6762-4c9a-a42a-923d8a7fc770",
    "tags": ["music", "rock", "2024"],
    "language": "en"
  }'

# Response
{
  "id": "123e4567-e89b-12d3-a456-426614174000",
  "title": "My Awesome Music Video",
  "description": "A great music video",
  "category_id": "a7808f7e-6762-4c9a-a42a-923d8a7fc770",
  "category": {
    "id": "a7808f7e-6762-4c9a-a42a-923d8a7fc770",
    "name": "Music",
    "slug": "music",
    "icon": "🎵",
    "color": "#FF0000"
  },
  "privacy": "public",
  "status": "uploading",
  "tags": ["music", "rock", "2024"],
  "language": "en",
  "created_at": "2024-01-15T10:00:00Z",
  "updated_at": "2024-01-15T10:00:00Z"
}
```

### Update Video Category

```bash
curl -X PUT "http://localhost:8080/api/v1/videos/123e4567-e89b-12d3-a456-426614174000" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "My Awesome Music Video",
    "category_id": "b736675b-2444-4e60-a5c9-faac96dbc7a6"
  }'
```

### Search Videos by Category

```bash
# Search for videos in the music category
curl -X GET "http://localhost:8080/api/v1/videos/search?category_id=a7808f7e-6762-4c9a-a42a-923d8a7fc770"

# List videos with their categories
curl -X GET "http://localhost:8080/api/v1/videos"

# Response includes category details
{
  "data": [
    {
      "id": "123e4567-e89b-12d3-a456-426614174000",
      "title": "My Video",
      "category_id": "a7808f7e-6762-4c9a-a42a-923d8a7fc770",
      "category": {
        "id": "a7808f7e-6762-4c9a-a42a-923d8a7fc770",
        "name": "Music",
        "slug": "music"
      }
      // ... other video fields
    }
  ],
  "meta": {
    "total": 100,
    "limit": 20,
    "offset": 0
  }
}
```

## Admin Operations

### Create New Category (Admin Only)

```bash
curl -X POST "http://localhost:8080/api/v1/admin/categories" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Podcasts",
    "slug": "podcasts",
    "description": "Audio podcasts and talk shows",
    "icon": "🎙️",
    "color": "#9933FF",
    "display_order": 20,
    "is_active": true
  }'

# Response
{
  "id": "c8a7d3f2-9b5e-4a1c-8f3d-2e7a9b5c4d1a",
  "name": "Podcasts",
  "slug": "podcasts",
  "description": "Audio podcasts and talk shows",
  "icon": "🎙️",
  "color": "#9933FF",
  "display_order": 20,
  "is_active": true,
  "created_at": "2024-01-15T10:30:00Z",
  "updated_at": "2024-01-15T10:30:00Z",
  "created_by": "550e8400-e29b-41d4-a716-446655440000"
}
```

### Update Category (Admin Only)

```bash
curl -X PUT "http://localhost:8080/api/v1/admin/categories/c8a7d3f2-9b5e-4a1c-8f3d-2e7a9b5c4d1a" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Podcasts & Talk Shows",
    "description": "Audio podcasts, interviews, and talk shows",
    "icon": "🎧",
    "color": "#6600CC",
    "display_order": 21
  }'

# Response
{
  "id": "c8a7d3f2-9b5e-4a1c-8f3d-2e7a9b5c4d1a",
  "name": "Podcasts & Talk Shows",
  "slug": "podcasts",
  "description": "Audio podcasts, interviews, and talk shows",
  "icon": "🎧",
  "color": "#6600CC",
  "display_order": 21,
  "is_active": true,
  "created_at": "2024-01-15T10:30:00Z",
  "updated_at": "2024-01-15T10:35:00Z",
  "created_by": "550e8400-e29b-41d4-a716-446655440000"
}
```

### Deactivate Category (Admin Only)

```bash
# Deactivate a category (soft delete)
curl -X PUT "http://localhost:8080/api/v1/admin/categories/c8a7d3f2-9b5e-4a1c-8f3d-2e7a9b5c4d1a" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "is_active": false
  }'
```

### Delete Category (Admin Only)

```bash
# Permanently delete a category
# Note: Cannot delete the default "other" category
curl -X DELETE "http://localhost:8080/api/v1/admin/categories/c8a7d3f2-9b5e-4a1c-8f3d-2e7a9b5c4d1a" \
  -H "Authorization: Bearer $TOKEN"

# Response: 204 No Content (success)
```

## Error Responses

### Category Not Found

```json
{
  "error": "Category not found",
  "details": "video category not found"
}
```

### Unauthorized (No Token)

```json
{
  "error": "Unauthorized",
  "details": "Authentication required"
}
```

### Forbidden (Not Admin)

```json
{
  "error": "Forbidden",
  "details": "unauthorized: only admins can create categories"
}
```

### Validation Error

```json
{
  "error": "Invalid request data",
  "details": "validation failed: slug must match pattern ^[a-z0-9-]+$"
}
```

### Cannot Delete Default Category

```json
{
  "error": "Failed to delete category",
  "details": "cannot delete the default 'other' category"
}
```

## Integration Examples

### JavaScript/TypeScript

```javascript
// Fetch categories
async function getCategories() {
  const response = await fetch('http://localhost:8080/api/v1/categories');
  return await response.json();
}

// Create video with category
async function createVideo(token, videoData) {
  const response = await fetch('http://localhost:8080/api/v1/videos', {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${token}`,
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({
      title: videoData.title,
      description: videoData.description,
      category_id: videoData.categoryId,
      privacy: 'public',
      tags: videoData.tags
    })
  });
  return await response.json();
}

// Admin: Create new category
async function createCategory(token, categoryData) {
  const response = await fetch('http://localhost:8080/api/v1/admin/categories', {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${token}`,
      'Content-Type': 'application/json'
    },
    body: JSON.stringify(categoryData)
  });

  if (!response.ok) {
    throw new Error(`Failed to create category: ${response.statusText}`);
  }

  return await response.json();
}
```

### Python

```python
import requests

# Get all categories
def get_categories():
    response = requests.get('http://localhost:8080/api/v1/categories')
    return response.json()

# Create video with category
def create_video(token, video_data):
    headers = {
        'Authorization': f'Bearer {token}',
        'Content-Type': 'application/json'
    }

    data = {
        'title': video_data['title'],
        'description': video_data['description'],
        'category_id': video_data['category_id'],
        'privacy': 'public',
        'tags': video_data.get('tags', [])
    }

    response = requests.post(
        'http://localhost:8080/api/v1/videos',
        headers=headers,
        json=data
    )
    return response.json()

# Admin: Update category
def update_category(token, category_id, updates):
    headers = {
        'Authorization': f'Bearer {token}',
        'Content-Type': 'application/json'
    }

    response = requests.put(
        f'http://localhost:8080/api/v1/admin/categories/{category_id}',
        headers=headers,
        json=updates
    )

    if response.status_code != 200:
        raise Exception(f'Failed to update category: {response.text}')

    return response.json()
```

## Best Practices

1. **Always specify category_id when creating videos** - This helps with organization and discovery
2. **Use slugs for user-friendly URLs** - e.g., `/videos/category/music` instead of UUIDs
3. **Cache category list** - Categories don't change often, so cache them client-side
4. **Handle null category_id** - Some videos may not have a category assigned
5. **Check is_active flag** - Filter out inactive categories in user-facing interfaces
6. **Validate admin role** - Ensure only admins can modify categories
7. **Use appropriate display_order** - Keep categories organized for users

## Default Categories Reference

| Name | Slug | Icon | Display Order |
|------|------|------|---------------|
| Music | music | 🎵 | 1 |
| Gaming | gaming | 🎮 | 2 |
| Education | education | 📚 | 3 |
| Entertainment | entertainment | 🎭 | 4 |
| News & Politics | news-politics | 📰 | 5 |
| Science & Technology | science-technology | 🔬 | 6 |
| Sports | sports | ⚽ | 7 |
| Travel & Events | travel-events | ✈️ | 8 |
| Film & Animation | film-animation | 🎬 | 9 |
| People & Blogs | people-blogs | 👥 | 10 |
| Pets & Animals | pets-animals | 🐾 | 11 |
| How-to & Style | howto-style | 💄 | 12 |
| Autos & Vehicles | autos-vehicles | 🚗 | 13 |
| Nonprofits & Activism | nonprofits-activism | 🤝 | 14 |
| Other | other | 📁 | 999 |

The "Other" category (slug: `other`) is the default fallback category and cannot be deleted.
