// Steady-state load: a realistic read-heavy mix against the database.
// Use this to find the latency profile of a given deployment shape.
//
//   BASE_URL=http://<ip> k6 run loadtest/k6/load.js
//   BASE_URL=http://<ip> RATE=200 DURATION=5m k6 run loadtest/k6/load.js
import http from 'k6/http';
import { check } from 'k6';
import { Trend } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const RATE = Number(__ENV.RATE || 50); // requests per second
const DURATION = __ENV.DURATION || '2m';
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

// Per-endpoint latency, so a slow write does not hide behind fast reads.
const listLatency = new Trend('latency_list', true);
const createLatency = new Trend('latency_create', true);

export const options = {
  scenarios: {
    // Arrival-rate (open model): k6 holds the request rate even if the service
    // slows down, which is what you want when measuring a server. A fixed VU
    // count would quietly reduce load as latency grows.
    steady: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: Math.max(10, Math.ceil(RATE / 5)),
      maxVUs: Math.max(50, RATE * 2),
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<300', 'p(99)<800'],
    latency_list: ['p(95)<250'],
    latency_create: ['p(95)<400'],
  },
};

export function setup() {
  // Seed a handful of rows so list/get are not measuring an empty table.
  const ids = [];
  for (let i = 0; i < 20; i++) {
    const res = http.post(
      `${BASE_URL}/api/v1/tasks`,
      JSON.stringify({ title: `seed ${i}` }),
      JSON_HEADERS,
    );
    if (res.status === 201) ids.push(res.json('data.id'));
  }

  return { ids };
}

export default function (data) {
  const roll = Math.random();

  if (roll < 0.6) {
    const res = http.get(`${BASE_URL}/api/v1/tasks?limit=20`, { tags: { endpoint: 'list' } });
    listLatency.add(res.timings.duration);
    check(res, { 'list is 200': (r) => r.status === 200 });

    return;
  }

  if (roll < 0.85 && data.ids.length > 0) {
    const id = data.ids[Math.floor(Math.random() * data.ids.length)];
    const res = http.get(`${BASE_URL}/api/v1/tasks/${id}`, {
      tags: { endpoint: 'get' },
      // A seeded row may have been deleted between runs; a 404 is not a failure.
      responseCallback: http.expectedStatuses(200, 404),
    });
    check(res, { 'get is 200 or 404': (r) => r.status === 200 || r.status === 404 });

    return;
  }

  const res = http.post(
    `${BASE_URL}/api/v1/tasks`,
    JSON.stringify({ title: `load ${__VU}-${__ITER}` }),
    { ...JSON_HEADERS, tags: { endpoint: 'create' } },
  );
  createLatency.add(res.timings.duration);
  check(res, { 'create is 201': (r) => r.status === 201 });
}
