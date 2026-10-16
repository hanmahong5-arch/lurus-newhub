#!/usr/bin/env bun
// acceptance-meta-gate.test.mjs
// Self-test for acceptance-meta-gate.mjs. A gate that cannot fail is worse
// than none, so each rule gets the run that must trip it, next to a healthy
// run that must not.
//
// Run: bun scripts/acceptance-meta-gate.test.mjs

import { evaluate, MIN_CASES, MIN_TESTS } from './acceptance-meta-gate.mjs';

let failures = 0;
function check(name, fn) {
  try {
    fn();
    process.stdout.write(`ok   ${name}\n`);
  } catch (err) {
    failures += 1;
    process.stdout.write(`FAIL ${name}: ${err.message}\n`);
  }
}
function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}

const reverse = [
  { title: 'step', steps: [{ title: 'reverse control: must fail' }] },
];
function test(title, over = {}) {
  return {
    title,
    tests: [
      {
        status: 'expected',
        results: [{ status: 'passed', steps: reverse }],
        ...over,
      },
    ],
  };
}
function run(specs, extra = {}) {
  return {
    suites: [{ file: 'a.spec.ts', specs, suites: [] }],
    errors: [],
    ...extra,
  };
}
const ids = [
  'TC-M1',
  'TC-M1',
  'TC-M2',
  'TC-M4',
  'TC-C3',
  'TC-G7',
  'TC-P1',
  'TC-E1',
  'TC-E2',
  'TC-E3',
  'TC-E4',
  'TC-E5',
];
const healthy = () => run(ids.map((id, i) => test(`${id} case ${i}`)));
const fake = { requests: 12, unhandled: [] };
const trips = (results, f, needle) => {
  const out = evaluate(results, f);
  assert(
    out.some((m) => m.includes(needle)),
    `expected a failure containing "${needle}", got ${JSON.stringify(out)}`,
  );
};

check('the floors match the suite this was written against', () => {
  assert(ids.length >= MIN_TESTS, 'healthy fixture is below MIN_TESTS');
  assert(new Set(ids).size >= MIN_CASES, 'healthy fixture is below MIN_CASES');
});
check('a healthy run passes', () => {
  const out = evaluate(healthy(), fake);
  assert(out.length === 0, JSON.stringify(out));
});
check('an empty run fails', () => trips(run([]), fake, 'tests ran'));
check('a skipped test fails', () => {
  const r = healthy();
  r.suites[0].specs[0].tests[0].status = 'skipped';
  trips(r, fake, 'skipped');
});
check('a flaky test fails (no retries hide a race)', () => {
  const r = healthy();
  r.suites[0].specs[1].tests[0].status = 'flaky';
  trips(r, fake, 'flaky');
});
check('a test without a case id fails', () => {
  const r = healthy();
  r.suites[0].specs[2].title = 'charges correctly';
  trips(r, fake, 'names no case');
});
check('too few distinct cases fail', () =>
  trips(
    run(Array.from({ length: 8 }, (_, i) => test(`TC-M1 variant ${i}`))),
    fake,
    'cases covered',
  ),
);
check('a test whose reverse control never ran fails', () => {
  const r = healthy();
  r.suites[0].specs[3].tests[0].results[0].steps = [
    { title: 'only the happy path' },
  ];
  trips(r, fake, 'reverse control');
});
check('a global-setup error fails', () =>
  trips(
    { ...healthy(), errors: [{ message: 'setup blew up' }] },
    fake,
    'run error',
  ),
);
check('a run that never reached the vendor fails', () =>
  trips(healthy(), { requests: 0, unhandled: [] }, 'no relay call'),
);
check('unreadable fakes fail', () =>
  trips(healthy(), null, 'could not be read'),
);
check('an unimplemented platform call fails', () =>
  trips(
    healthy(),
    { requests: 3, unhandled: ['GET /internal/v1/accounts/1/overview'] },
    'does not implement',
  ),
);

if (failures) {
  process.stdout.write(`\n${failures} failing\n`);
  process.exit(1);
}
process.stdout.write('\nall ok\n');
