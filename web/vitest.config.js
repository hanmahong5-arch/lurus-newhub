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
      // Ratchet floor. Raised 2026-08-17 in the same change that lifted the
      // coverage: 98 test files / 1737 tests measure statements 39.08,
      // branches 36.03, functions 33.61, lines 39.00 — up from 11.46 / 12.86 /
      // 11.52 / 11.12 over 35 files / 254 tests earlier the same day, which was
      // itself the first number this config had ever produced (the
      // @vitest/coverage-v8 provider had never been installed).
      //
      // Floors sit ~3pt under the measured values. Observed run-to-run jitter
      // is ~0.01pt, so the margin is for ordinary churn, not for flake.
      //
      // These only ever go UP: when a change raises the measured value, raise
      // the floor with it in the same PR. Never lower them to make a red build
      // pass — the point of the floor is that losing coverage has to be a
      // decision somebody writes down.
      thresholds: {
        statements: 36,
        branches: 33,
        functions: 30,
        lines: 36,
      },
    },
  },
});
