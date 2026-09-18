// Stress / autoscaling test: ramps CPU-heavy requests until something gives.
// Point it at the burn endpoint (ENABLE_BURN_ENDPOINT=true) so the pods are
// actually CPU-bound and the HPA has a reason to react.
//
//   BASE_URL=http://<ip> k6 run loadtest/k6/stress.js
//   BASE_URL=http://<ip> BURN_MS=100 PEAK_RATE=300 k6 run loadtest/k6/stress.js
//
// Watch it scale in another terminal:
//   kubectl -n experiment-go get hpa,pods -w
import http from 'k6/http';
import { check } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const BURN_MS = Number(__ENV.BURN_MS || 50);
const PEAK_RATE = Number(__ENV.PEAK_RATE || 150);

export const options = {
  scenarios: {
    ramp: {
      executor: 'ramping-arrival-rate',
      startRate: 10,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 500,
      stages: [
        { target: 10, duration: '1m' }, // baseline
        { target: Math.round(PEAK_RATE / 3), duration: '2m' }, // first step
        { target: Math.round((PEAK_RATE * 2) / 3), duration: '2m' }, // second step
        { target: PEAK_RATE, duration: '3m' }, // peak — HPA should have scaled by now
        { target: 10, duration: '2m' }, // recovery: watch scale-down lag
      ],
    },
  },
  thresholds: {
    // Deliberately loose: the point of a stress test is to find the ceiling,
    // not to pass. Errors above 5% mean you found it.
    http_req_failed: ['rate<0.05'],
    http_req_duration: ['p(95)<2000'],
  },
};

export default function () {
  const res = http.get(`${BASE_URL}/api/v1/burn?ms=${BURN_MS}`, {
    tags: { endpoint: 'burn' },
    timeout: '10s',
  });

  check(res, {
    'burn is 200': (r) => r.status === 200,
    'burn endpoint is enabled': (r) => r.status !== 404,
  });
}
