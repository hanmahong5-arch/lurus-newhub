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
import { attemptTagClass, parseRouteAttempts } from './row';

// Routing trace for one log row (moved out of Log/index.jsx unchanged).
const RouteAttempts = ({ selectedLog, tr }) => (
  <>
    {/* Routing trace — only present when the request failed over
        between channels. Answers "this request was slow/failed,
        what did the gateway actually try?", which the single
        channel column cannot. */}
    {parseRouteAttempts(selectedLog).length > 0 && (
      <div style={{ padding: '0 20px 20px' }} data-testid='log-route-attempts'>
        <div className='lbl' style={{ marginBottom: 8 }}>
          {tr('console.log.routing', 'routing')}
        </div>
        <div
          className='panel-paper'
          style={{
            padding: 12,
            fontFamily: 'var(--hf-mono)',
            fontSize: 11,
            lineHeight: 1.7,
          }}
        >
          {parseRouteAttempts(selectedLog).map((a, i) => (
            <div
              key={`${a.channel_id}-${i}`}
              data-testid={`route-attempt-${i}`}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 8,
                flexWrap: 'wrap',
              }}
            >
              <span className='muted'>{i + 1}.</span>
              <span className='strong'>
                #{a.channel_id}
                {a.channel_name ? ` ${a.channel_name}` : ''}
              </span>
              {a.provider && <span className='muted'>{a.provider}</span>}
              <span className={attemptTagClass(a.outcome)}>
                {tr(`console.log.attempt_${a.outcome}`, a.outcome || 'unknown')}
              </span>
              {a.status_code ? (
                <span className='muted'>{a.status_code}</span>
              ) : null}
              {a.error_code ? (
                <span className='faint'>{a.error_code}</span>
              ) : null}
              <span className='muted'>
                {a.duration_ms != null ? `${a.duration_ms}ms` : ''}
              </span>
            </div>
          ))}
        </div>
      </div>
    )}
  </>
);

export default RouteAttempts;
