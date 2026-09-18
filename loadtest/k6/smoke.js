// Smoke test: does the deployment work at all? Run this before any load test.
//
//   BASE_URL=http://localhost:8080 k6 run loadtest/k6/smoke.js
import http from 'k6/http';
import { check, group } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } };

export const options = {
  vus: 1,
  iterations: 5,
  thresholds: {
    // A smoke test that fails any check is a failed deploy.
    checks: ['rate==1.0'],
    http_req_failed: ['rate==0.0'],
  },
};

export default function () {
  group('health', () => {
    const live = http.get(`${BASE_URL}/healthz`);
    check(live, {
      'healthz is 200': (r) => r.status === 200,
      'healthz reports ok': (r) => r.json('status') === 'ok',
    });

    const ready = http.get(`${BASE_URL}/readyz`);
    check(ready, {
      'readyz is 200 (database reachable)': (r) => r.status === 200,
    });
  });

  group('task lifecycle', () => {
    const created = http.post(
      `${BASE_URL}/api/v1/tasks`,
      JSON.stringify({ title: `smoke ${__ITER}` }),
      JSON_HEADERS,
    );
    check(created, {
      'create is 201': (r) => r.status === 201,
      'create returns an id': (r) => r.json('data.id') > 0,
      'create sets Location': (r) => !!r.headers['Location'],
    });

    const id = created.json('data.id');
    if (!id) return;

    check(http.get(`${BASE_URL}/api/v1/tasks/${id}`), {
      'get is 200': (r) => r.status === 200,
    });

    check(
      http.put(
        `${BASE_URL}/api/v1/tasks/${id}`,
        JSON.stringify({ title: `smoke ${__ITER} done`, done: true }),
        JSON_HEADERS,
      ),
      {
        'update is 200': (r) => r.status === 200,
        'update persists done': (r) => r.json('data.done') === true,
      },
    );

    check(http.get(`${BASE_URL}/api/v1/tasks?limit=5`), {
      'list is 200': (r) => r.status === 200,
      'list returns an array': (r) => Array.isArray(r.json('data')),
    });

    check(http.del(`${BASE_URL}/api/v1/tasks/${id}`), {
      'delete is 204': (r) => r.status === 204,
    });

    // A 404 is the expected answer here, so tell k6 not to count it as a failed
    // request — otherwise http_req_failed reports 1/8 requests "broken".
    check(
      http.get(`${BASE_URL}/api/v1/tasks/${id}`, {
        responseCallback: http.expectedStatuses(404),
      }),
      {
        'deleted task is 404': (r) => r.status === 404,
      },
    );
  });
}
