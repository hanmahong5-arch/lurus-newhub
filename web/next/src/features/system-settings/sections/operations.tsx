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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { useQuota } from '@/hooks/use-quota'
import { useStatus } from '@/hooks/use-status'

import { LogCleanupCard } from '../components/log-cleanup-card'
import { OptionForm } from '../components/option-form'
import { RateLimitGroupEditor } from '../components/rate-limit-group-editor'
import type { OptionField } from '../lib/option-fields'
import { validateRateLimitGroups } from '../lib/validators'

const BEHAVIOR_FIELDS: OptionField[] = [
  {
    kind: 'switch',
    key: 'DefaultCollapseSidebar',
    label: 'Default Collapse Sidebar',
    description: 'Sidebar collapsed by default for new people',
  },
  {
    kind: 'switch',
    key: 'DemoSiteEnabled',
    label: 'Demo Site Mode',
    description: 'Enable demo mode with limited functionality',
  },
  {
    kind: 'switch',
    key: 'SelfUseModeEnabled',
    label: 'Self-Use Mode',
    description: 'Optimize the gateway for a single trusted operator',
  },
]

/** Quota fields whose hint shows the amount in the operator's money display. */
function useQuotaFields(): OptionField[] {
  const { t } = useTranslation()
  const { formatQuota, ready } = useQuota()
  return useMemo(() => {
    const hint = (value: number | null) =>
      ready && value !== null
        ? t('Equals {{amount}}', { amount: formatQuota(value) })
        : null
    return [
      {
        kind: 'number',
        key: 'QuotaForNewUser',
        label: 'New User Quota',
        description: 'Initial quota given to a new account, in quota units',
        integer: true,
        min: 0,
        hint,
      },
      {
        kind: 'number',
        key: 'QuotaRemindThreshold',
        label: 'Quota Reminder Threshold',
        description: 'Notify an account when its remaining quota falls below this',
        integer: true,
        min: 0,
        hint,
      },
      {
        kind: 'switch',
        key: 'quota_setting.enable_free_model_pre_consume',
        label: 'Pre-Consume for Free Models',
        description:
          'When enabled, zero-cost models also pre-consume quota before final settlement.',
        defaultValue: true,
      },
    ] satisfies OptionField[]
  }, [formatQuota, ready, t])
}

export function BehaviorSection() {
  return <OptionForm fields={BEHAVIOR_FIELDS} />
}

export function QuotaSection() {
  const fields = useQuotaFields()
  return <OptionForm fields={fields} />
}

const CURRENCY_FIELDS: OptionField[] = [
  {
    kind: 'select',
    key: 'general_setting.quota_display_type',
    label: 'Display Mode',
    description: 'Choose how quota values are shown to people',
    defaultValue: 'USD',
    options: [
      { value: 'USD', label: 'USD' },
      { value: 'CNY', label: 'CNY' },
      { value: 'CUSTOM', label: 'Custom Currency' },
      { value: 'TOKENS', label: 'Tokens Only' },
      { value: 'LUTE', label: 'Lute (LUT)' },
    ],
  },
  {
    kind: 'number',
    key: 'USDExchangeRate',
    label: 'CNY per USD',
    description:
      'Real exchange rate between USD and CNY. It also converts quota into wallet money, so changing it changes what is charged.',
    positive: true,
    max: 1000000,
    step: '0.01',
    visibleWhen: (draft) =>
      draft.str('general_setting.quota_display_type') !== 'TOKENS',
  },
  {
    kind: 'text',
    key: 'general_setting.custom_currency_symbol',
    label: 'Custom Currency Symbol',
    description: 'Prefix used when displaying prices',
    placeholder: '¥',
    required: true,
    maxLength: 8,
    visibleWhen: (draft) =>
      draft.str('general_setting.quota_display_type') === 'CUSTOM',
  },
  {
    kind: 'number',
    key: 'general_setting.custom_currency_exchange_rate',
    label: 'Units per USD',
    description: 'Conversion rate from USD to the custom currency',
    positive: true,
    step: '0.01',
    visibleWhen: (draft) =>
      draft.str('general_setting.quota_display_type') === 'CUSTOM',
  },
  {
    kind: 'switch',
    key: 'DisplayTokenStatEnabled',
    label: 'Display Token Statistics',
    description: 'Show token usage statistics in the console',
  },
]

