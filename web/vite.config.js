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

import react from '@vitejs/plugin-react';
import { defineConfig, transformWithEsbuild } from 'vite';
import pkg from '@douyinfe/vite-plugin-semi';
import path from 'path';
import { codeInspectorPlugin } from 'code-inspector-plugin';
import viteCompression from 'vite-plugin-compression';
const { vitePluginSemi } = pkg;

// https://vitejs.dev/config/
export default defineConfig({
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  plugins: [
    codeInspectorPlugin({
      bundler: 'vite',
    }),
    {
      name: 'treat-js-files-as-jsx',
      async transform(code, id) {
        if (!/src\/.*\.js$/.test(id)) {
          return null;
        }

        // Use the exposed transform from vite, instead of directly
        // transforming with esbuild
        return transformWithEsbuild(code, id, {
          loader: 'jsx',
          jsx: 'automatic',
        });
      },
    },
    react(),
    vitePluginSemi({
      cssLayer: true,
    }),
    viteCompression({
      algorithm: 'gzip',
      ext: '.gz',
      threshold: 10240, // Only compress files larger than 10KB
    }),
    viteCompression({
      algorithm: 'brotliCompress',
      ext: '.br',
      threshold: 10240,
    }),
  ],
  optimizeDeps: {
    force: true,
    esbuildOptions: {
      loader: {
        '.js': 'jsx',
        '.json': 'json',
      },
    },
  },
  build: {
    minify: 'esbuild',
    sourcemap: false,
    rollupOptions: {
      output: {
        // Rollup names a chunk after the file that faces it, and every routed
        // page in this tree is a directory with an index.jsx — so the build
        // emitted a crowd of interchangeable assets/index-<hash>.js files and
        // nobody could tell from a waterfall, a budget report or a CDN log
        // which page a chunk belonged to. Fold the directory back into the
        // name for exactly that case.
        chunkFileNames: (chunk) => {
          const facade = chunk.facadeModuleId;
          if (facade && /[\\/]index\.[jt]sx?$/.test(facade)) {
            const dir = path.dirname(facade);
            const label = [path.basename(path.dirname(dir)), path.basename(dir)]
              .filter(
                (part) =>
                  part && !['src', 'pages', 'components'].includes(part),
              )
              .join('-')
              .replace(/[^A-Za-z0-9._-]/g, '_');
            if (label) return `assets/${label}-[hash].js`;
          }
          return 'assets/[name]-[hash].js';
        },
        manualChunks: {
          'react-core': ['react', 'react-dom', 'react-router-dom'],
          'semi-ui': ['@douyinfe/semi-icons', '@douyinfe/semi-ui'],
          tools: ['axios', 'history', 'marked'],
          'react-components': [
            'react-dropzone',
            'react-fireworks',
            'react-telegram-login',
            'react-toastify',
            'react-turnstile',
          ],
          i18n: [
            'i18next',
            'react-i18next',
            'i18next-browser-languagedetector',
          ],
        },
      },
    },
  },
  server: {
    host: '0.0.0.0',
    proxy: {
      '/api': {
        target: 'http://localhost:8012',
        changeOrigin: true,
      },
      '/mj': {
        target: 'http://localhost:8012',
        changeOrigin: true,
      },
      '/pg': {
        target: 'http://localhost:8012',
        changeOrigin: true,
      },
    },
  },
});
