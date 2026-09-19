#!/usr/bin/env node
// check-bundle-budget.mjs
// First-paint transfer budget for the built SPA. Reads dist/index.html, follows
// the entry script and its modulepreloads, and compares five measurements
// against bundle-budget.json. Exits non-zero on any breach.
//
// Run: bun scripts/check-bundle-budget.mjs
// or:  node web/scripts/check-bundle-budget.mjs --dist web/dist
//
// Why a gate and not a note in a README: the entry chunk reached 6.57 MB
// (2026-09-19 measurement) one import at a time, and no single commit looked
// expensive. Every number here is raw bytes on disk; the .br sizes are printed
// alongside because that is what a visitor actually transfers, but the budget
// is on raw bytes so it still means something in a build without the
// compression plugin.

import { readFileSync, existsSync, readdirSync, statSync } from 'fs';
import { join, dirname, basename } from 'path';
import { fileURLToPath, pathToFileURL } from 'url';

const METRICS = [
  ['entryChunkBytes', 'the module the page loads first'],
  ['firstPaintJsBytes', 'entry chunk + every modulepreload in index.html'],
  ['largestChunkBytes', 'the biggest single .js file in the build'],
  ['totalJsBytes', 'every .js file the build emitted'],
  ['jsChunkCount', 'how many .js files the build emitted'],
];

// A measured value this far under its budget means the budget is stale and is
// no longer holding anything: the report says so out loud rather than letting
// it drift upward again in silence.
const SLACK_FRACTION = 0.8;

function assetHrefs(html, attribute, pattern) {
  const hrefs = [];
  const matcher = new RegExp(pattern, 'g');
  let match;
  while ((match = matcher.exec(html)) !== null) {
    hrefs.push(match[1]);
  }
  return hrefs.map((href) => href.replace(/^\//, '')).filter(Boolean);
}

function sizeOf(path) {
  return existsSync(path) ? statSync(path).size : 0;
}

export function measureBundle(distDir) {
  const indexPath = join(distDir, 'index.html');
  if (!existsSync(indexPath)) {
    throw new Error(
      `no index.html under ${distDir}: run the frontend build before the budget gate`,
    );
  }
  const html = readFileSync(indexPath, 'utf8');

  const entryHrefs = assetHrefs(
    html,
    'src',
    '<script[^>]+type=["\']module["\'][^>]+src=["\']([^"\']+)["\']',
  );
  const preloadHrefs = assetHrefs(
    html,
    'href',
    '<link[^>]+rel=["\']modulepreload["\'][^>]+href=["\']([^"\']+)["\']',
  );
  if (entryHrefs.length === 0) {
    throw new Error(
      `index.html in ${distDir} has no <script type="module">: nothing to measure`,
    );
  }

  const assetsDir = join(distDir, 'assets');
  const jsFiles = existsSync(assetsDir)
    ? readdirSync(assetsDir).filter((name) => name.endsWith('.js'))
    : [];
  if (jsFiles.length === 0) {
    throw new Error(
      `no .js files under ${assetsDir}: the build produced nothing to budget`,
    );
  }

  const jsSizes = jsFiles.map((name) => sizeOf(join(assetsDir, name)));
  const entryPath = join(distDir, entryHrefs[0]);
  const firstPaintPaths = [...entryHrefs, ...preloadHrefs].map((href) =>
    join(distDir, href),
  );

  return {
    entryChunkBytes: sizeOf(entryPath),
    entryChunkBrotliBytes: sizeOf(`${entryPath}.br`),
    firstPaintJsBytes: firstPaintPaths.reduce((sum, p) => sum + sizeOf(p), 0),
    firstPaintChunkCount: firstPaintPaths.length,
    largestChunkBytes: jsSizes.reduce((max, size) => Math.max(max, size), 0),
    largestChunkName: jsFiles[jsSizes.indexOf(Math.max(...jsSizes))],
    totalJsBytes: jsSizes.reduce((sum, size) => sum + size, 0),
    jsChunkCount: jsFiles.length,
    entryChunkName: basename(entryPath),
  };
}

export function checkBudget(metrics, budget) {
  const breaches = [];
  const slack = [];
  for (const [key] of METRICS) {
    const limit = budget[key];
    if (typeof limit !== 'number') {
      breaches.push(`${key}: no budget declared in bundle-budget.json`);
      continue;
    }
    const measured = metrics[key];
    if (measured > limit) {
      breaches.push(
        `${key}: ${measured} exceeds the budget of ${limit} (+${measured - limit})`,
      );
    } else if (measured < limit * SLACK_FRACTION) {
      slack.push(
        `${key}: ${measured} is well under the budget of ${limit} — lower the budget in the same change that earned the room`,
      );
    }
  }
  return { breaches, slack };
}

function formatReport(metrics) {
  const kb = (bytes) => `${(bytes / 1024).toFixed(1)} KiB`;
  return [
    `entry chunk        ${metrics.entryChunkName}: ${kb(metrics.entryChunkBytes)}` +
      (metrics.entryChunkBrotliBytes
        ? ` (${kb(metrics.entryChunkBrotliBytes)} brotli)`
        : ' (no .br sibling)'),
    `first paint JS     ${kb(metrics.firstPaintJsBytes)} over ${metrics.firstPaintChunkCount} files`,
    `largest chunk      ${metrics.largestChunkName}: ${kb(metrics.largestChunkBytes)}`,
    `all JS             ${kb(metrics.totalJsBytes)} over ${metrics.jsChunkCount} files`,
  ].join('\n');
}

function main(argv) {
  const here = dirname(fileURLToPath(import.meta.url));
  const webRoot = join(here, '..');
  const distIndex = argv.indexOf('--dist');
  const distDir =
    distIndex === -1 ? join(webRoot, 'dist') : argv[distIndex + 1];
  const budgetIndex = argv.indexOf('--budget');
  const budgetPath =
    budgetIndex === -1
      ? join(webRoot, 'bundle-budget.json')
      : argv[budgetIndex + 1];

  const metrics = measureBundle(distDir);
  const budget = JSON.parse(readFileSync(budgetPath, 'utf8'));
  const { breaches, slack } = checkBudget(metrics, budget);

  process.stdout.write(`${formatReport(metrics)}\n`);
  for (const note of slack) process.stdout.write(`note: ${note}\n`);
  if (breaches.length > 0) {
    for (const breach of breaches) process.stderr.write(`FAIL ${breach}\n`);
    process.stderr.write(
      'The first paint got heavier. Either move the new weight behind a dynamic import, or raise the budget in this commit and say what it bought.\n',
    );
    return 1;
  }
  process.stdout.write('bundle budget OK\n');
  return 0;
}

const invokedDirectly =
  process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url;
if (invokedDirectly) {
  try {
    process.exit(main(process.argv.slice(2)));
  } catch (err) {
    process.stderr.write(`FAIL ${err.message}\n`);
    process.exit(1);
  }
}
