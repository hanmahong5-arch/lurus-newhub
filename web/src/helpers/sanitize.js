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
 * The three ways this codebase is allowed to put untrusted text on screen.
 *
 * `marked` passes raw HTML straight through by design — it is a Markdown
 * renderer, not a sanitiser, and its own `sanitize` option was removed in v5
 * precisely so nobody would mistake it for one. So every
 * `marked.parse(...)` -> `dangerouslySetInnerHTML` pair needs one of these.
 *
 * Two of the three sanitise; they differ by WHO wrote the HTML:
 *
 *   sanitizeHtml          — the author is not the operator. An upstream
 *                           model's ```html fence, a third-party release
 *                           note, a legal document served to anonymous
 *                           visitors. Page-wide CSS, inline style attributes and form controls are
 *                           removed, because in this origin they are the
 *                           two primitives an overlay + credential prompt
 *                           needs and no legitimate author of this content
 *                           needs either.
 *   sanitizeOperatorHtml  — a root admin typed it into System settings and
 *                           the settings UI advertises "supports HTML".
 *                           Layout CSS and target="_blank" survive; the
 *                           script/event-handler/javascript:-URL floor and
 *                           the form-control ban are identical.
 *
 * Neither profile is a substitute for the other: swapping them either lets
 * model output paint the console, or silently breaks a customer's footer.
 */

/*
 * Removed from BOTH profiles.
 *
 * DOMPurify's `html` profile ALLOWS <style>, <form>, <input> and <button>
 * (see the html tag list in dompurify's dist bundle), and FORBID_CONTENTS
 * only applies to tags that are not allowed — so without this list a
 * <style> block survives verbatim, `@import url(...)` and all. <style> is
 * additionally in DOMPurify's DEFAULT_FORBID_CONTENTS, so forbidding the
 * tag also drops the CSS text rather than leaving it as loose text.
 *
 * iframe/object/embed are already dropped by the html profile; they are
 * listed so the ban survives a profile change.
 */
const NO_FORM_CONTROLS = [
  'form',
  'input',
  'button',
  'iframe',
  'object',
  'embed',
];

/**
 * For content whose author is NOT the operator: an upstream model response
 * rendered as HTML, a third-party release note, the legal documents. Keeps
 * headings, emphasis, links and images — the formatting is the point — and
 * removes scripts, event handlers, script-bearing URLs, page-wide CSS and
 * form controls.
 */
export const sanitizeHtml = (html) =>
  DOMPurify.sanitize(String(html ?? ''), {
    // svg: a model asked for a diagram answers with one, and the preview
    // must draw it rather than show an empty box; DOMPurify's svg profile
    // drops script and event handlers inside the drawing like anywhere else.
    USE_PROFILES: { html: true, svg: true },
    FORBID_TAGS: ['style', ...NO_FORM_CONTROLS],
    // The style ATTRIBUTE is the overlay primitive: position:fixed;inset:0
    // on any element paints over the whole console origin. Inline styles are
    // formatting nobody who is not the operator needs.
    FORBID_ATTR: ['style'],
  });

/*
 * Forces rel="noopener noreferrer" onto anything that carries a target, so
 * re-admitting target does not hand the opened page a live `window.opener`.
 * Registered around one sanitize() call and removed immediately: DOMPurify
 * hooks are module-global, and the strict profile must not inherit this one.
 */
const forceSafeTargetRel = (node) => {
  if (typeof node?.hasAttribute === 'function' && node.hasAttribute('target')) {
    node.setAttribute('rel', 'noopener noreferrer');
  }
};

/**
 * For HTML a root admin authored in System settings — the footer, the home
 * page body, the announcement/notice text. The settings UI tells them in so
 * many words that HTML is supported, so the two things they actually use
 * survive: a <style> block for layout, and target="_blank" on a link out to
 * their own docs (with rel forced, see above).
 *
 * Everything that makes a page lie about who is asking still goes: script,
 * event-handler attributes, javascript: URLs, and <form>/<input>/<button>.
 * An operator who needs a form on the home page should link to it.
 */
export const sanitizeOperatorHtml = (html) => {
  DOMPurify.addHook('afterSanitizeAttributes', forceSafeTargetRel);
  try {
    return DOMPurify.sanitize(String(html ?? ''), {
      USE_PROFILES: { html: true },
      ADD_ATTR: ['target'],
      FORBID_TAGS: NO_FORM_CONTROLS,
    });
  } finally {
    DOMPurify.removeHook('afterSanitizeAttributes');
  }
};

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
