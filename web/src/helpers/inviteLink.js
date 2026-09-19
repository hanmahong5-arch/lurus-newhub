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

/**
 * The delivery URL an operator sends an invitee: what InvitesDrawer.jsx
 * builds and issues once, and what OidcRedirect.jsx:readInviteCode() /
 * return_to rebuild after the identity round trip — those two call sites
 * import THIS module rather than each assembling the string themselves, so
 * the shape can no longer drift between "issue" and "carry it back".
 * Must match OidcRedirect.jsx's readInviteCode() query param name (invite)
 * and the /login route App.jsx mounts OidcRedirect on.
 */
export const inviteLink = (origin, code) =>
  `${origin}/login?invite=${encodeURIComponent(code)}`;
