#!/usr/bin/env bun
// acceptance-meta-gate.mjs
// Refuses an acceptance run that could not have failed.
//
// A green Playwright exit code says the tests that ran passed. It does not
// say that any ran (a config that matches nothing, a skip-guard that fires),
// that the relay ever reached the fake vendor (a run that asserted only
// refusals), or that each scenario contains a check that can fail. Each of
// those has happened here before (2026-09: UAT e2e executed zero specs for
// five days while its job stayed green). This gate turns them into failures.
//
// Rules, over acceptance-report/results.json and the live fakes:
//   1. no top-level errors (a failed global setup);
//   2. at least MIN_TESTS tests, none skipped / unexpected / flaky;
//   3. every test names its case (TC-xx) and the run covers MIN_CASES cases;
//   4. every test ran a step titled "reverse control: …";
//   5. the fake vendor answered at least one relay call;
//   6. the fake platform saw no call it does not implement.
// It also prints the identity of what was tested (acceptance-report/target.json).
//
// Run:  bun scripts/acceptance-meta-gate.mjs <results.json> <fake-url>
// Self-test: bun scripts/acceptance-meta-gate.test.mjs

import { existsSync, readFileSync } from 'fs';
import { dirname, join } from 'path';

// Raise these with the suite; never lower them to make a run pass.
export const MIN_TESTS = 12;
export const MIN_CASES = 11;

const CASE_ID = /\bTC-[A-Z]+\d+\b/;
const REVERSE = /^reverse control: /;

function collectTests(suite, out = []) {
  for (const spec of suite.specs ?? []) {
    for (const t of spec.tests ?? [])
      out.push({ title: spec.title, file: suite.file, ...t });
  }
  for (const s of suite.suites ?? []) collectTests(s, out);
  return out;
}

function stepTitles(steps, out = []) {
  for (const s of steps ?? []) {
    out.push(s.title);
    stepTitles(s.steps, out);
  }
  return out;
}

/**
 * Pure check. `fake` = { requests: number, unhandled: string[] } or null when
 * the fakes could not be read. Returns a list of failure messages.
 */
export function evaluate(results, fake) {
  const fail = [];
  if (!results || typeof results !== 'object')
    return ['results.json is not a Playwright JSON report'];

  for (const e of results.errors ?? []) {
    fail.push(`run error: ${(e.message ?? JSON.stringify(e)).split('\n')[0]}`);
  }

  const tests = (results.suites ?? []).flatMap((s) => collectTests(s));
  if (tests.length < MIN_TESTS) {
    fail.push(`${tests.length} tests ran, the floor is ${MIN_TESTS}`);
  }
  const cases = new Set();
  for (const t of tests) {
    const where = `${t.file ?? '?'} › ${t.title}`;
    if (t.status !== 'expected') fail.push(`${where}: ${t.status}`);
    const id = t.title.match(CASE_ID)?.[0];
    if (!id) fail.push(`${where}: title names no case (TC-xx)`);
    else cases.add(id);
    const last = (t.results ?? []).at(-1);
    if (!stepTitles(last?.steps).some((s) => REVERSE.test(s))) {
      fail.push(`${where}: no "reverse control: …" step ran`);
    }
  }
  if (cases.size < MIN_CASES) {
    fail.push(
      `${cases.size} cases covered (${[...cases].sort().join(', ') || 'none'}), the floor is ${MIN_CASES}`,
    );
  }

  if (!fake) {
    fail.push(
      'the fakes could not be read: nothing proves the relay reached them',
    );
  } else {
    if (!(fake.requests > 0))
      fail.push('the fake vendor answered no relay call');
    for (const u of fake.unhandled ?? [])
      fail.push(`fake platform does not implement: ${u}`);
  }
  return fail;
}

async function readFake(url) {
  try {
    const [r, u] = await Promise.all([
      fetch(`${url}/_fake/requests`).then((x) => x.json()),
      fetch(`${url}/_fake/platform/unhandled`).then((x) => x.json()),
    ]);
    return {
      requests: (r.requests ?? []).length,
      unhandled: u.unhandled ?? [],
    };
  } catch {
    return null;
  }
}

async function main() {
  const [resultsPath, fakeURL] = process.argv.slice(2);
  if (!resultsPath || !fakeURL) {
    process.stderr.write(
      'usage: acceptance-meta-gate.mjs <results.json> <fake-url>\n',
    );
    process.exit(2);
  }
  const target = join(dirname(resultsPath), 'target.json');
  if (existsSync(target))
    process.stdout.write(
      `[meta-gate] under test: ${readFileSync(target, 'utf8').trim()}\n`,
    );
  else
    process.stdout.write('[meta-gate] under test: UNKNOWN (no target.json)\n');

  let results = null;
  if (existsSync(resultsPath)) {
    try {
      results = JSON.parse(readFileSync(resultsPath, 'utf8'));
    } catch {
      results = null;
    }
  }
  const fake = await readFake(fakeURL);
  const failures = results
    ? evaluate(results, fake)
    : [`${resultsPath} missing or unreadable — the suite did not run`];
  const s = results?.stats ?? {};
  process.stdout.write(
    `[meta-gate] expected=${s.expected ?? 0} unexpected=${s.unexpected ?? 0} skipped=${s.skipped ?? 0} flaky=${s.flaky ?? 0} vendor_calls=${fake?.requests ?? '?'}\n`,
  );
  if (failures.length) {
    for (const f of failures) process.stderr.write(`[meta-gate] FAIL: ${f}\n`);
    process.exit(1);
  }
  process.stdout.write('[meta-gate] ok\n');
}

if (import.meta.main ?? process.argv[1]?.endsWith('acceptance-meta-gate.mjs')) {
  await main();
}
