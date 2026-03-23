# E2E Test Fixtures

This directory contains test data files used by E2E tests.

## Video Files

Test video files should be placed here for upload testing. You can generate test videos using ffmpeg:

```bash
# Generate a 10-second test video (H.264, 1280x720, 30fps)
ffmpeg -f lavfi -i testsrc=duration=10:size=1280x720:rate=30 \
  -vf "drawtext=text='Test Video':fontsize=48:fontcolor=white:x=(w-text_w)/2:y=(h-text_h)/2" \
  -c:v libx264 -preset fast -pix_fmt yuv420p \
  fixtures/test_video_720p.mp4

# Generate a shorter 5-second test video (smaller file size)
ffmpeg -f lavfi -i testsrc=duration=5:size=640x480:rate=24 \
  -vf "drawtext=text='Short Test':fontsize=32:fontcolor=white:x=(w-text_w)/2:y=(h-text_h)/2" \
  -c:v libx264 -preset ultrafast -pix_fmt yuv420p \
  fixtures/test_video_480p.mp4

# Generate a test video with audio
ffmpeg -f lavfi -i testsrc=duration=5:size=1280x720:rate=30 \
  -f lavfi -i sine=frequency=1000:duration=5 \
  -c:v libx264 -c:a aac -b:a 128k \
  fixtures/test_video_with_audio.mp4
```

## Available Fixtures

- `test_video_720p.mp4` - 10-second 720p test video (not included, generate with command above)
- `test_video_480p.mp4` - 5-second 480p test video (smaller file size)
- `test_video_with_audio.mp4` - 5-second video with audio track

## Malware Test Files

For ClamAV virus scanning tests, you can use the EICAR test file (safe to use, detected as malware by all scanners):

```bash
# Create EICAR test file (safe malware test file)
echo 'X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*' > fixtures/eicar.com
```

**WARNING**: Do NOT place real malware in this directory.

## Image Fixtures

Thumbnail and image upload tests:

```bash
# Generate a test thumbnail
convert -size 1280x720 xc:blue \
  -pointsize 72 -fill white -gravity center \
  -annotate +0+0 'Test Thumbnail' \
  fixtures/test_thumbnail.jpg
```

## JSON Test Data

The `data/` subdirectory contains JSON files with test data:

- `users.json` - Sample user profiles
- `videos.json` - Sample video metadata
- `comments.json` - Sample comments

## Usage in Tests

```go
// Load a test video
videoPath := filepath.Join("fixtures", "test_video_480p.mp4")
videoID := client.UploadVideo(t, videoPath, "Test Video", "Description")
```

## Gitignore

Large binary files (videos, images) should be gitignored and generated locally or in CI.
Add to `.gitignore`:

```
tests/e2e/fixtures/*.mp4
tests/e2e/fixtures/*.avi
tests/e2e/fixtures/*.mov
tests/e2e/fixtures/*.jpg
tests/e2e/fixtures/*.png
```
