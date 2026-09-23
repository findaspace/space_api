import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter } from 'k6/metrics';

// The interesting question is not throughput. It is whether the exclusion
// constraint still admits exactly one winner when many clients race for the
// same nights over real HTTP, through the pool, under load.
const wins = new Counter('booking_wins');
const conflicts = new Counter('booking_conflicts');
const errors = new Counter('booking_errors');

const API = __ENV.API || 'http://localhost:8080';
const TOKEN = __ENV.TOKEN;
const SPACE = __ENV.SPACE;

export const options = {
  scenarios: {
    // Read-heavy, which is what a marketplace actually serves.
    browse: {
      executor: 'constant-vus',
      vus: 30,
      duration: '60s',
      exec: 'browse',
    },
    // Fifty clients, one set of dates, all at once.
    contend: {
      executor: 'shared-iterations',
      vus: 50,
      iterations: 50,
      startTime: '20s',
      exec: 'contend',
    },
  },
  thresholds: {
    'http_req_duration{scenario:browse}': ['p(95)<500'],
    'http_req_failed{scenario:browse}': ['rate<0.01'],
    booking_wins: ['count==1'],
    booking_errors: ['count==0'],
  },
};

export function browse() {
  const res = http.get(
    `${API}/v1/search?lat=5.6363&lng=-0.1580&radius_m=10000&sort=relevance&limit=20`);

  check(res, {
    'search ok': (r) => r.status === 200,
    'search returns results': (r) => (r.json('results') || []).length > 0,
  });
  sleep(Math.random() * 2);
}

export function contend() {
  const res = http.post(`${API}/v1/bookings`, JSON.stringify({
    space_id: SPACE,
    starts_on: '2029-05-01',
    months: 12,
    guests: 2,
  }), {
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${TOKEN}`,
      // Distinct per iteration, so idempotency cannot mask a double booking.
      'Idempotency-Key': `load-${__VU}-${__ITER}`,
    },
  });

  if (res.status === 201) wins.add(1);
  else if (res.status === 409) conflicts.add(1);
  else errors.add(1);
}
