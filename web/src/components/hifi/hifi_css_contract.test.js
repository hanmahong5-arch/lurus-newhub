/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

/*
 * hifi-tokens.css CSS-discipline contract. Plain-text assertions against the
 * stylesheet source, same technique as the other structural gates in this
 * repo (e.g. src/pages/v2/no_fabricated_status.test.js) — there is no CSS
 * parser in the toolchain, and a regex substring check is enough to pin
 * "this rule exists" without needing one.
 *
 * This does NOT verify the rules actually apply to the right elements (that
 * would need a real browser layout engine) — it only pins that the specific
 * fixes from this pass (search/kbd row alignment, disabled buttons, the
 * nav-h-toggle focus ring, drawer motion-safety, the shared .num/.hf-dense
 * table contract, and the two-theme --hf-info-bg token) don't silently get
 * deleted or renamed out from under the pages that depend on them.
 */
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const CSS = fs.readFileSync(
  path.resolve(HERE, '../../styles/hifi-tokens.css'),
  'utf8',
);

// Isolates the body of the first `@media (prefers-reduced-motion: reduce) {
// ... }` block, so the .hf-drawer assertion below can't accidentally match a
// `.hf-drawer { animation: ... }` rule declared OUTSIDE the motion-safety
// guard (which would defeat the whole point of the rule).
const reducedMotionBlock = (() => {
  const start = CSS.indexOf('@media (prefers-reduced-motion: reduce)');
  expect(start).toBeGreaterThan(-1);
  const braceOpen = CSS.indexOf('{', start);
  // Walk to the matching closing brace — the block itself contains nested
  // rule braces, so this can't be a naive indexOf('}').
  let depth = 0;
  let i = braceOpen;
  for (; i < CSS.length; i++) {
    if (CSS[i] === '{') depth++;
    else if (CSS[i] === '}') {
      depth--;
      if (depth === 0) break;
    }
  }
  return CSS.slice(braceOpen, i + 1);
})();

// Isolates the CSS between the "severity-1 fix" comment above the base
// (unscoped) .search-label/.kbd-group rule and the next "Responsive
// breakpoints" section comment. This is NOT the same as "everything before
// the first @media" — an unrelated @media (max-width: 560px) block exists
// earlier in the file — and it deliberately excludes the two @media-scoped
// copies of this rule further down, which share the exact same
// selector/declaration text as the base rule. Without this isolation, a
// whole-file regex match would still pass even if only the unscoped base
// rule were deleted, silently reintroducing the sev-1 misalignment at
// normal desktop widths (where no @media block is in effect).
const baseSearchLabelBlock = (() => {
  const startMarker = 'Sidebar search button contents (severity-1 fix)';
  const endMarker = 'Responsive breakpoints';
  const start = CSS.indexOf(startMarker);
  expect(start).toBeGreaterThan(-1);
  const end = CSS.indexOf(endMarker, start);
  expect(end).toBeGreaterThan(start);
  return CSS.slice(start, end);
})();

