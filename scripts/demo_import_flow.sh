#!/bin/bash

# demo_import_flow.sh - Simple demonstration of the video import flow
# This is a simplified version showing the key concepts

set -e

echo "========================================"
echo "Vidra Core Video Import System - Demo"
echo "========================================"
echo ""

# Configuration
API_URL="${API_URL:-http://localhost:8080}"
TOKEN="${JWT_TOKEN:-demo-token}"

echo "📹 Video Import Flow Demonstration"
echo ""
echo "This script demonstrates how to:"
echo "  1. Import a video from YouTube/Vimeo/etc"
echo "  2. Check import status and progress"
echo "  3. List all your imports"
echo "  4. Cancel an import if needed"
echo ""

# Step 1: Create Import
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "📤 Step 1: Creating Video Import"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "POST /api/v1/videos/imports"
echo "Request:"
cat << 'EOF'
{
  "source_url": "https://youtube.com/watch?v=example",
  "target_privacy": "private",
  "target_category": "Entertainment"
}
EOF
echo ""
echo "Response:"
cat << 'EOF'
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "source_url": "https://youtube.com/watch?v=example",
  "status": "pending",
  "progress": 0,
  "target_privacy": "private",
  "source_platform": "youtube",
  "created_at": "2025-01-12T10:00:00Z"
}
EOF
echo ""
read -p "Press Enter to continue..."
echo ""

# Step 2: Check Status
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔍 Step 2: Checking Import Status"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "GET /api/v1/videos/imports/{id}"
echo ""
echo "Response (downloading):"
cat << 'EOF'
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "downloading",
  "progress": 45,
  "downloaded_bytes": 50000000,
  "metadata": {
    "title": "Example Video",
    "description": "An example video",
    "duration": 300,
    "uploader": "Example Channel"
  }
}
EOF
echo ""
read -p "Press Enter to continue..."
echo ""

# Step 3: Monitor Progress
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "⏳ Step 3: Monitoring Progress"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "The import goes through these states:"
echo ""
echo "  pending → downloading → processing → completed"
echo "       ↓         ↓            ↓"
echo "       └─────── failed / cancelled"
echo ""
echo "Progress updates:"
echo "  1s:  Status=downloading, Progress=10%"
echo "  5s:  Status=downloading, Progress=45%"
echo "  12s: Status=processing,  Progress=75%"
echo "  18s: Status=completed,   Progress=100%"
echo ""
read -p "Press Enter to continue..."
echo ""

# Step 4: Completed Import
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ Step 4: Import Completed"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Final status:"
cat << 'EOF'
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "completed",
  "progress": 100,
  "video_id": "99a87b56-c43d-42e8-a925-789012345678",
  "completed_at": "2025-01-12T10:00:18Z"
}
EOF
echo ""
echo "✨ Video is now ready for encoding and playback!"
echo ""
read -p "Press Enter to continue..."
echo ""

# Step 5: List Imports
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "📋 Step 5: Listing Your Imports"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "GET /api/v1/videos/imports?limit=20&offset=0"
echo ""
echo "Response:"
cat << 'EOF'
{
  "imports": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "status": "completed",
      "source_platform": "youtube",
      "created_at": "2025-01-12T10:00:00Z"
    },
    {
      "id": "661f9511-f3ac-52e5-b827-557766551111",
      "status": "downloading",
      "progress": 30,
      "source_platform": "vimeo",
      "created_at": "2025-01-12T09:45:00Z"
    }
  ],
  "total_count": 2,
  "limit": 20,
  "offset": 0
}
EOF
echo ""
read -p "Press Enter to continue..."
echo ""

# Step 6: Cancel Import
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "❌ Step 6: Cancelling an Import (Optional)"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "DELETE /api/v1/videos/imports/{id}"
echo ""
echo "Response: 204 No Content"
echo ""
echo "Note: Can only cancel imports in 'pending', 'downloading', or 'processing' states"
echo ""
read -p "Press Enter to continue..."
echo ""

# Quota Information
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "⚠️  Quota & Rate Limits"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Daily Quota:"
echo "  - 100 imports per day per user"
echo ""
echo "Concurrent Limit:"
echo "  - Max 5 imports running at the same time"
echo "  - Counts 'downloading' + 'processing' states"
echo ""
echo "Timeout:"
echo "  - Imports stuck for 2+ hours are automatically marked as failed"
echo ""
echo "Cleanup:"
echo "  - Old completed/failed imports are cleaned up after 30 days"
echo ""
read -p "Press Enter to continue..."
echo ""

# Error Handling
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🚨 Error Handling"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Common errors:"
echo ""
echo "1. Quota Exceeded (429):"
cat << 'EOF'
   {
     "error": "Too Many Requests",
     "message": "daily import quota exceeded (max 100 per day)"
   }
EOF
echo ""
echo "2. Rate Limited (429):"
cat << 'EOF'
   {
     "error": "Too Many Requests",
     "message": "too many concurrent imports (max 5)"
   }
EOF
echo ""
echo "3. Unsupported URL (400):"
cat << 'EOF'
   {
     "error": "Bad Request",
     "message": "unsupported URL or platform"
   }
EOF
echo ""
echo "4. Import Failed:"
cat << 'EOF'
   {
     "id": "550e8400-...",
     "status": "failed",
     "error_message": "download failed: video not available"
   }
EOF
echo ""
read -p "Press Enter to continue..."
echo ""

# Supported Platforms
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🌐 Supported Platforms (via yt-dlp)"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "Major platforms:"
echo "  ✓ YouTube"
echo "  ✓ Vimeo"
echo "  ✓ Dailymotion"
echo "  ✓ Twitch"
echo "  ✓ Twitter/X"
echo "  ✓ Facebook"
echo "  ✓ Instagram"
echo "  ✓ TikTok"
echo "  ✓ 1000+ more platforms"
echo ""
echo "For full list: https://github.com/yt-dlp/yt-dlp/blob/master/supportedsites.md"
echo ""
read -p "Press Enter to finish..."
echo ""

# Summary
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✨ Demo Complete!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "To test with a real server:"
echo ""
echo "  export JWT_TOKEN=\"your-actual-token\""
echo "  export API_URL=\"http://localhost:8080\""
echo "  ./scripts/test_import_api.sh"
echo ""
echo "Documentation:"
echo "  - Sprint docs: docs/sprints/README.md"
echo "  - Import implementation: docs/sprints/SPRINT1_COMPLETE.md"
echo "  - Feature summary: docs/sprints/IMPLEMENTATION_SUMMARY.md"
echo ""
echo "Happy importing! 🎬"
echo ""
