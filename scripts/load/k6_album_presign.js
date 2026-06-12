import http from 'k6/http';
import { check, sleep } from 'k6';

// Local compose load scenario: album list + media presign.
// Requires a valid access token — export before running:
//   export K6_TOKEN=<access_token>
//   k6 run scripts/load/k6_album_presign.js

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const TOKEN = __ENV.K6_TOKEN || '';

export const options = {
  scenarios: {
    album_presign: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: '30s', target: 100 },
        { duration: '1m', target: 500 },
        { duration: '30s', target: 0 },
      ],
      gracefulRampDown: '10s',
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.05'],
    http_req_duration: ['p(95)<2000'],
  },
};

const headers = TOKEN
  ? { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' }
  : { 'Content-Type': 'application/json' };

export default function () {
  if (!TOKEN) {
    check(null, { 'K6_TOKEN set': () => false });
    return;
  }

  const list = http.get(`${BASE}/v1/albums?state=active`, { headers });
  check(list, { 'albums 200': (r) => r.status === 200 });

  const presign = http.post(
    `${BASE}/v1/media/presign`,
    JSON.stringify({
      kind: 'photo',
      content_type: 'image/jpeg',
      byte_size: 120000,
    }),
    { headers }
  );
  check(presign, { 'presign 200': (r) => r.status === 200 });

  sleep(0.2);
}