describe('hifi-tokens.css — CSS-discipline contract (2026-09-29)', () => {
  it('the sidebar search label is inline-flex at the base (unscoped, desktop >640px) tier — not only inside a narrower breakpoint copy (severity-1 icon-row-alignment fix)', () => {
    // Scoped to baseSearchLabelBlock, not the whole stylesheet: see the
    // comment above baseSearchLabelBlock for why a whole-file match can't
    // catch a regression that deletes only the base rule.
    expect(baseSearchLabelBlock).toMatch(
      /^\.hf-side \.search-label,\n\.hf-side \.kbd-group \{\n {2}display: inline-flex;/m,
    );
  });

  it('the two responsive display-toggles keep search-label/kbd-group as inline-flex, not block', () => {
    // The ≤640px reveal-tier rule that restores `display: block` must NOT
    // list search-label/kbd-group — reintroducing `block` there is exactly
    // the severity-1 misalignment this pass fixed.
    expect(CSS).toContain(
      '.hf-side .brand-text,\n  .hf-side .nav-h,\n  .hf-side .nav-label,\n  .hf-side .nav-i .nav-badge,\n  .hf-side .footer {\n    display: block;',
    );
    // They get their own inline-flex rule instead.
    expect(CSS).toMatch(
      /\.hf-side \.search-label,\s*\n\s*\.hf-side \.kbd-group \{\s*\n\s*display: inline-flex;\s*\n\s*\}/,
    );
  });

  it('disabled buttons carry a visible, non-interactive style', () => {
    expect(CSS).toMatch(
      /\.hf \.btn:disabled,\s*\n\s*\.hf \.btn\[aria-disabled='true'\] \{/,
    );
    expect(CSS).toMatch(/opacity: 0\.45;/);
    expect(CSS).toMatch(/cursor: not-allowed;/);
  });

  it('the collapsible nav-h-toggle heading joins the shared keyboard focus ring', () => {
    expect(CSS).toMatch(/\.hf-side \.nav-h-toggle:focus-visible/);
  });

  it('the detail drawer animation is neutralized under prefers-reduced-motion', () => {
    expect(reducedMotionBlock).toMatch(
      /\.hf \.hf-drawer \{\s*\n\s*animation: none;/,
    );
  });

  it('the shared .num / .hf-tnum / .hf-dense data-table contract exists (consumed by L2/L4/L5/L6)', () => {
    expect(CSS).toMatch(/\.hf \.num \{/);
    expect(CSS).toMatch(/text-align: right;/);
    expect(CSS).toMatch(/\.hf \.hf-tnum \{/);
    expect(CSS).toMatch(/\.hf table\.t\.hf-dense th,/);
  });

  it('--hf-info-bg is defined once for light (:root) and once for dark ([data-theme=dark])', () => {
    const matches = CSS.match(/--hf-info-bg:\s*rgba\(/g) || [];
    expect(matches.length).toBe(2);
  });

  it('--hf-ok-bg and --hf-row-hover are likewise defined for both themes', () => {
    expect((CSS.match(/--hf-ok-bg:/g) || []).length).toBe(2);
    expect((CSS.match(/--hf-row-hover:/g) || []).length).toBe(2);
  });

  it('the row-hover step is consumed by the actual hover rule, not left orphaned', () => {
    expect(CSS).toMatch(
      /\.hf table\.t tbody tr:hover \{\s*\n[\s\S]*?background: var\(--hf-row-hover\);/,
    );
  });

  it('the three elevation-tier radius/shadow tokens exist and dialog/drawer/toast/popover reference them', () => {
    expect(CSS).toMatch(/--hf-radius-control:\s*2px;/);
    expect(CSS).toMatch(/--hf-radius-menu:\s*6px;/);
    expect(CSS).toMatch(/--hf-radius-dialog:\s*10px;/);
    expect((CSS.match(/--hf-shadow-menu:/g) || []).length).toBe(2);
    expect((CSS.match(/--hf-shadow-dialog:/g) || []).length).toBe(2);

    expect(CSS).toMatch(/border-radius: var\(--hf-radius-dialog\);/); // .hf-dialog
    expect(CSS).toMatch(/box-shadow: var\(--hf-shadow-dialog\);/); // .hf-dialog and/or .hf-drawer
    expect(CSS).toMatch(/border-radius: var\(--hf-radius-menu\);/); // .hf-toast / .hf-menu
    expect(CSS).toMatch(/box-shadow: var\(--hf-shadow-menu\);/); // .hf-toast / .hf-menu
  });

  it('nav-legacy-tag is styled (was previously unstyled, inheriting full-weight ink)', () => {
    expect(CSS).toMatch(/\.nav-legacy-tag \{[\s\S]*?color: var\(--hf-ink-4\);/);
  });

  it('inactive nav items are de-emphasized to ink-3, active items keep full ink', () => {
    expect(CSS).toMatch(
      /\.hf-side \.nav-i \{[\s\S]*?color: var\(--hf-ink-3\);/,
    );
    expect(CSS).toMatch(
      /\.hf-side \.nav-i\.active \{[\s\S]*?color: var\(--hf-ink\);/,
    );
  });

  it('the token page master-detail grid stacks at <=768px instead of pushing the detail pane off-screen (cycle-20 C3)', () => {
    // Base (desktop) rule: two panes. Isolated from the @media copy below so
    // deleting only the responsive rule cannot hide behind the base match.
    const baseStart = CSS.indexOf('.hf-token-grid {');
    expect(baseStart).toBeGreaterThan(-1);
    expect(CSS.slice(baseStart, CSS.indexOf('}', baseStart))).toMatch(
      /grid-template-columns: 380px minmax\(0, 1fr\);/,
    );

    const mediaStart = CSS.indexOf('@media (max-width: 768px) {', baseStart);
    expect(mediaStart).toBeGreaterThan(-1);
    const gridRule = CSS.indexOf('.hf-token-grid {', mediaStart);
    expect(gridRule).toBeGreaterThan(-1);
    const body = CSS.slice(gridRule, CSS.indexOf('}', gridRule));
    // minmax(0, 1fr), not bare 1fr: a bare 1fr track floors at min-content, so
    // the unbroken snippet <pre> would re-widen the column past the viewport.
    expect(body).toMatch(/grid-template-columns: minmax\(0, 1fr\);/);
    expect(body).toMatch(/height: auto;/);
    // Colours must stay on the theme tokens (dark mode shares this rule).
    expect(
      CSS.slice(baseStart, CSS.indexOf('/* [hidden] must hide.', baseStart)),
    ).not.toMatch(/#[0-9a-f]{3,6};|rgba?\(/i);
  });

  it('the shell main column cannot be widened by its children (phone topbar overflow, cycle-20 follow-up)', () => {
    const start = CSS.indexOf('.hf-main {');
    expect(start).toBeGreaterThan(-1);
    expect(CSS.slice(start, CSS.indexOf('}', start))).toMatch(
      /grid-template-columns: minmax\(0, 1fr\);/,
    );
    const phone = CSS.indexOf('@media (max-width: 640px) {');
    const rule = CSS.indexOf('.hf-top .hf-user-name {', phone);
    expect(phone).toBeGreaterThan(-1);
    expect(rule).toBeGreaterThan(phone);
    expect(CSS.slice(rule, CSS.indexOf('}', rule))).toMatch(/display: none;/);
  });

  it('the token page no longer hard-codes its column template inline (an inline style would beat the @media rule)', () => {
    const src = fs.readFileSync(
      path.resolve(HERE, '../../pages/v2/Token/index.jsx'),
      'utf8',
    );
    expect(src).not.toContain("'380px 1fr'");
    expect(src).toContain("className='hf-token-grid'");
  });
});
