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
import React, { Fragment, useCallback, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import WIPBanner from '../../../components/hifi/WIPBanner';
import ConfirmDialog from '../../../components/common/ConfirmDialog';
import { useFormDraft } from '../../../hooks/common/useFormDraft';
import { API, showError } from '../../../helpers';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

/* HiFi 13 — Flows: multi-step wizards & incident response. Ported from hifi/hf13-flows.jsx. */

// Second column is the English fallback; display labels resolve through
// tr(`console.flows.flow_${key}`, fallback) at render time.
const FLOWS = [
  ['newChannel', 'New Channel', 4],
  ['newToken', 'New Token', 3],
  ['incident', 'Incident Response', 4],
  ['retry', 'Retry Chain Visualizer', 1],
];

const Stepper = ({ steps, cur }) => (
  <div
    style={{
      padding: '20px 40px',
      borderBottom: '1px solid var(--hf-rule)',
      background: 'var(--hf-paper)',
      display: 'flex',
      alignItems: 'center',
      gap: 10,
    }}
  >
    {steps.map((s, i) => (
      <Fragment key={i}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <div
            style={{
              width: 22,
              height: 22,
              borderRadius: '50%',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontFamily: 'var(--hf-mono)',
              fontSize: 11,
              fontWeight: 600,
              background:
                i + 1 < cur
                  ? 'var(--hf-ok)'
                  : i + 1 === cur
                    ? 'var(--hf-accent)'
                    : 'var(--hf-sunken)',
              color: i + 1 <= cur ? '#fff' : 'var(--hf-ink-3)',
              border:
                '1px solid ' +
                (i + 1 <= cur ? 'transparent' : 'var(--hf-rule)'),
            }}
          >
            {i + 1 < cur ? '✓' : i + 1}
          </div>
          <span
            className='strong'
            style={{
              fontSize: 12,
              color: i + 1 === cur ? 'var(--hf-ink)' : 'var(--hf-ink-3)',
            }}
          >
            {s}
          </span>
        </div>
        {i < steps.length - 1 && (
          <div style={{ flex: 1, height: 1, background: 'var(--hf-rule)' }} />
        )}
      </Fragment>
    ))}
  </div>
);

const NewChannelStep = ({ step }) => {
  // Sub-components defined in this file each own their useTranslation() hook.
  const { t: tr } = useTranslation();
  if (step === 1) {
    return (
      <div>
        <div className='lbl'>
          {tr('console.flows.step_of', 'step {{step}} of {{total}}', {
            step: 1,
            total: 4,
          })}
        </div>
        <h1 className='display' style={{ fontSize: 28, margin: '4px 0 4px' }}>
          {tr('console.flows.pick_vendor', 'Pick a vendor')}
        </h1>
        <div className='muted' style={{ marginBottom: 22 }}>
          {tr(
            'console.flows.pick_vendor_sub',
            "we'll handle the protocol differences for you",
          )}
        </div>
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(4, 1fr)',
            gap: 12,
          }}
        >
          {[
            'OpenAI',
            'Anthropic',
            'Google Vertex',
            'Azure',
            'AWS Bedrock',
            'Zhipu',
            'SiliconFlow',
            tr('console.flows.vendor_custom', 'custom'),
          ].map((v, i) => (
            <div
              key={i}
              className='panel'
              style={{
                padding: 16,
                cursor: 'pointer',
                border:
                  i === 0
                    ? '2px solid var(--hf-accent)'
                    : '1px solid var(--hf-rule)',
              }}
            >
              <div className='display' style={{ fontSize: 15 }}>
                {v}
              </div>
              <div
                className='faint mono'
                style={{ fontSize: 10, marginTop: 4 }}
              >
                {i === 0
                  ? tr(
                      'console.flows.vendor_hint_oai',
                      '18 models · OAI-compatible',
                    )
                  : i < 7
                    ? tr('console.flows.vendor_hint_native', 'native protocol')
                    : tr(
                        'console.flows.vendor_hint_custom',
                        'OAI-compatible URL',
                      )}
              </div>
            </div>
          ))}
        </div>
      </div>
    );
  }
  if (step === 2) {
    const fields = [
      [
        tr('console.flows.field_channel_name', 'channel name'),
        'openai/main-2',
        tr('console.flows.hint_identifier', 'identifier · alphanumeric'),
      ],
      [
        tr('console.flows.field_base_url', 'base url'),
        'https://api.openai.com/v1',
        tr('console.flows.hint_override', 'override for proxies'),
      ],
      [
        tr('console.flows.field_api_keys', 'api keys'),
        'sk-•••••••• ••••••••  ⊕ add another',
        tr('console.flows.hint_keys_rr', '4 keys · round-robin'),
      ],
      [
        tr('console.flows.field_org_id', 'organization id'),
        'org-acme-prod',
        tr('console.flows.hint_optional', 'optional'),
      ],
      [
        tr('console.flows.field_custom_headers', 'custom headers'),
        tr('console.flows.none', '— none —'),
        tr('console.flows.hint_optional', 'optional'),
      ],
    ];
    return (
      <div>
        <div className='lbl'>
          {tr('console.flows.step_of', 'step {{step}} of {{total}}', {
            step: 2,
            total: 4,
          })}
        </div>
        <h1 className='display' style={{ fontSize: 28, margin: '4px 0 4px' }}>
          {tr('console.flows.credentials_title', 'Credentials & endpoint')}
        </h1>
        <div className='muted' style={{ marginBottom: 22 }}>
          {tr(
            'console.flows.credentials_sub',
            'encrypted at rest · never logged',
          )}
        </div>
        <div className='panel' style={{ padding: 22 }}>
          {fields.map((r, i) => (
            <div
              key={i}
              style={{
                display: 'grid',
                gridTemplateColumns: '160px 1fr 200px',
                padding: '14px 0',
                borderBottom:
                  i < fields.length - 1 ? '1px dashed var(--hf-rule)' : 0,
                alignItems: 'center',
                gap: 14,
              }}
            >
              <span className='lbl'>{r[0]}</span>
              <span className='strong mono' style={{ fontSize: 12 }}>
                {r[1]}
              </span>
              <span className='faint mono' style={{ fontSize: 10 }}>
                {r[2]}
              </span>
            </div>
          ))}
        </div>
        <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
          <button
            type='button'
            className='btn ghost'
            disabled
            title={tr(
              'console.flows.test_connection_tip',
              'Select a channel in the Channels page to test it',
            )}
            data-testid='flow-test-connection-btn'
          >
            {tr('console.flows.test_connection', '▶ test connection')}
          </button>
          <span className='muted' style={{ alignSelf: 'center', fontSize: 11 }}>
            {tr(
              'console.flows.test_connection_hint',
              '→ select a channel in the Channels page, then click "Test channel"',
            )}
          </span>
        </div>
      </div>
    );
  }
  if (step === 3) {
    const rows = [
      ['gpt-4o-2024-11-20', 'gpt-4o', true],
      ['gpt-4o-mini-2024-07-18', 'gpt-4o-mini', true],
      ['gpt-4o-realtime', 'gpt-4o-realtime', true],
      ['o1-preview', 'o1-preview', true],
      ['text-embedding-3-large', 'embedding', true],
      ['dall-e-3', tr('console.flows.skip_value', '— skip —'), false],
    ];
    return (
      <div>
        <div className='lbl'>
          {tr('console.flows.step_of', 'step {{step}} of {{total}}', {
            step: 3,
            total: 4,
          })}
        </div>
        <h1 className='display' style={{ fontSize: 28, margin: '4px 0 4px' }}>
          {tr('console.flows.map_models', 'Map models')}
        </h1>
        <div className='muted' style={{ marginBottom: 22 }}>
          {tr(
            'console.flows.map_models_sub',
            'vendor model → your alias · we filled this in',
          )}
        </div>
        <div className='panel'>
          <div className='hf-table-scroll'>
            <table className='t'>
              <thead>
                <tr>
                  <th></th>
                  <th>{tr('console.flows.th_vendor_model', 'vendor model')}</th>
                  <th></th>
                  <th>{tr('console.flows.th_your_alias', 'your alias')}</th>
                  <th>{tr('console.flows.th_auto', 'auto')}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r, i) => (
                  <tr key={i}>
                    <td>
                      <input type='checkbox' defaultChecked={r[2]} />
                    </td>
                    <td className='mono strong'>{r[0]}</td>
                    <td className='faint'>→</td>
                    <td>
                      <div
                        className='field'
                        style={{ width: 200, height: 24, fontSize: 11 }}
                      >
                        {r[1]}
                      </div>
                    </td>
                    <td>
                      {r[2] && (
                        <span className='tag info'>
                          {tr('console.flows.tag_auto', 'auto')}
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>
    );
  }
  // step 4
  const summary = [
    [tr('console.flows.sum_vendor', 'vendor'), 'OpenAI'],
    [tr('console.flows.field_channel_name', 'channel name'), 'openai/main-2'],
    [tr('console.flows.field_base_url', 'base url'), 'api.openai.com/v1'],
    [
      tr('console.flows.sum_keys', 'keys'),
      tr('console.flows.val_keys_rr', '4 (round-robin)'),
    ],
    [
      tr('console.flows.sum_models_enabled', 'models enabled'),
      tr('console.flows.val_models_enabled', '5 of 18'),
    ],
    [
      tr('console.flows.sum_routing_weight', 'routing weight'),
      tr('console.flows.val_weight_primary', '1.0 · primary'),
    ],
    [tr('console.flows.sum_groups', 'groups'), 'default, vip'],
    [
      tr('console.flows.sum_fallback_to', 'fallback to'),
      'openai/main, openai/backup',
    ],
  ];
  return (
    <div>
      <div className='lbl'>
        {tr('console.flows.step_of', 'step {{step}} of {{total}}', {
          step: 4,
          total: 4,
        })}
      </div>
      <h1 className='display' style={{ fontSize: 28, margin: '4px 0 4px' }}>
        {tr('console.flows.review_title', 'Review & enable')}
      </h1>
      <div className='muted' style={{ marginBottom: 22 }}>
        {tr('console.flows.review_sub', 'pre-flight summary')}
      </div>
      <div className='panel' style={{ padding: 22 }}>
        {summary.map((r, i) => (
          <div
            key={i}
            style={{
              display: 'grid',
              gridTemplateColumns: '180px 1fr',
              padding: '10px 0',
              borderBottom:
                i < summary.length - 1 ? '1px dashed var(--hf-rule)' : 0,
            }}
          >
            <span className='lbl'>{r[0]}</span>
            <span className='strong' style={{ fontSize: 13 }}>
              {r[1]}
            </span>
          </div>
        ))}
      </div>
      <div
        className='panel'
        style={{
          padding: 14,
          marginTop: 14,
          background: 'var(--hf-paper)',
          borderLeft: '2px solid var(--hf-ok)',
        }}
      >
        <span className='strong'>
          {tr('console.flows.preflight_passed', 'Pre-flight passed.')}
        </span>{' '}
        <span className='muted'>
          {tr(
            'console.flows.preflight_detail',
            '42ms ttft · 18 models · keys all reachable',
          )}
        </span>
      </div>
    </div>
  );
};

// Draft key shared between NewTokenStep and HFFlows nav buttons.
const TOKEN_DRAFT_KEY = 'flow-newtoken-draft';
const TOKEN_DRAFT_INIT = {
  name: '',
  group: '',
  unlimited_quota: true,
  remain_quota: 500000,
  expires_at: '',
};

// NewTokenStep receives the shared draft state and callbacks from HFFlows so
// that it does not own local state (wizard steps re-mount on flow tab switch).
const NewTokenStep = ({
  step,
  draft,
  setDraft,
  createdKey,
  onCopyKey,
  onConfirmOpen,
  confirmVisible,
  onConfirmCancel,
  onSubmit,
}) => {
  // Own hook — sub-component defined in the same file.
  const { t: tr } = useTranslation();
  if (step === 3 && createdKey) {
    return (
      <div
        style={{ textAlign: 'center', padding: '40px 0' }}
        data-testid='newtoken-success'
      >
        <div
          style={{
            width: 56,
            height: 56,
            borderRadius: '50%',
            background: 'var(--hf-ok)',
            color: '#fff',
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            fontSize: 24,
            marginBottom: 16,
          }}
        >
          ✓
        </div>
        <h1 className='display' style={{ fontSize: 28, margin: '0 0 8px' }}>
          {tr('console.flows.token_created', 'Token created')}
        </h1>
        <div className='muted'>
          {tr(
            'console.flows.copy_once',
            "copy this once · it won't be shown again",
          )}
        </div>
        <div
          className='panel-paper'
          style={{
            display: 'inline-block',
            padding: '14px 22px',
            marginTop: 22,
          }}
        >
          <span
            className='mono strong'
            data-testid='newtoken-key'
            style={{ fontSize: 14, letterSpacing: '0.04em' }}
          >
            {createdKey}
          </span>
          <button
            type='button'
            className='btn sm'
            data-testid='newtoken-copy'
            style={{ marginLeft: 12 }}
            onClick={onCopyKey}
          >
            {tr('console.common.copy', 'copy')}
          </button>
        </div>
      </div>
    );
  }

  if (step === 3) {
    // Review + confirm step — shows summary and opens ConfirmDialog on submit.
    return (
      <>
        <div className='lbl'>
          {tr('console.flows.step_of', 'step {{step}} of {{total}}', {
            step: 3,
            total: 3,
          })}{' '}
          · {tr('console.flows.step_review', 'review')}
        </div>
        <h1 className='display' style={{ fontSize: 28, margin: '4px 0 22px' }}>
          {tr('console.flows.review_token', 'Review token')}
        </h1>
        <div className='panel' style={{ padding: 22 }}>
          {[
            [tr('console.flows.label_name', 'name'), draft.name || '—'],
            [
              tr('console.flows.label_group', 'group'),
              draft.group || 'default',
            ],
            [
              tr('console.flows.label_quota', 'quota'),
              draft.unlimited_quota
                ? tr('console.flows.unlimited', 'unlimited')
                : String(draft.remain_quota),
            ],
            [
              tr('console.flows.label_expires', 'expires'),
              draft.expires_at || tr('console.flows.never', 'never'),
            ],
          ].map((r, i, arr) => (
            <div
              key={r[0]}
              style={{
                display: 'grid',
                gridTemplateColumns: '180px 1fr',
                padding: '12px 0',
                borderBottom:
                  i < arr.length - 1 ? '1px dashed var(--hf-rule)' : 0,
                alignItems: 'center',
              }}
            >
              <span className='lbl'>{r[0]}</span>
              <span className='strong mono' style={{ fontSize: 12 }}>
                {r[1]}
              </span>
            </div>
          ))}
        </div>
        <div style={{ marginTop: 24, textAlign: 'right' }}>
          <button
            type='button'
            className='btn primary'
            data-testid='newtoken-open-confirm'
            onClick={onConfirmOpen}
          >
            {tr('console.flows.create_token_btn', 'create token →')}
          </button>
        </div>
        <ConfirmDialog
          visible={confirmVisible}
          title={tr(
            'console.flows.confirm_create_title',
            'Confirm token creation',
          )}
          consequenceList={[
            tr(
              'console.flows.confirm_create_c1',
              'The token key is shown only once after creation.',
            ),
            tr(
              'console.flows.confirm_create_c2',
              'Once created it counts against your quota immediately.',
            ),
          ]}
          confirmText={draft.name}
          confirmButtonText={tr(
            'console.flows.confirm_create_btn',
            'Create token',
          )}
          confirmButtonType='primary'
          onConfirm={onSubmit}
          onCancel={onConfirmCancel}
        />
      </>
    );
  }

  if (step === 1) {
    return (
      <>
        <div className='lbl'>
          {tr('console.flows.step_of', 'step {{step}} of {{total}}', {
            step: 1,
            total: 3,
          })}{' '}
          · {tr('console.flows.step_scope', 'name & scope')}
        </div>
        <h1 className='display' style={{ fontSize: 28, margin: '4px 0 22px' }}>
          {tr('console.flows.token_purpose', 'What is this token for?')}
        </h1>
        <div
          className='panel'
          style={{ padding: 22, display: 'grid', gap: 16 }}
        >
          <label style={{ display: 'grid', gap: 6 }}>
            <span className='lbl'>
              {tr('console.flows.token_name', 'token name')}
            </span>
            <input
              className='input'
              data-testid='newtoken-name'
              value={draft.name}
              onChange={(e) =>
                setDraft((d) => ({ ...d, name: e.target.value }))
              }
              placeholder={tr(
                'console.flows.ph_token_name',
                'e.g. lurus-edit · prod',
              )}
            />
          </label>
          <label style={{ display: 'grid', gap: 6 }}>
            <span className='lbl'>
              {tr('console.flows.group_optional', 'group (optional)')}
            </span>
            <input
              className='input'
              data-testid='newtoken-group'
              value={draft.group}
              onChange={(e) =>
                setDraft((d) => ({ ...d, group: e.target.value }))
              }
              placeholder='default'
            />
          </label>
        </div>
      </>
    );
  }

  // step === 2 — quota limits
  return (
    <>
      <div className='lbl'>
        {tr('console.flows.step_of', 'step {{step}} of {{total}}', {
          step: 2,
          total: 3,
        })}{' '}
        · {tr('console.flows.step_limits', 'limits')}
      </div>
      <h1 className='display' style={{ fontSize: 28, margin: '4px 0 22px' }}>
        {tr('console.flows.set_limits', 'Set limits')}
      </h1>
      <div className='panel' style={{ padding: 22, display: 'grid', gap: 16 }}>
        <label style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <input
            type='checkbox'
            data-testid='newtoken-unlimited'
            checked={draft.unlimited_quota}
            onChange={(e) =>
              setDraft((d) => ({ ...d, unlimited_quota: e.target.checked }))
            }
          />
          <span className='lbl'>
            {tr('console.flows.unlimited_quota', 'unlimited quota')}
          </span>
        </label>
        {!draft.unlimited_quota && (
          <label style={{ display: 'grid', gap: 6 }}>
            <span className='lbl'>
              {tr('console.flows.remain_quota', 'remain quota (tokens)')}
            </span>
            <input
              type='number'
              className='input'
              data-testid='newtoken-quota'
              min={1}
              value={draft.remain_quota}
              onChange={(e) =>
                setDraft((d) => ({
                  ...d,
                  remain_quota: parseInt(e.target.value, 10) || 0,
                }))
              }
            />
          </label>
        )}
        <label style={{ display: 'grid', gap: 6 }}>
          <span className='lbl'>
            {tr('console.flows.expires_at', 'expires at (leave blank = never)')}
          </span>
          <input
            type='date'
            className='input'
            data-testid='newtoken-expires'
            value={draft.expires_at}
            onChange={(e) =>
              setDraft((d) => ({ ...d, expires_at: e.target.value }))
            }
          />
        </label>
      </div>
    </>
  );
};

const IncidentScreen = () => {
  // Own hook — sub-component defined in the same file.
  const { t: tr } = useTranslation();
  const events = [
    [
      '14:01:55',
      tr('console.flows.evt_detected', 'detected'),
      tr(
        'console.flows.evt_detected_desc',
        'success rate dropped to 0% · auto-detected',
      ),
      'err',
    ],
    [
      '14:02:11',
      tr('console.flows.evt_failover', 'failover engaged'),
      tr(
        'console.flows.evt_failover_desc',
        'traffic routed to openai/main · auto',
      ),
      'info',
    ],
    [
      '14:03:00',
      tr('console.flows.evt_paged', 'paged on-call'),
      tr('console.flows.evt_paged_desc', 'andy@ acknowledged · 14:03:42'),
      'info',
    ],
    [
      '14:05:18',
      tr('console.flows.evt_investigation', 'investigation'),
      tr(
        'console.flows.evt_investigation_desc',
        'Andy · checking upstream status page',
      ),
      'ink',
    ],
    [
      '14:08:30',
      tr('console.flows.evt_mitigation', 'mitigation'),
      tr(
        'console.flows.evt_mitigation_desc',
        'rotated 2 stale keys · waiting for confirmation',
      ),
      'warn',
    ],
    [
      '14:13:45',
      tr('console.flows.evt_monitoring', 'monitoring'),
      tr(
        'console.flows.evt_monitoring_desc',
        'success rate climbing · 38% and rising',
      ),
      'warn',
    ],
  ];
  return (
    <div style={{ padding: '24px 32px' }}>
      <div className='lbl'>
        {tr('console.flows.incident_label', 'incident')} · INC-2026-0512
      </div>
      <h1 className='display' style={{ fontSize: 28, margin: '4px 0 4px' }}>
        {tr('console.flows.incident_title', 'openai/backup · totally down')}
      </h1>
      <div className='muted' style={{ marginBottom: 22 }}>
        {tr(
          'console.flows.incident_sub',
          'started 14:01:55 · 12m ago · sev1 · 6 tenants affected',
        )}
      </div>

      <div
        style={{ display: 'grid', gridTemplateColumns: '1fr 320px', gap: 18 }}
      >
        <div>
          <div className='panel'>
            <div
              style={{
                padding: '14px 18px',
                borderBottom: '1px solid var(--hf-rule)',
                display: 'flex',
                alignItems: 'center',
                gap: 8,
              }}
            >
              <span className='dot err' />{' '}
              <span className='strong'>
                {tr('console.flows.timeline', 'timeline')}
              </span>
              <span style={{ flex: 1 }} />
              <button
                type='button'
                className='btn sm'
                disabled
                title={tr(
                  'console.flows.incident_deferred_tip',
                  'incident workflow requires alerting infra (alertmanager + on-call rotations) — deferred to v3',
                )}
                data-testid='incident-post-update-btn'
              >
                {tr('console.flows.post_update', 'post update')}
              </button>
            </div>
            {events.map((e, i) => (
              <div
                key={i}
                style={{
                  display: 'grid',
                  gridTemplateColumns: '80px 110px 1fr',
                  padding: '12px 18px',
                  borderBottom:
                    i < events.length - 1 ? '1px solid var(--hf-rule)' : 0,
                  gap: 14,
                  alignItems: 'flex-start',
                }}
              >
                <span className='mono muted' style={{ fontSize: 11 }}>
                  {e[0]}
                </span>
                <span className={'tag ' + (e[3] === 'ink' ? '' : e[3])}>
                  {e[1]}
                </span>
                <span style={{ fontSize: 12, lineHeight: 1.5 }}>{e[2]}</span>
              </div>
            ))}
          </div>

          <div className='panel' style={{ marginTop: 14, padding: 18 }}>
            <div className='lbl'>
              {tr(
                'console.flows.success_rate_label',
                'success rate · last 15min',
              )}
            </div>
            <svg
              viewBox='0 0 600 100'
              style={{ width: '100%', height: 100, marginTop: 10 }}
            >
              <line
                x1='0'
                y1='20'
                x2='600'
                y2='20'
                stroke='var(--hf-rule)'
                strokeDasharray='2 4'
              />
              <line
                x1='0'
                y1='50'
                x2='600'
                y2='50'
                stroke='var(--hf-rule)'
                strokeDasharray='2 4'
              />
              <line
                x1='0'
                y1='80'
                x2='600'
                y2='80'
                stroke='var(--hf-rule)'
                strokeDasharray='2 4'
              />
              <polyline
                fill='none'
                stroke='var(--hf-err)'
                strokeWidth='2'
                points='0,15 30,15 50,15 70,15 100,90 130,95 160,98 190,95 220,90 250,82 280,70 310,55 340,42 370,32 400,25 430,22 460,20 490,18 530,16 560,15 600,15'
              />
              <text
                x='0'
                y='14'
                fontSize='10'
                fontFamily='var(--hf-mono)'
                fill='var(--hf-ink-3)'
              >
                100%
              </text>
              <text
                x='0'
                y='98'
                fontSize='10'
                fontFamily='var(--hf-mono)'
                fill='var(--hf-ink-3)'
              >
                0%
              </text>
            </svg>
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div className='panel' style={{ padding: 16 }}>
            <div className='lbl'>{tr('console.flows.impact', 'impact')}</div>
            <div
              className='display'
              style={{ fontSize: 28, marginTop: 4, color: 'var(--hf-err)' }}
            >
              1,420
            </div>
            <div className='muted' style={{ fontSize: 11 }}>
              {tr(
                'console.flows.impact_detail',
                'requests rerouted · $42.80 est revenue saved',
              )}
            </div>
          </div>
          <div className='panel' style={{ padding: 16 }}>
            <div className='lbl'>
              {tr('console.flows.tenants_label', 'tenants')} · 6
            </div>
            <div
              style={{
                display: 'flex',
                flexWrap: 'wrap',
                gap: 4,
                marginTop: 8,
              }}
            >
              {[
                'acme',
                'contoso',
                'globex',
                'initech',
                'foobar',
                'umbrella',
              ].map((t) => (
                <span key={t} className='pill' style={{ fontSize: 10 }}>
                  {t}
                </span>
              ))}
            </div>
          </div>
          <div className='panel' style={{ padding: 16 }}>
            <div className='lbl'>{tr('console.flows.on_call', 'on-call')}</div>
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 10,
                marginTop: 8,
              }}
            >
              <div
                style={{
                  width: 28,
                  height: 28,
                  background: 'var(--hf-accent)',
                  color: '#fff',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  fontFamily: 'var(--hf-display)',
                  fontWeight: 600,
                }}
              >
                A
              </div>
              <div>
                <div className='strong' style={{ fontSize: 12 }}>
                  Andy Liu
                </div>
                <div className='faint mono' style={{ fontSize: 10 }}>
                  {tr('console.flows.ack_time', 'ack 14:03:42')}
                </div>
              </div>
            </div>
          </div>
          <div className='panel' style={{ padding: 16 }}>
            <div className='lbl'>{tr('console.flows.actions', 'actions')}</div>
            <div
              style={{
                display: 'flex',
                flexDirection: 'column',
                gap: 6,
                marginTop: 10,
              }}
            >
              <button
                type='button'
                className='btn'
                disabled
                title={tr(
                  'console.flows.incident_deferred_tip',
                  'incident workflow requires alerting infra (alertmanager + on-call rotations) — deferred to v3',
                )}
                data-testid='incident-silence-btn'
              >
                {tr('console.flows.silence_alert', 'silence alert · 30m')}
              </button>
              <button
                type='button'
                className='btn'
                disabled
                title={tr(
                  'console.flows.incident_deferred_tip',
                  'incident workflow requires alerting infra (alertmanager + on-call rotations) — deferred to v3',
                )}
                data-testid='incident-status-update-btn'
              >
                {tr('console.flows.post_status_update', 'post status update')}
              </button>
              {/* "disable channel" navigates to Channel page — no incident context available here */}
              <a
                href='/console/v2/channel'
                className='btn'
                data-testid='incident-disable-channel-btn'
                title={tr(
                  'console.flows.goto_channels_tip',
                  'go to Channels to disable',
                )}
                style={{ textDecoration: 'none', textAlign: 'center' }}
              >
                {tr('console.flows.disable_channel', 'disable channel ↗')}
              </a>
              <button
                type='button'
                className='btn primary'
                disabled
                title={tr(
                  'console.flows.incident_deferred_tip',
                  'incident workflow requires alerting infra (alertmanager + on-call rotations) — deferred to v3',
                )}
                data-testid='incident-resolve-btn'
              >
                {tr('console.flows.resolve_incident', 'resolve incident')}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};

