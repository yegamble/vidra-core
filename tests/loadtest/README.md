# Load Testing for Vidra Core

This directory contains load testing scripts for the Vidra Core video platform using k6.

## Prerequisites

Install k6:

```bash
# macOS
brew install k6

# Ubuntu/Debian
sudo gpg -k
sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69
echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" | sudo tee /etc/apt/sources.list.d/k6.list
sudo apt-get update
sudo apt-get install k6

# Docker
docker pull grafana/k6
```

## Running Load Tests

### Basic Usage

```bash
# Run with default settings (local server)
k6 run k6-video-platform.js

# Run with custom base URL
k6 run -e BASE_URL=https://vidra.example.com k6-video-platform.js

# Run with custom user credentials
k6 run -e TEST_USERNAME=loadtest -e TEST_PASSWORD=secret k6-video-platform.js
```

### Test Scenarios

The load test includes multiple scenarios:

- **Video Listing** (40%): Browse video catalogs
- **Video Streaming** (20%): Stream video content
- **Search** (20%): Search for videos
- **Social Interactions** (10%): Likes, comments
- **API Endpoints** (10%): Health checks, user info

### Load Profiles

**Default Profile**:

- Ramp up: 2 min → 50 users
- Sustain: 5 min @ 50 users
- Ramp up: 2 min → 100 users
- Sustain: 5 min @ 100 users
- Spike: 2 min → 200 users
- Sustain spike: 5 min @ 200 users
- Ramp down: 5 min → 0 users

**Total Duration**: 25 minutes

### Custom Load Profiles

**Smoke Test** (quick validation):

```bash
k6 run --vus 1 --duration 1m k6-video-platform.js
```

**Stress Test** (find breaking point):

```bash
k6 run --vus 500 --duration 10m k6-video-platform.js
```

**Spike Test** (sudden load):

```bash
k6 run --stage 0s:0,10s:1000,1m:1000,10s:0 k6-video-platform.js
```

**Soak Test** (long duration):

```bash
k6 run --vus 100 --duration 4h k6-video-platform.js
```

## Performance Thresholds

**Configured Thresholds**:

- 95th percentile response time: < 2 seconds
- HTTP error rate: < 5%
- Custom error rate: < 10%

If any threshold is exceeded, k6 will exit with code 99.

## Results

### Console Output

k6 provides real-time statistics:

```
scenarios: (100.00%) 1 scenario, 200 max VUs, 25m30s max duration
default: [ 100% ] 200 VUs  25m0s

✓ list videos - status 200
✓ stream video - status 206
✗ search videos - status 200
  ↳  95% — ✓ 950 / ✗ 50

checks.........................: 98.00% ✓ 9800     ✗ 200
data_received..................: 150 MB 100 kB/s
data_sent......................: 5.0 MB 3.3 kB/s
http_req_blocked...............: avg=1.2ms    min=1µs      med=5µs
http_req_duration..............: avg=450ms    min=10ms     med=200ms    p(95)=1500ms
http_reqs......................: 10000  66.6/s
vus............................: 200    min=0      max=200
vus_max........................: 200    min=200    max=200
```

### Cloud Results (k6 Cloud)

Run with cloud integration:

```bash
k6 cloud k6-video-platform.js
```

View detailed metrics, graphs, and analysis at <https://app.k6.io>

### Grafana Integration

For local monitoring:

```bash
# Run k6 with InfluxDB output
k6 run --out influxdb=http://localhost:8086/k6 k6-video-platform.js

# View in Grafana (pre-configured dashboard)
open http://localhost:3000/d/k6/k6-load-testing-results
```

## Interpreting Results

### Key Metrics

| Metric | Good | Warning | Critical |
|--------|------|---------|----------|
| **http_req_duration (p95)** | < 1s | 1-2s | > 2s |
| **http_req_failed** | < 1% | 1-5% | > 5% |
| **checks** | > 99% | 95-99% | < 95% |
| **http_reqs** | Expected throughput | ±20% | Outside range |

### Common Issues

**High Response Times**:

- Database connection pool exhaustion
- Insufficient worker processes
- Network bottlenecks
- CPU saturation

**High Error Rates**:

- Rate limiting triggered
- Database connection failures
- Upstream service failures (Redis, IPFS, ClamAV)

**Memory Issues**:

- Memory leaks (check pod metrics)
- Insufficient memory limits
- Cache overflow

## Best Practices

1. **Run on Staging First**: Never run load tests on production
2. **Monitor Resources**: Watch CPU, memory, database connections
3. **Start Small**: Begin with smoke tests before full load
4. **Realistic Data**: Use production-like data volumes
5. **Clean Up**: Remove test users/videos after completion

## CI/CD Integration

### GitHub Actions

```yaml
name: Load Test

on:
  schedule:
    - cron: '0 2 * * 0' # Weekly on Sunday 2 AM

jobs:
  load-test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Run k6 load test
        uses: grafana/k6-action@v0.3.0
        with:
          filename: tests/loadtest/k6-video-platform.js
        env:
          BASE_URL: ${{ secrets.STAGING_URL }}
      - name: Upload results
        uses: actions/upload-artifact@v3
        with:
          name: k6-results
          path: summary.json
```

## Advanced Configuration

### Custom Metrics

Add custom metrics in test script:

```javascript
import { Trend } from 'k6/metrics';
const customMetric = new Trend('custom_metric_name');
customMetric.add(value);
```

### Scenarios

Define complex scenarios:

```javascript
export const options = {
  scenarios: {
    video_upload: {
      executor: 'constant-vus',
      vus: 10,
      duration: '5m',
      exec: 'testVideoUpload',
    },
    video_streaming: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '2m', target: 100 },
        { duration: '5m', target: 100 },
        { duration: '2m', target: 0 },
      ],
      exec: 'testVideoStreaming',
    },
  },
};
```

## Troubleshooting

**k6 Crashes**:

- Increase system limits: `ulimit -n 65535`
- Reduce VUs or duration
- Run distributed load test across multiple machines

**Network Errors**:

- Check firewall rules
- Verify DNS resolution
- Ensure server can handle connections

**Authentication Failures**:

- Verify credentials
- Check token expiration
- Ensure auth endpoints not rate-limited

## Resources

- [k6 Documentation](https://k6.io/docs/)
- [k6 Examples](https://k6.io/docs/examples/)
- [Grafana k6 Cloud](https://k6.io/cloud/)
- [Performance Testing Best Practices](https://k6.io/docs/testing-guides/)
