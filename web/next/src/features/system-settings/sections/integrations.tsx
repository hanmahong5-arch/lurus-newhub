/*
Copyright (C) 2023-2026 QuantumNous

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
import { OptionForm } from '../components/option-form'
import type { OptionField } from '../lib/option-fields'

const EMAIL_FIELDS: OptionField[] = [
  {
    kind: 'text',
    key: 'SMTPServer',
    label: 'SMTP server',
    description: 'Host name of the outgoing mail server',
    placeholder: 'smtp.example.com',
  },
  {
    kind: 'number',
    key: 'SMTPPort',
    label: 'SMTP port',
    description: 'Usually 465 with SSL, or 587 / 25 without',
    integer: true,
    min: 1,
    max: 65535,
  },
  {
    kind: 'text',
    key: 'SMTPAccount',
    label: 'SMTP account',
    description: 'Login name for the mail server',
  },
  {
    kind: 'text',
    key: 'SMTPFrom',
    label: 'Sender address',
    description: 'The address mail is sent from. Leave empty to use the account.',
    placeholder: 'noreply@example.com',
  },
  {
    kind: 'secret',
    key: 'SMTPToken',
    label: 'SMTP password or access code',
    description:
      'Credential for the mail account. It is never shown again after saving.',
  },
  {
    kind: 'switch',
    key: 'SMTPSSLEnabled',
    label: 'Use SSL',
    description: 'Connect over SSL/TLS',
  },
]

/** Outgoing mail, used for low-balance reminders. */
export function EmailSection() {
  return <OptionForm fields={EMAIL_FIELDS} />
}

const DRAWING_FIELDS: OptionField[] = [
  {
    kind: 'switch',
    key: 'DrawingEnabled',
    label: 'Enable drawing',
    description: 'Offer the image drawing feature to people',
  },
  {
    kind: 'switch',
    key: 'MjNotifyEnabled',
    label: 'Allow callbacks',
    description:
      'Let drawing requests set a callback address. This exposes the server IP address.',
  },
  {
    kind: 'switch',
    key: 'MjAccountFilterEnabled',
    label: 'Allow the account filter parameter',
    description: 'Pass the account filter of a drawing request on to the upstream',
  },
  {
    kind: 'switch',
    key: 'MjForwardUrlEnabled',
    label: 'Rewrite upstream addresses',
    description:
      'Replace the upstream address in drawing results with this server address',
  },
  {
    kind: 'switch',
    key: 'MjModeClearEnabled',
    label: 'Strip speed modes from prompts',
    description:
      'Remove the --fast, --relax and --turbo parameters from prompts before they are sent',
  },
  {
    kind: 'switch',
    key: 'MjActionCheckSuccessEnabled',
    label: 'Require a finished drawing before follow-up actions',
    description:
      'Upscale, variation and similar actions are refused until the original drawing succeeded',
  },
]

export function DrawingSection() {
  return <OptionForm fields={DRAWING_FIELDS} />
}