const RetryScreen = () => {
  // Own hook — sub-component defined in the same file.
  const { t: tr } = useTranslation();
  return (
    <div style={{ padding: '32px 40px' }}>
      <div className='lbl'>
        {tr('console.flows.retry_label', 'request flow visualizer')}
      </div>
      <h1 className='display' style={{ fontSize: 28, margin: '4px 0 4px' }}>
        req_1f4a...e90c
      </h1>
      <div className='muted' style={{ marginBottom: 28 }}>
        {tr(
          'console.flows.retry_sub',
          '3 attempts · ended successfully · 4.2s total',
        )}
      </div>

      <div className='panel' style={{ padding: 28 }}>
        <svg viewBox='0 0 800 280' style={{ width: '100%', height: 320 }}>
          <defs>
            <marker
              id='arr'
              viewBox='0 0 8 8'
              refX='6'
              refY='4'
              markerWidth='6'
              markerHeight='6'
              orient='auto'
            >
              <path d='M0,0 L8,4 L0,8 Z' fill='var(--hf-ink-3)' />
            </marker>
          </defs>
          <g fontFamily='var(--hf-mono)' fontSize='11'>
            <rect
              x='20'
              y='120'
              width='100'
              height='40'
              fill='var(--hf-paper)'
              stroke='var(--hf-rule-strong)'
            />
            <text
              x='70'
              y='138'
              textAnchor='middle'
              fill='var(--hf-ink)'
              fontWeight='500'
            >
              {tr('console.flows.svg_client', 'client')}
            </text>
            <text
              x='70'
              y='152'
              textAnchor='middle'
              fill='var(--hf-ink-3)'
              fontSize='9'
            >
              acme · prod
            </text>

            <rect
              x='180'
              y='120'
              width='100'
              height='40'
              fill='var(--hf-paper)'
              stroke='var(--hf-rule-strong)'
            />
            <text
              x='230'
              y='138'
              textAnchor='middle'
              fill='var(--hf-ink)'
              fontWeight='500'
            >
              {tr('console.flows.svg_gateway', 'lurus gateway')}
            </text>
            <text
              x='230'
              y='152'
              textAnchor='middle'
              fill='var(--hf-ink-3)'
              fontSize='9'
            >
              {tr('console.flows.svg_router', 'router')}
            </text>

            <rect
              x='360'
              y='20'
              width='160'
              height='60'
              fill='rgba(238,111,94,0.1)'
              stroke='var(--hf-err)'
            />
            <text
              x='440'
              y='40'
              textAnchor='middle'
              fill='var(--hf-err)'
              fontWeight='500'
            >
              {tr('console.flows.attempt', 'attempt')} 1 · 504
            </text>
            <text
              x='440'
              y='58'
              textAnchor='middle'
              fill='var(--hf-ink-3)'
              fontSize='9'
            >
              openai/main · 2.1s
            </text>
            <text
              x='440'
              y='72'
              textAnchor='middle'
              fill='var(--hf-ink-3)'
              fontSize='9'
            >
              upstream_timeout
            </text>

            <rect
              x='360'
              y='120'
              width='160'
              height='60'
              fill='rgba(224,160,64,0.1)'
              stroke='var(--hf-warn)'
            />
            <text
              x='440'
              y='140'
              textAnchor='middle'
              fill='var(--hf-warn)'
              fontWeight='500'
            >
              {tr('console.flows.attempt', 'attempt')} 2 · 429
            </text>
            <text
              x='440'
              y='158'
              textAnchor='middle'
              fill='var(--hf-ink-3)'
              fontSize='9'
            >
              openai/backup · 0.4s
            </text>
            <text
              x='440'
              y='172'
              textAnchor='middle'
              fill='var(--hf-ink-3)'
              fontSize='9'
            >
              {tr('console.flows.backoff', 'rate_limit · backoff 1s')}
            </text>

            <rect
              x='360'
              y='220'
              width='160'
              height='60'
              fill='rgba(90,204,146,0.1)'
              stroke='var(--hf-ok)'
            />
            <text
              x='440'
              y='240'
              textAnchor='middle'
              fill='var(--hf-ok)'
              fontWeight='500'
            >
              {tr('console.flows.attempt', 'attempt')} 3 · 200
            </text>
            <text
              x='440'
              y='258'
              textAnchor='middle'
              fill='var(--hf-ink-3)'
              fontSize='9'
            >
              azure/eu · 1.6s
            </text>
            <text
              x='440'
              y='272'
              textAnchor='middle'
              fill='var(--hf-ink-3)'
              fontSize='9'
            >
              {tr('console.flows.tokens_cost', '847 tokens · $0.0042')}
            </text>

            <rect
              x='600'
              y='220'
              width='120'
              height='60'
              fill='rgba(90,204,146,0.1)'
              stroke='var(--hf-ok)'
            />
            <text
              x='660'
              y='240'
              textAnchor='middle'
              fill='var(--hf-ok)'
              fontWeight='500'
            >
              {tr('console.flows.delivered', 'delivered')}
            </text>
            <text
              x='660'
              y='258'
              textAnchor='middle'
              fill='var(--hf-ink-3)'
              fontSize='9'
            >
              {tr('console.flows.to_client', 'to client')}
            </text>
            <text
              x='660'
              y='272'
              textAnchor='middle'
              fill='var(--hf-ink-3)'
              fontSize='9'
            >
              {tr('console.flows.total_time', 'total 4.2s')}
            </text>
          </g>

          <g
            stroke='var(--hf-ink-3)'
            strokeWidth='1.2'
            fill='none'
            markerEnd='url(#arr)'
          >
            <line x1='120' y1='140' x2='180' y2='140' />
            <line x1='280' y1='135' x2='360' y2='50' />
            <line
              x1='280'
              y1='140'
              x2='360'
              y2='150'
              stroke='var(--hf-rule-strong)'
              strokeDasharray='3 3'
            />
            <line
              x1='280'
              y1='145'
              x2='360'
              y2='250'
              stroke='var(--hf-rule-strong)'
              strokeDasharray='3 3'
            />
            <line
              x1='520'
              y1='50'
              x2='280'
              y2='142'
              stroke='var(--hf-err)'
              strokeDasharray='2 3'
            />
            <line
              x1='520'
              y1='150'
              x2='280'
              y2='148'
              stroke='var(--hf-warn)'
              strokeDasharray='2 3'
            />
            <line x1='520' y1='250' x2='600' y2='250' stroke='var(--hf-ok)' />
            <line x1='600' y1='245' x2='120' y2='160' stroke='var(--hf-ok)' />
          </g>
        </svg>
      </div>

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(4, 1fr)',
          gap: 14,
          marginTop: 18,
        }}
      >
        {[
          [
            tr('console.flows.stat_attempts', 'attempts'),
            '3',
            'var(--hf-accent)',
          ],
          [tr('console.flows.stat_retries', 'retries'), '2', 'var(--hf-warn)'],
          [
            tr('console.flows.stat_final', 'final'),
            tr('console.flows.final_ok', '200 ok'),
            'var(--hf-ok)',
          ],
          [tr('console.flows.stat_cost', 'cost'), '$0.0042', 'var(--hf-ink)'],
        ].map(([l, v, c], i) => (
          <div key={i} className='panel' style={{ padding: 14 }}>
            <div className='lbl'>{l}</div>
            <div
              className='display'
              style={{ fontSize: 22, color: c, marginTop: 2 }}
            >
              {v}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
};

const HFFlows = () => {
  // Aliased to `tr` per the v2 console convention.
  const { t: tr } = useTranslation();
  const [flow, setFlow] = useState('newChannel');
  const [step, setStep] = useState(1);
  const meta = FLOWS.find((f) => f[0] === flow);
  const tenantSlug = useTenantSlug();

  // newToken wizard state
  const [draft, setDraft, clearDraft] = useFormDraft(
    TOKEN_DRAFT_KEY,
    TOKEN_DRAFT_INIT,
  );
  const [confirmVisible, setConfirmVisible] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [createdKey, setCreatedKey] = useState(null);

  const handleSubmit = useCallback(async () => {
    if (submitting) return;
    setSubmitting(true);
    try {
      const payload = {
        name: draft.name,
        group: draft.group || 'default',
        unlimited_quota: draft.unlimited_quota,
        remain_quota: draft.unlimited_quota ? 0 : draft.remain_quota,
        expired_time: draft.expires_at
          ? Math.floor(new Date(draft.expires_at).getTime() / 1000)
          : -1,
      };
      const res = await API.post(`/api/v2/${tenantSlug}/tokens`, payload);
      const key = res?.data?.data?.key ?? res?.data?.key;
      setCreatedKey(key);
      clearDraft();
      setConfirmVisible(false);
    } catch (err) {
      const msg =
        err?.response?.data?.message ??
        err?.message ??
        tr('console.flows.create_failed', 'Failed to create token');
      showError(msg);
    } finally {
      setSubmitting(false);
    }
  }, [draft, tenantSlug, submitting, clearDraft, tr]);

  // When user switches flow or step, keep step in bounds.
  const maxStep = meta[2];
  // For newToken, "next" on step 2 goes to review (step 3). The review step
  // handles its own submit — nav buttons are hidden once createdKey is set.
  const showNav =
    flow !== 'incident' &&
    flow !== 'retry' &&
    !(flow === 'newToken' && step === maxStep);

  return (
    <HFShell
      active='channels'
      crumbs={[
        tr('console.flows.crumb', 'flows'),
        tr(`console.flows.flow_${meta[0]}`, meta[1]),
      ]}
      actions={
        showNav ? (
          <>
            <button
              type='button'
              className='btn'
              data-testid='flows-back'
              onClick={() => setStep(Math.max(1, step - 1))}
            >
              {tr('console.flows.back', '← back')}
            </button>
            <button
              type='button'
              className='btn primary'
              data-testid='flows-next'
              onClick={() => setStep(Math.min(maxStep, step + 1))}
            >
              {step === maxStep
                ? tr('console.flows.finish', 'finish')
                : tr('console.flows.next', 'next →')}
            </button>
          </>
        ) : null
      }
    >
      {flow !== 'newToken' && (
        <WIPBanner
          reason={tr(
            'console.flows.wip_static',
            'Flow wizards are static step previews — wired in v3.',
          )}
          todo='newChannel/incident/retry wired in v3.'
        />
      )}
      <div
        style={{
          display: 'flex',
          gap: 0,
          padding: '14px 24px',
          borderBottom: '1px solid var(--hf-rule)',
          background: 'var(--hf-paper)',
        }}
      >
        {FLOWS.map(([k, l]) => (
          <button
            key={k}
            type='button'
            data-testid={`flows-tab-${k}`}
            onClick={() => {
              setFlow(k);
              setStep(1);
              if (k === 'newToken') setCreatedKey(null);
            }}
            style={{
              padding: '6px 14px',
              border: 0,
              background: 'transparent',
              cursor: 'pointer',
              fontFamily: 'var(--hf-mono)',
              fontSize: 11,
              color: flow === k ? 'var(--hf-ink)' : 'var(--hf-ink-3)',
              borderBottom:
                flow === k
                  ? '2px solid var(--hf-accent)'
                  : '2px solid transparent',
              marginBottom: -15,
              paddingBottom: 17,
            }}
          >
            {tr(`console.flows.flow_${k}`, l)}
          </button>
        ))}
      </div>

      {flow === 'newChannel' && (
        <>
          <WIPBanner
            reason={tr(
              'console.flows.wip_new_channel',
              'New Channel wizard wired in v3.',
            )}
            todo='Requires credential validation + connection-test + model-discovery.'
            data-testid='wip-newChannel'
          />
          <Stepper
            steps={[
              tr('console.flows.step_vendor', 'vendor'),
              tr('console.flows.step_credentials', 'credentials'),
              tr('console.flows.step_models', 'models'),
              tr('console.flows.step_review', 'review'),
            ]}
            cur={step}
          />
          <div style={{ padding: '32px 40px' }}>
            <NewChannelStep step={step} />
          </div>
        </>
      )}
      {flow === 'newToken' && (
        <>
          <Stepper
            steps={[
              tr('console.flows.step_scope', 'name & scope'),
              tr('console.flows.step_limits', 'limits'),
              tr('console.flows.step_review', 'review'),
            ]}
            cur={step}
          />
          <div style={{ padding: '32px 40px' }}>
            <NewTokenStep
              step={step}
              draft={draft}
              setDraft={setDraft}
              createdKey={createdKey}
              onCopyKey={() =>
                navigator.clipboard.writeText(createdKey).catch(() => undefined)
              }
              onConfirmOpen={() => setConfirmVisible(true)}
              confirmVisible={confirmVisible}
              onConfirmCancel={() => setConfirmVisible(false)}
              onSubmit={handleSubmit}
            />
          </div>
        </>
      )}
      {flow === 'incident' && (
        <>
          <WIPBanner
            reason={tr(
              'console.flows.wip_incident',
              'Incident Response wizard wired in v3.',
            )}
            todo='Event-response automation — deferred.'
            data-testid='wip-incident'
          />
          <IncidentScreen />
        </>
      )}
      {flow === 'retry' && (
        <>
          <WIPBanner
            reason={tr(
              'console.flows.wip_retry',
              'Retry Chain Visualizer wired in v3.',
            )}
            todo='Failed-retry orchestration — deferred.'
            data-testid='wip-retry'
          />
          <RetryScreen />
        </>
      )}
    </HFShell>
  );
};

export default HFFlows;
