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
import React from 'react';
import { Link } from 'react-router-dom';

/**
 * HfEmptyState — the one shape a v2 console panel uses for "nothing here
 * yet", replacing a bare caption with a headline, a one-line reason, and — if
 * there is one — a single primary action that takes the operator to the next
 * step (send a request, open the logs, widen a window). No illustration: a
 * small line icon only, so the panel's height stays close to its populated
 * state instead of jumping.
 *
 * Props:
 *   icon     optional; an already-built element (e.g. an svg or react-icons
 *            component). Rendered aria-hidden — the text carries the meaning.
 *   title    optional short headline.
 *   hint     the one-line explanation of why this is empty. Required in
 *            practice; every caller has one, because a bare "nothing here"
 *            with no reason is exactly what this component replaces.
 *   action   optional { label, href } | { label, onClick }. href renders a
 *            router Link (so it never triggers a full page reload inside the
 *            SPA); onClick renders a button. Exactly one of the two is read —
 *            href takes precedence if a caller mistakenly passes both.
 *   testId   forwarded to the root as data-testid.
 */
const HfEmptyState = ({ icon, title, hint, action, testId }) => {
  return (
    <div className='hf-empty-state' data-testid={testId}>
      {icon && (
        <div className='hf-empty-state__icon' aria-hidden='true'>
          {icon}
        </div>
      )}
      {title && <div className='hf-empty-state__title'>{title}</div>}
      {hint && <div className='hf-empty-state__hint'>{hint}</div>}
      {action &&
        (action.href ? (
          <Link to={action.href} className='btn sm hf-empty-state__action'>
            {action.label}
          </Link>
        ) : (
          <button
            type='button'
            className='btn sm hf-empty-state__action'
            onClick={action.onClick}
          >
            {action.label}
          </button>
        ))}
    </div>
  );
};

export default HfEmptyState;
