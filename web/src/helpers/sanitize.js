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
import DOMPurify from 'dompurify';

/**
 * The two ways this codebase is allowed to put untrusted text on screen.
 *
 * `marked` passes raw HTML straight through by design — it is a Markdown
 * renderer, not a sanitiser, and its own `sanitize` option was removed in v5
 * precisely so nobody would mistake it for one. So every
 * `marked.parse(...)` -> `dangerouslySetInnerHTML` pair needs one of these.
 */

/**
 * For rendered Markdown that should keep its formatting: headings, emphasis,
 * links, images. Strips event-handler attributes and script-bearing URLs while
 * leaving the elements themselves intact, so `**bold**` still renders bold.
 *
 * Use this for operator-authored content (announcements, FAQ answers) where
 * the formatting is the point.
 */
export const sanitizeHtml = (html) =>
  DOMPurify.sanitize(String(html ?? ''), { USE_PROFILES: { html: true } });

/**
 * For text that must be DISPLAYED rather than rendered — an upstream model
 * response, a returned error body, anything the operator is reading to work
 * out what a server sent back. Here markup is not formatting, it is content,
 * and hiding it would lose the very thing being inspected. So it is escaped
 * and shown verbatim rather than sanitised away.
 *
 * Only `&`, `<` and `>` are escaped: the result is interpolated into text
 * nodes, never into an attribute value, where a bare quote is inert.
 */
export const escapeHtml = (text) =>
  String(text ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