export function CurrencySection() {
  const { t } = useTranslation()
  const { status } = useStatus()
  const perUnit =
    typeof status?.quota_per_unit === 'number' ? status.quota_per_unit : null
  return (
    <OptionForm
      fields={CURRENCY_FIELDS}
      intro={
        <div className='bg-muted/40 text-muted-foreground rounded-lg border px-3 py-2 text-sm'>
          {perUnit === null
            ? t('Quota per unit is not available yet.')
            : t(
                'Quota per unit: {{value}} quota units equal 1 USD. This value is fixed by the server configuration and cannot be changed here.',
                { value: perUnit.toLocaleString() }
              )}
        </div>
      }
    />
  )
}

const CHANNEL_FIELDS: OptionField[] = [
  {
    kind: 'switch',
    key: 'AutomaticDisableChannelEnabled',
    label: 'Automatically disable failing channels',
    description:
      'Disable a channel when it returns an error that matches the rules below',
  },
  {
    kind: 'switch',
    key: 'AutomaticEnableChannelEnabled',
    label: 'Automatically re-enable recovered channels',
    description: 'Enable a disabled channel again once a test succeeds',
  },
  {
    kind: 'number',
    key: 'ChannelDisableThreshold',
    label: 'Slow response threshold (seconds)',
    description:
      'A channel test slower than this counts as a failure. 0 turns the check off.',
    min: 0,
    step: '0.1',
  },
  {
    kind: 'number',
    key: 'RetryTimes',
    label: 'Retry Times',
    description:
      'How many other channels a failed request is retried on before the error is returned',
    integer: true,
    min: 0,
  },
  {
    kind: 'text',
    key: 'AutomaticDisableKeywords',
    label: 'Disable keywords',
    description:
      'One keyword per line. An upstream error containing one of them disables the channel (case-insensitive).',
    placeholder: 'quota exceeded',
    rows: 6,
  },
  {
    kind: 'switch',
    key: 'monitor_setting.auto_test_channel_enabled',
    label: 'Scheduled channel tests',
    description:
      'Test every channel on a timer. The CHANNEL_TEST_FREQUENCY environment variable overrides this when it is set.',
  },
  {
    kind: 'number',
    key: 'monitor_setting.auto_test_channel_minutes',
    label: 'Test interval (minutes)',
    positive: true,
    visibleWhen: (draft) => draft.bool('monitor_setting.auto_test_channel_enabled'),
  },
  {
    kind: 'select',
    key: 'monitor_setting.auto_test_channel_mode',
    label: 'Channels to test',
    description: 'A channel can override this in its own settings',
    defaultValue: 'all',
    options: [
      { value: 'all', label: 'All channels' },
      { value: 'auto_ban_only', label: 'Only channels with auto-disable on' },
      { value: 'none', label: 'None' },
    ],
    visibleWhen: (draft) => draft.bool('monitor_setting.auto_test_channel_enabled'),
  },
  {
    kind: 'switch',
    key: 'model_probe_setting.enabled',
    label: 'Per-model health probe',
    description:
      'Probe every model of a pooled channel on its own schedule, not just the first one',
  },
  {
    kind: 'text',
    key: 'model_probe_setting.groups',
    label: 'Probed groups',
    description: 'Comma-separated channel groups eligible for probing',
    placeholder: 'free',
    visibleWhen: (draft) => draft.bool('model_probe_setting.enabled'),
  },
  {
    kind: 'number',
    key: 'model_probe_setting.interval_hours',
    label: 'Probe interval (hours)',
    description: 'How long a channel and model pair may go unprobed',
    positive: true,
    visibleWhen: (draft) => draft.bool('model_probe_setting.enabled'),
  },
  {
    kind: 'number',
    key: 'model_probe_setting.disable_threshold',
    label: 'Failures before disabling',
    description: 'Consecutive failures (other than rate limits) before a pair is disabled',
    integer: true,
    min: 1,
    visibleWhen: (draft) => draft.bool('model_probe_setting.enabled'),
  },
  {
    kind: 'number',
    key: 'model_probe_setting.max_probes_per_run',
    label: 'Probes per run',
    integer: true,
    min: 1,
    visibleWhen: (draft) => draft.bool('model_probe_setting.enabled'),
  },
  {
    kind: 'number',
    key: 'model_probe_setting.sleep_seconds',
    label: 'Pause between probes (seconds)',
    min: 0,
    visibleWhen: (draft) => draft.bool('model_probe_setting.enabled'),
  },
]

export function ChannelsSection() {
  return <OptionForm fields={CHANNEL_FIELDS} />
}

