#!/usr/bin/env node
// bundle-budget.test.mjs
// Self-test for check-bundle-budget.mjs, driven over synthetic dist trees.
// A budget gate that cannot fail is worse than no gate, so the cases here are
// the ones that decide whether it can: over budget, under budget, so far under
// that the budget has stopped holding anything, and the two shapes of a broken
// measurement (a dist with no JS, and no dist at all) that must fail loudly
// instead of reporting zero and passing.
//
// Run: bun scripts/bundle-budget.test.mjs
// or:  node web/scripts/bundle-budget.test.mjs

import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { measureBundle, checkBudget } from './check-bundle-budget.mjs';

let failures = 0;

function check(name, fn) {
  try {
    fn();
    process.stdout.write(`ok   ${name}\n`);
  } catch (err) {
    failures += 1;
    process.stdout.write(`FAIL ${name}\n     ${err.message}\n`);
  }
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function makeDist({ entryBytes = 100, preloadBytes = [], withIndex = true }) {
  const dir = mkdtempSync(join(tmpdir(), 'bundle-budget-'));
  const assets = join(dir, 'assets');
  mkdirSync(assets);
  writeFileSync(join(assets, 'entry-aaaa.js'), 'x'.repeat(entryBytes));
  const preloads = preloadBytes.map((size, i) => {
    const name = `preload-${i}-bbbb.js`;
    writeFileSync(join(assets, name), 'y'.repeat(size));
    return name;
  });
  if (withIndex) {
    const links = preloads
      .map(
        (name) =>
          `<link rel="modulepreload" crossorigin href="/assets/${name}">`,
      )
      .join('\n');
    writeFileSync(
      join(dir, 'index.html'),
      `<!doctype html><html><head>\n<script type="module" crossorigin src="/assets/entry-aaaa.js"></script>\n${links}\n</head><body></body></html>`,
    );
  }
  return dir;
}

const budgetOf = (metrics, factor) => ({
  entryChunkBytes: Math.round(metrics.entryChunkBytes * factor),
  firstPaintJsBytes: Math.round(metrics.firstPaintJsBytes * factor),
  largestChunkBytes: Math.round(metrics.largestChunkBytes * factor),
  totalJsBytes: Math.round(metrics.totalJsBytes * factor),
  jsChunkCount: Math.round(metrics.jsChunkCount * factor),
});

const created = [];
function dist(options) {
  const dir = makeDist(options);
  created.push(dir);
  return dir;
}

check('measures the entry, its modulepreloads and the whole build', () => {
  const dir = dist({ entryBytes: 1000, preloadBytes: [200, 300] });
  const m = measureBundle(dir);
  assert(m.entryChunkBytes === 1000, `entryChunkBytes=${m.entryChunkBytes}`);
  assert(
    m.firstPaintJsBytes === 1500,
    `firstPaintJsBytes=${m.firstPaintJsBytes}`,
  );
  assert(
    m.largestChunkBytes === 1000,
    `largestChunkBytes=${m.largestChunkBytes}`,
  );
  assert(m.totalJsBytes === 1500, `totalJsBytes=${m.totalJsBytes}`);
  assert(m.jsChunkCount === 3, `jsChunkCount=${m.jsChunkCount}`);
  assert(
    m.entryChunkBrotliBytes === 0,
    'a dist with no .br sibling must report 0, not crash',
  );
});

check('passes when every measurement is inside its budget', () => {
  const m = measureBundle(dist({ entryBytes: 1000, preloadBytes: [200] }));
  const { breaches, slack } = checkBudget(m, budgetOf(m, 1.05));
  assert(breaches.length === 0, `unexpected breaches: ${breaches.join('; ')}`);
  assert(slack.length === 0, `unexpected slack notes: ${slack.join('; ')}`);
});

check('fails when a measurement is over budget', () => {
  const m = measureBundle(dist({ entryBytes: 1000, preloadBytes: [200] }));
  const budget = budgetOf(m, 1.05);
  budget.entryChunkBytes = 900;
  const { breaches } = checkBudget(m, budget);
  assert(breaches.length === 1, `breaches=${JSON.stringify(breaches)}`);
  assert(
    breaches[0].includes('entryChunkBytes') && breaches[0].includes('900'),
    `breach text does not name the metric and its budget: ${breaches[0]}`,
  );
});

check('asks for the budget to be lowered once it is 20% too loose', () => {
  const m = measureBundle(dist({ entryBytes: 1000, preloadBytes: [200] }));
  const budget = budgetOf(m, 1.05);
  budget.entryChunkBytes = 2000;
  const { breaches, slack } = checkBudget(m, budget);
  assert(breaches.length === 0, `unexpected breaches: ${breaches.join('; ')}`);
  assert(
    slack.length === 1 && slack[0].includes('entryChunkBytes'),
    `slack=${JSON.stringify(slack)}`,
  );
});

check('fails when a metric has no budget at all', () => {
  const m = measureBundle(dist({ entryBytes: 1000, preloadBytes: [200] }));
  const budget = budgetOf(m, 1.05);
  delete budget.totalJsBytes;
  const { breaches } = checkBudget(m, budget);
  assert(
    breaches.length === 1 && breaches[0].includes('totalJsBytes'),
    `breaches=${JSON.stringify(breaches)}`,
  );
});

check('refuses to measure a dist with no JS at all', () => {
  const dir = mkdtempSync(join(tmpdir(), 'bundle-budget-empty-'));
  created.push(dir);
  writeFileSync(
    join(dir, 'index.html'),
    '<!doctype html><script type="module" src="/assets/gone.js"></script>',
  );
  let threw = null;
  try {
    measureBundle(dir);
  } catch (err) {
    threw = err;
  }
  assert(
    threw !== null,
    'a dist with no .js files reported metrics instead of failing',
  );
  assert(
    /no \.js files/.test(threw.message),
    `unexpected message: ${threw && threw.message}`,
  );
});

check('refuses to measure a missing index.html', () => {
  const dir = dist({ entryBytes: 1000, withIndex: false });
  let threw = null;
  try {
    measureBundle(dir);
  } catch (err) {
    threw = err;
  }
  assert(
    threw !== null,
    'a dist with no index.html reported metrics instead of failing',
  );
  assert(
    /no index\.html/.test(threw.message),
    `unexpected message: ${threw && threw.message}`,
  );
});

for (const dir of created) rmSync(dir, { recursive: true, force: true });

if (failures > 0) {
  process.stderr.write(`${failures} bundle-budget self-test case(s) failed\n`);
  process.exit(1);
}
process.stdout.write('bundle-budget self-test OK\n');
