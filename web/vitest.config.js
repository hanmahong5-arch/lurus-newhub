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
      // Ratchet floor — 2026-08-17 measured baseline over 35 test files /
      // 254 tests: statements 11.46-11.47, branches 12.86, functions
      // 11.52-11.54, lines 11.12 (ranges are real run-to-run jitter observed
      // across three consecutive runs). Floors are rounded down below that
      // jitter so ordering alone can never turn CI red. These numbers only
      // ever go UP: when a change raises the measured value, raise the floor
      // with it in the same PR. Never lower them to make a red build pass.
      thresholds: {
        statements: 10,
        branches: 11,
        functions: 10,
        lines: 10,
      },
    },
  },
});