const LOG_FIELDS: OptionField[] = [
  {
    kind: 'switch',
    key: 'LogConsumeEnabled',
    label: 'Record quota usage',
    description: 'Write one log entry for every request that consumes quota',
  },
  {
    kind: 'switch',
    key: 'DataExportEnabled',
    label: 'Dashboard data export',
    description: 'Aggregate usage for the dashboard charts',
  },
  {
    kind: 'number',
    key: 'DataExportInterval',
    label: 'Export interval (minutes)',
    description: 'How often usage is aggregated',
    integer: true,
    min: 1,
    visibleWhen: (draft) => draft.bool('DataExportEnabled'),
  },
  {
    kind: 'select',
    key: 'DataExportDefaultTime',
    label: 'Default chart granularity',
    defaultValue: 'hour',
    options: [
      { value: 'hour', label: 'Hour' },
      { value: 'day', label: 'Day' },
      { value: 'week', label: 'Week' },
    ],
    visibleWhen: (draft) => draft.bool('DataExportEnabled'),
  },
]

export function LogsSection() {
  return (
    <OptionForm fields={LOG_FIELDS}>
      <LogCleanupCard />
    </OptionForm>
  )
}

const MODERATION_FIELDS: OptionField[] = [
  {
    kind: 'switch',
    key: 'CheckSensitiveEnabled',
    label: 'Check for sensitive words',
    description: 'Scan request content against the word list below',
  },
  {
    kind: 'switch',
    key: 'CheckSensitiveOnPromptEnabled',
    label: 'Check prompts',
    description: 'Also scan the prompt, not only generated content',
    visibleWhen: (draft) => draft.bool('CheckSensitiveEnabled'),
  },
  {
    kind: 'switch',
    key: 'StopOnSensitiveEnabled',
    label: 'Stop a stream on a match',
    description: 'End a streaming response as soon as a sensitive word appears',
    visibleWhen: (draft) => draft.bool('CheckSensitiveEnabled'),
  },
  {
    kind: 'text',
    key: 'SensitiveWords',
    label: 'Sensitive words',
    description: 'One word per line',
    rows: 8,
    visibleWhen: (draft) => draft.bool('CheckSensitiveEnabled'),
  },
]

export function ModerationSection() {
  return <OptionForm fields={MODERATION_FIELDS} />
}

const PORT_PATTERN = /^\d{1,5}(-\d{1,5})?$/

const OUTBOUND_FIELDS: OptionField[] = [
  {
    kind: 'switch',
    key: 'fetch_setting.enable_ssrf_protection',
    label: 'Enable SSRF Protection',
    description:
      'Refuse server-side fetches to private networks and unlisted targets',
    defaultValue: true,
  },
  {
    kind: 'switch',
    key: 'fetch_setting.allow_private_ip',
    label: 'Allow Private IPs',
    description: 'Let server-side fetches reach private address ranges',
    visibleWhen: (draft) => draft.bool('fetch_setting.enable_ssrf_protection'),
  },
  {
    kind: 'select',
    key: 'fetch_setting.domain_filter_mode',
    label: 'Domain Filter Mode',
    description: 'Choose how to filter domains',
    defaultValue: 'false',
    options: [
      { value: 'false', label: 'Blacklist (Block listed domains)' },
      { value: 'true', label: 'Whitelist (Only allow listed domains)' },
    ],
    visibleWhen: (draft) => draft.bool('fetch_setting.enable_ssrf_protection'),
  },
  {
    kind: 'list',
    key: 'fetch_setting.domain_list',
    label: 'Domain list',
    description: 'One domain per line. Wildcards such as *.example.com are accepted.',
    placeholder: 'example.com',
    visibleWhen: (draft) => draft.bool('fetch_setting.enable_ssrf_protection'),
  },
  {
    kind: 'select',
    key: 'fetch_setting.ip_filter_mode',
    label: 'IP Filter Mode',
    description: 'Choose how to filter IP addresses',
    defaultValue: 'false',
    options: [
      { value: 'false', label: 'Blacklist (Block listed IPs)' },
      { value: 'true', label: 'Whitelist (Only allow listed IPs)' },
    ],
    visibleWhen: (draft) => draft.bool('fetch_setting.enable_ssrf_protection'),
  },
  {
    kind: 'list',
    key: 'fetch_setting.ip_list',
    label: 'IP list',
    description: 'One IP address or CIDR range per line',
    placeholder: '10.0.0.0/8',
    visibleWhen: (draft) => draft.bool('fetch_setting.enable_ssrf_protection'),
  },
  {
    kind: 'list',
    key: 'fetch_setting.allowed_ports',
    label: 'Allowed Ports',
    description: 'Comma-separated ports or ranges, for example 80, 443, 8000-9000',
    placeholder: '80,443,8080',
    separator: 'comma',
    validateItem: (item) =>
      PORT_PATTERN.test(item) ? null : 'Enter ports such as 80 or 8000-9000',
    visibleWhen: (draft) => draft.bool('fetch_setting.enable_ssrf_protection'),
  },
  {
    kind: 'switch',
    key: 'fetch_setting.apply_ip_filter_for_domain',
    label: 'Apply IP Filter to Resolved Domains',
    description: 'Experimental: also run the IP filter on what a domain resolves to',
    visibleWhen: (draft) => draft.bool('fetch_setting.enable_ssrf_protection'),
  },
  {
    kind: 'text',
    key: 'WorkerUrl',
    label: 'Worker URL',
    description: 'Proxy worker used to fetch images and files on behalf of the server',
    placeholder: 'https://worker.example.com',
    url: true,
    trimTrailingSlash: true,
  },
  {
    kind: 'secret',
    key: 'WorkerValidKey',
    label: 'Worker access key',
    description: 'Shared secret the worker expects. It is never shown again after saving.',
  },
  {
    kind: 'switch',
    key: 'WorkerAllowHttpImageRequestEnabled',
    label: 'Allow plain HTTP image requests',
    description: 'Let the worker fetch images over http as well as https',
  },
]

