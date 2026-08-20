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
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.js'],
    include: ['src/**/*.{test,spec}.{js,jsx,ts,tsx}'],
    exclude: ['node_modules', 'tests/e2e/**', 'dist'],
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html', 'json-summary'],
      include: ['src/**/*.{js,jsx}'],
      exclude: [
        'src/test/**',
        'src/**/*.test.{js,jsx}',
        'src/main.jsx',
        'src/i18n/**',
      ],
      // Ratchet floor. Raised 2026-08-20 in the same change that lifted the
      // coverage: 139 test files / 2724 tests measure statements 49.80,
      // branches 47.70, functions 44.94, lines 49.95 — up from 39.08 / 36.03 /
      // 33.61 / 39.00 over 98 files / 1737 tests on 2026-08-17, which was
      // itself up from 11.46 / 12.86 / 11.52 / 11.12, the first number this
      // config had ever produced (the @vitest/coverage-v8 provider had never
      // been installed until that day).
      //
      // Floors sit ~3pt under the measured values. Observed run-to-run jitter
      // is ~0.01pt, so the margin is for ordinary churn, not for flake.
      //
      // These only ever go UP: when a change raises the measured value, raise
      // the floor with it in the same PR. Never lower them to make a red build
      // pass — the point of the floor is that losing coverage has to be a
      // decision somebody writes down.
      thresholds: {
        statements: 46,
        branches: 44,
        functions: 41,
        lines: 46,
      },
    },
  },
});
