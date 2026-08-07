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

// `.hf` scope Suspense fallback for the /console/v2/* route group.
// components/common/ui/Loading.jsx (Semi `<Spin>` + Tailwind) is shared by
// every React.lazy route in App.jsx, including v2 — so every hi-fi code
// chunk load used to flash a non-hi-fi spinner first. This keeps the same
// full-viewport centering but renders inside `.hf` so it reads as part of
// the editorial console, not a foreign loading screen.
import React from 'react';

const HfRouteFallback = () => (
  <div className='hf hf-route-fallback' role='status' aria-live='polite'>
    <span className='dot idle' />
    <span>loading…</span>
  </div>
);

export default HfRouteFallback;
