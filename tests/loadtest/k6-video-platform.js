import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

// Custom metrics
const errorRate = new Rate('errors');
const uploadDuration = new Trend('upload_duration');
const streamingDuration = new Trend('streaming_duration');

// Test configuration
export const options = {
  stages: [
    { duration: '2m', target: 50 },   // Ramp up to 50 users
    { duration: '5m', target: 50 },   // Stay at 50 users
    { duration: '2m', target: 100 },  // Ramp up to 100 users
    { duration: '5m', target: 100 },  // Stay at 100 users
    { duration: '2m', target: 200 },  // Spike to 200 users
    { duration: '5m', target: 200 },  // Stay at spike
    { duration: '5m', target: 0 },    // Ramp down
  ],
  thresholds: {
    http_req_duration: ['p(95)<2000'], // 95% of requests under 2s
    http_req_failed: ['rate<0.05'],     // Error rate under 5%
    errors: ['rate<0.1'],               // Custom error rate under 10%
  },
};

// Environment variables
const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const TEST_USERNAME = __ENV.TEST_USERNAME || `testuser_${__VU}_${Date.now()}`;
const TEST_PASSWORD = __ENV.TEST_PASSWORD || 'testpassword123';

// Auth token storage
let authToken = '';

// Setup: Create user and login
export function setup() {
  // Register user
  const registerPayload = JSON.stringify({
    username: TEST_USERNAME,
    email: `${TEST_USERNAME}@example.com`,
    password: TEST_PASSWORD,
  });

  const registerRes = http.post(`${BASE_URL}/auth/register`, registerPayload, {
    headers: { 'Content-Type': 'application/json' },
  });

  if (registerRes.status !== 201) {
    console.log('Registration failed, attempting login...');
  }

  // Login
  const loginPayload = JSON.stringify({
    username: TEST_USERNAME,
    password: TEST_PASSWORD,
  });

  const loginRes = http.post(`${BASE_URL}/auth/login`, loginPayload, {
    headers: { 'Content-Type': 'application/json' },
  });

  check(loginRes, {
    'login successful': (r) => r.status === 200,
  });

  const loginData = JSON.parse(loginRes.body);
  return { token: loginData.access_token };
}

// Main test scenario
export default function (data) {
  authToken = data.token;

  // Test scenario distribution
  const scenario = Math.random();

  if (scenario < 0.4) {
    testVideoList();
  } else if (scenario < 0.6) {
    testVideoStreaming();
  } else if (scenario < 0.8) {
    testVideoSearch();
  } else if (scenario < 0.9) {
    testSocialInteractions();
  } else {
    testAPIEndpoints();
  }

  sleep(1);
}

// Test: List videos (40% of requests)
function testVideoList() {
  const res = http.get(`${BASE_URL}/api/v1/videos`, {
    headers: {
      'Authorization': `Bearer ${authToken}`,
    },
  });

  const success = check(res, {
    'list videos - status 200': (r) => r.status === 200,
    'list videos - has results': (r) => {
      try {
        const body = JSON.parse(r.body);
        return Array.isArray(body.videos);
      } catch (e) {
        return false;
      }
    },
  });

  errorRate.add(!success);
}

// Test: Video streaming (20% of requests)
function testVideoStreaming() {
  // First get a video ID
  const listRes = http.get(`${BASE_URL}/api/v1/videos?limit=1`);

  if (listRes.status !== 200) {
    errorRate.add(1);
    return;
  }

  const videos = JSON.parse(listRes.body).videos;
  if (!videos || videos.length === 0) {
    return; // No videos to stream
  }

  const videoId = videos[0].id;

  // Stream video
  const startTime = Date.now();
  const streamRes = http.get(`${BASE_URL}/api/v1/videos/${videoId}/stream`, {
    headers: {
      'Authorization': `Bearer ${authToken}`,
      'Range': 'bytes=0-1023', // Request first 1KB
    },
  });

  const duration = Date.now() - startTime;
  streamingDuration.add(duration);

  const success = check(streamRes, {
    'stream video - status 206': (r) => r.status === 206,
    'stream video - has content': (r) => r.body.length > 0,
  });

  errorRate.add(!success);
}

// Test: Video search (20% of requests)
function testVideoSearch() {
  const searchTerms = ['test', 'demo', 'tutorial', 'review', 'gaming'];
  const query = searchTerms[Math.floor(Math.random() * searchTerms.length)];

  const res = http.get(`${BASE_URL}/api/v1/videos/search?q=${query}`, {
    headers: {
      'Authorization': `Bearer ${authToken}`,
    },
  });

  const success = check(res, {
    'search videos - status 200': (r) => r.status === 200,
    'search videos - valid response': (r) => {
      try {
        const body = JSON.parse(r.body);
        return Array.isArray(body.results) || Array.isArray(body.videos);
      } catch (e) {
        return false;
      }
    },
  });

  errorRate.add(!success);
}

// Test: Social interactions (10% of requests)
function testSocialInteractions() {
  // Get a random video
  const listRes = http.get(`${BASE_URL}/api/v1/videos?limit=1`);

  if (listRes.status !== 200) {
    errorRate.add(1);
    return;
  }

  const videos = JSON.parse(listRes.body).videos;
  if (!videos || videos.length === 0) {
    return;
  }

  const videoId = videos[0].id;

  // Like video
  const likePayload = JSON.stringify({ rating: 1 });
  const likeRes = http.put(`${BASE_URL}/api/v1/videos/${videoId}/rating`, likePayload, {
    headers: {
      'Authorization': `Bearer ${authToken}`,
      'Content-Type': 'application/json',
    },
  });

  // Get comments
  const commentsRes = http.get(`${BASE_URL}/api/v1/videos/${videoId}/comments`, {
    headers: {
      'Authorization': `Bearer ${authToken}`,
    },
  });

  const success = check(likeRes, {
    'like video - status 200 or 201': (r) => r.status === 200 || r.status === 201,
  }) && check(commentsRes, {
    'get comments - status 200': (r) => r.status === 200,
  });

  errorRate.add(!success);
}

// Test: Various API endpoints (10% of requests)
function testAPIEndpoints() {
  const batch = http.batch([
    ['GET', `${BASE_URL}/health`, null, { tags: { name: 'HealthCheck' } }],
    ['GET', `${BASE_URL}/api/v1/videos/qualities`, null, { headers: { 'Authorization': `Bearer ${authToken}` } }],
    ['GET', `${BASE_URL}/api/v1/users/me`, null, { headers: { 'Authorization': `Bearer ${authToken}` } }],
    ['GET', `${BASE_URL}/api/v1/channels`, null, { headers: { 'Authorization': `Bearer ${authToken}` } }],
  ]);

  const success = check(batch[0], { 'health check - status 200': (r) => r.status === 200 })
    && check(batch[1], { 'qualities - status 200': (r) => r.status === 200 })
    && check(batch[2], { 'get user - status 200': (r) => r.status === 200 })
    && check(batch[3], { 'get channels - status 200': (r) => r.status === 200 });

  errorRate.add(!success);
}

// Teardown
export function teardown(data) {
  console.log(`Load test completed for user: ${TEST_USERNAME}`);
}
