import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  stages: [
    { duration: '30s', target: 20 }, // Ramp up to 20 users
    { duration: '1m', target: 20 },  // Stay at 20 users
    { duration: '30s', target: 0 },  // Ramp down
  ],
  thresholds: {
    http_req_duration: ['p(95)<500'], // 95% of requests must complete below 500ms
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export default function () {
  // 1. Health Check
  const healthRes = http.get(`${BASE_URL}/health`);
  check(healthRes, {
    'health check status is 200': (r) => r.status === 200,
  });

  // 2. Browse Videos (Public)
  const videosRes = http.get(`${BASE_URL}/api/v1/videos?count=10&sort=-createdAt`);
  check(videosRes, {
    'videos list status is 200': (r) => r.status === 200,
    'videos list duration < 500ms': (r) => r.timings.duration < 500,
  });

  // 3. Simulate "viewing" a video (if list returned videos)
  if (videosRes.status === 200) {
    try {
        const body = JSON.parse(videosRes.body);
        if (body.data && body.data.length > 0) {
            const videoId = body.data[0].id;
            const videoRes = http.get(`${BASE_URL}/api/v1/videos/${videoId}`);
            check(videoRes, {
                'video detail status is 200': (r) => r.status === 200,
            });

            // Simulate HLS manifest fetch
            const hlsRes = http.get(`${BASE_URL}/videos/${videoId}/master.m3u8`);
             check(hlsRes, {
                'HLS manifest status is 200 or 404 (if not ready)': (r) => r.status === 200 || r.status === 404,
            });
        }
    } catch (e) {
        // console.error("Failed to parse video list", e);
    }
  }

  sleep(1);
}
