#!/usr/bin/env node
// check-bundle-budget.mjs
// First-paint transfer budget for the built SPA. Reads dist/index.html, follows
// the entry script, its modulepreloads and its stylesheets, and compares seven
// measurements against bundle-budget.json. Exits non-zero on any breach.
//
// Run: bun scripts/check-bundle-budget.mjs
// or:  node web/scripts/check-bundle-budget.mjs --dist web/dist
//
// Why a gate and not a note in a README: the entry chunk reached 6.57 MB
// (2026-09-19 measurement) one import at a time, and no single commit looked
// expensive.
//
// Two of the seven are deliberately not raw-bytes-of-JS:
//   * entryChunkBrotliBytes is what the customer's connection actually carries.
//     Locale tables and generated pricing rows compress ten to one, so a change
//     can add a megabyte of raw weight and cost the visitor almost nothing —
//     and the reverse, a change that adds incompressible weight, is invisible
//     to a raw-bytes budget until it is large. Both directions want watching.
//   * firstPaintCssBytes, because the two render-blocking stylesheets in <head>
//     are bigger than the entry chunk they sit above (1.26 MB against 0.94 MB
//     on the 2026-09-19 build). A budget that only measures .js is blind to the
//     largest first-paint blocker doubling.
// The raw-byte budgets stay primary: they still mean something in a build with
// no compression plugin, where the brotli measurement reports 0.

import { readFileSync, existsSync, readdirSync, statSync } from 'fs';
import { join, dirname, basename } from 'path';
import { fileURLToPath, pathToFileURL } from 'url';

const METRICS = [
  ['entryChunkBytes', 'the module the page loads first'],
  ['entryChunkBrotliBytes', 'what the entry chunk costs on the wire'],
  ['firstPaintJsBytes', 'entry chunk + every modulepreload in index.html'],
  ['firstPaintCssBytes', 'every render-blocking stylesheet in index.html'],
  ['largestChunkBytes', 'the biggest single .js file in the build'],
  ['totalJsBytes', 'every .js file the build emitted'],
  ['jsChunkCount', 'how many .js files the build emitted'],
];

// A measured value this far under its budget means the budget is stale and is
// no longer holding anything: the report says so out loud rather than letting
// it drift upward again in silence.
const SLACK_FRACTION = 0.8;

// The attribute name is already baked into each caller's pattern; this only
// collects capture group 1 and normalises the leading slash away.
function assetHrefs(html, pattern) {
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
    '<script[^>]+type=["\']module["\'][^>]+src=["\']([^"\']+)["\']',
  );
  const preloadHrefs = assetHrefs(
    html,
    '<link[^>]+rel=["\']modulepreload["\'][^>]+href=["\']([^"\']+)["\']',
  );
  const stylesheetHrefs = assetHrefs(
    html,
    '<link[^>]+rel=["\']stylesheet["\'][^>]+href=["\']([^"\']+)["\']',
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

  const cssPaths = stylesheetHrefs.map((href) => join(distDir, href));

  return {
    entryChunkBytes: sizeOf(entryPath),
    entryChunkBrotliBytes: sizeOf(`${entryPath}.br`),
    firstPaintJsBytes: firstPaintPaths.reduce((sum, p) => sum + sizeOf(p), 0),
    firstPaintChunkCount: firstPaintPaths.length,
    firstPaintCssBytes: cssPaths.reduce((sum, p) => sum + sizeOf(p), 0),
    firstPaintCssCount: cssPaths.length,
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
    if (key === 'entryChunkBrotliBytes' && measured === 0) {
      // A zero here is not a small entry chunk: the build emitted no .br
      // sibling, so the precompressed transport in
      // internal/adapter/handler/router/web-router.go has nothing to serve
      // and every visitor is back on per-request gzip.
      breaches.push(
        `${key}: the build emitted no .br sibling for the entry chunk — the precompressed transport has nothing to serve`,
      );
      continue;
    }
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
    `first paint CSS    ${kb(metrics.firstPaintCssBytes)} over ${metrics.firstPaintCssCount} render-blocking stylesheets`,
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