export function OutboundSection() {
  return <OptionForm fields={OUTBOUND_FIELDS} />
}

const LIMIT_FIELDS: OptionField[] = [
  {
    kind: 'switch',
    key: 'ModelRequestRateLimitEnabled',
    label: 'Enable rate limiting',
    description:
      'This controls model request rate limiting. Web and API route throttling is configured by environment variables and may still return 429.',
  },
  {
    kind: 'number',
    key: 'ModelRequestRateLimitDurationMinutes',
    label: 'Limit period (minutes)',
    description: 'Time window for rate limiting',
    integer: true,
    min: 0,
    visibleWhen: (draft) => draft.bool('ModelRequestRateLimitEnabled'),
  },
  {
    kind: 'number',
    key: 'ModelRequestRateLimitCount',
    label: 'Max requests per period',
    description: 'Including failed requests, 0 = unlimited',
    integer: true,
    min: 0,
    max: 100000000,
    visibleWhen: (draft) => draft.bool('ModelRequestRateLimitEnabled'),
  },
  {
    kind: 'number',
    key: 'ModelRequestRateLimitSuccessCount',
    label: 'Max successful requests',
    description: 'Only successful requests',
    integer: true,
    min: 1,
    max: 100000000,
    visibleWhen: (draft) => draft.bool('ModelRequestRateLimitEnabled'),
  },
  {
    kind: 'json',
    key: 'ModelRequestRateLimitGroup',
    label: 'Group-based rate limits',
    expect: 'object',
    description:
      'JSON object {"group": [maxRequests, maxSuccess]}. A group entry overrides the global limits and shares the same period.',
    placeholder: '{\n  "default": [200, 100]\n}',
    heightClassName: 'h-48 min-h-48 max-h-48',
    validate: validateRateLimitGroups,
    editor: (props) => <RateLimitGroupEditor {...props} />,
    visibleWhen: (draft) => draft.bool('ModelRequestRateLimitEnabled'),
  },
]

export function LimitsSection() {
  return <OptionForm fields={LIMIT_FIELDS} />
}

const RELAY_FIELDS: OptionField[] = [
  {
    kind: 'switch',
    key: 'global.pass_through_request_enabled',
    label: 'Pass-through requests',
    description:
      'Forward the request body to the upstream unchanged instead of converting it',
  },
  {
    kind: 'list',
    key: 'global.thinking_model_blacklist',
    label: 'Thinking suffix blacklist',
    description:
      'One model name per line. Models listed here keep their thinking suffix instead of having it interpreted.',
    placeholder: 'model-a',
    rows: 4,
  },
  {
    kind: 'switch',
    key: 'general_setting.ping_interval_enabled',
    label: 'Keep streams alive',
    description:
      'Send a periodic ping on a streaming response so idle proxies do not cut it',
  },
  {
    kind: 'number',
    key: 'general_setting.ping_interval_seconds',
    label: 'Ping interval (seconds)',
    integer: true,
    min: 1,
    visibleWhen: (draft) => draft.bool('general_setting.ping_interval_enabled'),
  },
  {
    kind: 'number',
    key: 'StreamCacheQueueLength',
    label: 'Stream cache queue length',
    description: 'Stream mode cache queue length. 0 means no cache.',
    integer: true,
    min: 0,
  },
]

export function RelaySection() {
  return <OptionForm fields={RELAY_FIELDS} />
}
