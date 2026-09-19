module.exports = {
  root: true,
  env: { browser: true, es2021: true, node: true },
  parserOptions: {
    // ES2021 is required for numeric separators (e.g. 500_000) used in
    // src/pages/v2/{Token,Tenants,Settings,Log,Dashboard}/index.jsx and
    // src/components/hifi/UsageRing.jsx. Lift to 2022 to keep room.
    ecmaVersion: 2022,
    sourceType: 'module',
    ecmaFeatures: { jsx: true },
  },
  plugins: ['header', 'react-hooks'],
  overrides: [
    {
      files: ['**/*.{js,jsx}'],
      rules: {
        'header/header': [
          2,
          'block',
          [
            '',
            'Copyright (C) 2025 QuantumNous',
            '',
            'This program is free software: you can redistribute it and/or modify',
            'it under the terms of the GNU Affero General Public License as',
            'published by the Free Software Foundation, either version 3 of the',
            'License, or (at your option) any later version.',
            '',
            'This program is distributed in the hope that it will be useful,',
            'but WITHOUT ANY WARRANTY; without even the implied warranty of',
            'MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the',
            'GNU Affero General Public License for more details.',
            '',
            'You should have received a copy of the GNU Affero General Public License',
            'along with this program. If not, see <https://www.gnu.org/licenses/>.',
            '',
            'For commercial licensing, please contact support@quantumnous.com',
            '',
          ],
        ],
        'no-multiple-empty-lines': ['error', { max: 1 }],
        // eslint-plugin-react-hooks has been installed and listed in
        // `plugins` since the hi-fi console landed, with neither of its two
        // rules turned on — so it linted nothing. Switching rules-of-hooks on
        // found 16 real violations (11 in shipped code), one of which took
        // /privacy-policy and /user-agreement down on every HTML document:
        // components/common/DocumentRenderer called useEffect from inside a
        // branch that sits after four early returns.
        'react-hooks/rules-of-hooks': 'error',
        // exhaustive-deps is a warning, not an error: a missing dependency is
        // sometimes deliberate (mount-once effects), so this is a ratchet
        // carried by the --max-warnings ceiling on the `eslint` script rather than
        // a hard gate. Lower the ceiling when the count drops; never raise it.
        'react-hooks/exhaustive-deps': 'warn',
      },
    },
  ],
};
