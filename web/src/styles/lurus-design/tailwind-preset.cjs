// AUTO-GENERATED from shared/lurus-design — DO NOT EDIT; run scripts/design-sync.sh

/**
 * @lurus/design — Tailwind v3 preset
 *
 * Usage (v3 consumers, e.g. Tally):
 *   // tailwind.config.js
 *   module.exports = {
 *     presets: [require('@lurus/design/tailwind-preset')],
 *     ...
 *   };
 *
 * Also import tokens.css (or copy its :root/[data-theme="dark"] blocks) so the
 * CSS custom properties referenced below actually resolve at runtime.
 *
 * Keys are utility names (paper → bg-paper) and stay unprefixed; values point
 * at the --lt-* brand tokens defined in tokens.css.
 */
module.exports = {
  theme: {
    extend: {
      colors: {
        paper: 'var(--lt-paper)',
        bg: 'var(--lt-bg)',
        ink: 'var(--lt-ink)',
        rule: 'var(--lt-rule)',
        ok: 'var(--lt-ok)',
        warn: 'var(--lt-warn)',
        err: 'var(--lt-err)',
        info: 'var(--lt-info)',
        accent: 'var(--lt-accent)',
        'accent-2': 'var(--lt-accent-2)',
      },
      fontFamily: {
        display: ['var(--lt-font-display)'],
        sans: ['var(--lt-font-sans)'],
        mono: ['var(--lt-font-mono)'],
      },
      spacing: {
        '1': '4px',
        '2': '8px',
        '3': '12px',
        '4': '16px',
        '6': '24px',
        '8': '32px',
      },
    },
  },
};
