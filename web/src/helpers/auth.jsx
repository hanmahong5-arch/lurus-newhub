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
import { Navigate } from 'react-router-dom';
import { history } from './history';

export function authHeader() {
  // return authorization header with jwt token
  let user;
  try {
    user = JSON.parse(localStorage.getItem('user'));
  } catch (e) {
    // 损坏的 user 影子数据视为未登录，而不是把调用方一起带崩
    return {};
  }

  if (user && user.token) {
    return { Authorization: 'Bearer ' + user.token };
  } else {
    return {};
  }
}

export const AuthRedirect = ({ children }) => {
  const user = localStorage.getItem('user');

  if (user) {
    return <Navigate to='/console' replace />;
  }

  return children;
};

function PrivateRoute({ children }) {
  if (!localStorage.getItem('user')) {
    return <Navigate to='/login' state={{ from: history.location }} />;
  }
  return children;
}

export function AdminRoute({ children }) {
  const raw = localStorage.getItem('user');
  if (!raw) {
    return <Navigate to='/login' state={{ from: history.location }} />;
  }
  try {
    const user = JSON.parse(raw);
    if (user && typeof user.role === 'number' && user.role >= 10) {
      return children;
    }
  } catch (e) {
    // ignore
  }
  return <Navigate to='/forbidden' replace />;
}

/*
 * One notch above AdminRoute. Pages under /console/v2/admin whose every call
 * goes to a route under /api/v2/admin need this: that whole group is mounted
 * behind middleware.RootJWTAuth (see
 * internal/adapter/handler/router/api-v2-router.go), which refuses a role-10
 * session with 403 PERMISSION_DENIED. Before cycle 12 those routes carried
 * only PrivateRoute, so a role-10 admin who typed the URL got the page shell
 * rendered against a refusal.
 *
 * Deliberately mirrors AdminRoute rather than generalising it: the `typeof
 * user.role === 'number'` check is the load-bearing part (a JSON string "100"
 * would clear a bare >=), and two eight-line guards read better here than one
 * parameterised one whose caller could pass the threshold wrong.
 */
export function RootRoute({ children }) {
  const raw = localStorage.getItem('user');
  if (!raw) {
    return <Navigate to='/login' state={{ from: history.location }} />;
  }
  try {
    const user = JSON.parse(raw);
    if (user && typeof user.role === 'number' && user.role >= 100) {
      return children;
    }
  } catch (e) {
    // A corrupted shim fails closed, same as AdminRoute.
  }
  return <Navigate to='/forbidden' replace />;
}

export { PrivateRoute };
