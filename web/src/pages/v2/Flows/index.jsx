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
import ConfirmDialog from '../../../components/common/ConfirmDialog';
import { useFormDraft } from '../../../hooks/common/useFormDraft';
import { API, showError } from '../../../helpers';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';
import { CHANNEL_PRESETS } from '../../../constants/channel.constants';

/* HiFi 13 — Flows: multi-step wizards. Ported from hifi/hf13-flows.jsx, then
   wired to the real channel/token backends (cycle 10 L4). The original port
   also carried an "Incident Response" and a "Retry Chain Visualizer" tab —
   both static mockups with no backend and no plan to build one, so this pass
   removed them rather than ship them behind a permanent warning. */

// Second column is the English fallback; display labels resolve through
// tr(`console.flows.flow_${key}`, fallback) at render time.
const FLOWS = [
  ['newChannel', 'New Channel', 4],
  ['newToken', 'New Token', 3],
];

// Vendor picker choices for NewChannelStep step 1. `type` is the numeric
// repo.Channel.Type value the backend persists — same catalogue as
// constants/channel.constants.js's CHANNEL_OPTIONS, narrowed to the vendors
// this wizard surfaces as one-click presets. Selecting a vendor sets the
// channel's type and, when CHANNEL_PRESETS carries one, prefills base_url —
// entity/channel.go derives a per-type default upstream host when base_url
// is left empty, so an unlisted preset (base_url: '') is not a gap, it means
// "use the vendor's default host".
const VENDOR_CHOICES = [
  { key: 'openai', type: 1, label: 'OpenAI' },
  { key: 'anthropic', type: 14, label: 'Anthropic' },
  { key: 'vertex', type: 41, label: 'Google Vertex' },
  { key: 'azure', type: 3, label: 'Azure' },
  { key: 'bedrock', type: 33, label: 'AWS Bedrock' },
  { key: 'zhipu', type: 26, label: 'Zhipu' },
  { key: 'siliconflow', type: 40, label: 'SiliconCloud' },
  { key: 'custom', type: 8, label: null },
];

// describeChannelWriteError turns an axios rejection from a channel write
// into a message a tenant admin can act on. The 403 shape checked here
// ({success:false, message:'insufficient permission',
// error_code:'PERMISSION_DENIED'}) is what enforceChannelSensitiveWriteDecided
// writes (internal/adapter/handler/channel_sensitive_write.go) — proved
// against the real router by TestCreateChannelV2_NonRootAdminWithoutGrant403
// (internal/adapter/handler/channel_sensitive_write_test.go). A channel
// create always populates `key`, which is one of the fields that gate
// covers, so a non-root tenant admin without the channel:sensitive_write
// grant hits this on every create — the bare "insufficient permission" body
// does not say what the caller is missing or what to do about it.
function describeChannelWriteError(err, tr) {
  const data = err?.response?.data;
  if (
    err?.response?.status === 403 &&
    data?.error_code === 'PERMISSION_DENIED'
  ) {
    return tr(
      'console.flows.permission_denied_sensitive_write',
      'Missing the channel:sensitive_write grant — creating a channel sets its key, so it needs this grant even for an otherwise ordinary tenant admin. Ask a root admin to grant channel:sensitive_write, or have them create the channel for you.',
    );
  }
  return (
    data?.message ??
    err?.message ??
    tr('console.flows.channel_create_failed', 'Failed to reach the channel API')
  );
}

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

const inputStyle = {
  fontFamily: 'var(--hf-mono)',
  fontSize: 12,
};

// NewChannelStep is fully controlled by HFFlows — it owns no state of its
// own (mirrors NewTokenStep). Three real backend calls drive it end to end:
// POST …/channels (create, step 2 → 3), GET …/channels/:id/upstream-models
// (discovery, step 3) and POST …/channels/:id/test (connection test, step
// 4). Test and discovery both need a persisted channel row (they read the
// stored key server-side), which is why creation happens at the step 2 → 3
// transition rather than at the end of the wizard.
const NewChannelStep = ({
  step,
  form,
  onFieldChange,
  onSelectVendor,
  creating,
  createError,
  onCreate,
  createdChannel,
  discovery,
  selectedNewModels,
  onToggleNewModel,
  onDiscoverModels,
  applyingModels,
  onApplyModels,
  onSkipModels,
  testing,
  testResult,
  onTestChannel,
  onFinish,
}) => {
  // Own hook — sub-component defined in this file.
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
          {VENDOR_CHOICES.map((v, i) => {
            const label =
              v.label ?? tr('console.flows.vendor_custom', 'custom');
            const selected = Number(form.type) === v.type;
            return (
              <div
                key={v.key}
                className='panel'
                role='button'
                tabIndex={0}
                data-testid={`newchannel-vendor-${v.key}`}
                onClick={() => onSelectVendor(v)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') onSelectVendor(v);
                }}
                style={{
                  padding: 16,
                  cursor: 'pointer',
                  border: selected
                    ? '2px solid var(--hf-accent)'
                    : '1px solid var(--hf-rule)',
                }}
              >
                <div className='display' style={{ fontSize: 15 }}>
                  {label}
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
                      ? tr(
                          'console.flows.vendor_hint_native',
                          'native protocol',
                        )
                      : tr(
                          'console.flows.vendor_hint_custom',
                          'OAI-compatible URL',
                        )}
                </div>
              </div>
            );
          })}
        </div>
      </div>
    );
  }

  if (step === 2) {
    const locked = creating || !!createdChannel;
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
            'sent once on create · never echoed back',
          )}
        </div>
        <div
          className='panel'
          style={{ padding: 22, display: 'grid', gap: 16 }}
        >
          <label style={{ display: 'grid', gap: 6 }}>
            <span className='lbl'>
              {tr('console.flows.field_channel_name', 'channel name')}
            </span>
            <input
              className='input'
              style={inputStyle}
              data-testid='newchannel-name'
              value={form.name}
              disabled={locked}
              onChange={(e) => onFieldChange('name', e.target.value)}
              placeholder='openai/main-2'
            />
            <span className='faint mono' style={{ fontSize: 10 }}>
              {tr('console.flows.hint_identifier', 'identifier · alphanumeric')}
            </span>
          </label>
          <label style={{ display: 'grid', gap: 6 }}>
            <span className='lbl'>
              {tr('console.flows.field_base_url', 'base url')}
            </span>
            <input
              className='input'
              style={inputStyle}
              data-testid='newchannel-baseurl'
              value={form.baseURL}
              disabled={locked}
              onChange={(e) => onFieldChange('baseURL', e.target.value)}
              placeholder='https://api.openai.com/v1'
            />
            <span className='faint mono' style={{ fontSize: 10 }}>
              {tr('console.flows.hint_override', 'override for proxies')}
            </span>
          </label>
          <label style={{ display: 'grid', gap: 6 }}>
            <span className='lbl'>
              {tr('console.flows.field_api_keys', 'api keys')}
            </span>
            <textarea
              className='input'
              style={{ ...inputStyle, minHeight: 56, resize: 'vertical' }}
              data-testid='newchannel-key'
              value={form.key}
              disabled={locked}
              onChange={(e) => onFieldChange('key', e.target.value)}
              placeholder='sk-...'
            />
            <span className='faint mono' style={{ fontSize: 10 }}>
              {tr(
                'console.flows.hint_keys_rr',
                'newline-separated · round-robin across keys',
              )}
            </span>
          </label>
          <label style={{ display: 'grid', gap: 6 }}>
            <span className='lbl'>
              {tr('console.flows.field_models', 'models')}
            </span>
            <input
              className='input'
              style={inputStyle}
              data-testid='newchannel-models'
              value={form.models}
              disabled={locked}
              onChange={(e) => onFieldChange('models', e.target.value)}
              placeholder='gpt-4o,gpt-4o-mini'
            />
            <span className='faint mono' style={{ fontSize: 10 }}>
              {tr(
                'console.flows.hint_models_required',
                'comma-separated · at least one required',
              )}
            </span>
          </label>
          <label style={{ display: 'grid', gap: 6 }}>
            <span className='lbl'>
              {tr('console.flows.field_org_id', 'organization id')}
            </span>
            <input
              className='input'
              style={inputStyle}
              data-testid='newchannel-orgid'
              value={form.orgId}
              disabled={locked}
              onChange={(e) => onFieldChange('orgId', e.target.value)}
              placeholder='org-acme-prod'
            />
            <span className='faint mono' style={{ fontSize: 10 }}>
              {tr('console.flows.hint_optional', 'optional')}
            </span>
          </label>
          {locked && !creating && (
            <span className='faint mono' style={{ fontSize: 10 }}>
              {tr(
                'console.flows.locked_after_create',
                'locked — edit this channel from the Channels page',
              )}
            </span>
          )}
        </div>

        {createError && (
          <div
            className='panel'
            data-testid='newchannel-create-error'
            style={{
              marginTop: 14,
              padding: 12,
              borderLeft: '2px solid var(--hf-err)',
              fontSize: 12,
            }}
          >
            {createError}
          </div>
        )}

        {createdChannel ? (
          <div
            className='panel'
            data-testid='newchannel-created-note'
            style={{
              marginTop: 14,
              padding: 12,
              borderLeft: '2px solid var(--hf-ok)',
              fontSize: 12,
            }}
          >
            {tr(
              'console.flows.channel_ready_note',
              'Channel #{{id}} created and enabled.',
              { id: createdChannel.id },
            )}
          </div>
        ) : (
          <div style={{ marginTop: 16, textAlign: 'right' }}>
            <button
              type='button'
              className='btn primary'
              data-testid='newchannel-create-btn'
              disabled={
                creating ||
                !form.name.trim() ||
                !form.key.trim() ||
                !form.models.trim()
              }
              onClick={onCreate}
            >
              {creating
                ? tr('console.flows.creating_channel', 'creating…')
                : tr('console.flows.create_channel_btn', 'create channel →')}
            </button>
          </div>
        )}
      </div>
    );
  }

  if (step === 3) {
    if (!createdChannel) {
      // Guard only — step 3 is unreachable without a created channel because
      // the top-level "next" button is hidden for newChannel past step 1 and
      // progression to here always goes through onCreate.
      return (
        <div className='muted'>
          {tr(
            'console.flows.channel_required_note',
            'Create the channel in step 2 first.',
          )}
        </div>
      );
    }
    const upstream = discovery.data;
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
            'discover the models the upstream actually serves and add the ones you want',
          )}
        </div>
        <div
          style={{
            display: 'flex',
            gap: 12,
            alignItems: 'center',
            marginBottom: 14,
          }}
        >
          <button
            type='button'
            className='btn'
            data-testid='newchannel-discover-btn'
            disabled={discovery.loading}
            onClick={onDiscoverModels}
          >
            {discovery.loading
              ? tr('console.flows.discovering_models', 'discovering…')
              : tr(
                  'console.flows.discover_models_btn',
                  'discover upstream models →',
                )}
          </button>
          {discovery.error && (
            <span
              data-testid='newchannel-discover-error'
              className='muted'
              style={{ fontSize: 11, color: 'var(--hf-err)' }}
            >
              {discovery.error}
            </span>
          )}
        </div>
        {upstream && (
          <div className='panel'>
            <div className='hf-table-scroll'>
              <table className='t'>
                <thead>
                  <tr>
                    <th></th>
                    <th>{tr('console.flows.th_model', 'model')}</th>
                    <th>{tr('console.flows.th_status', 'status')}</th>
                  </tr>
                </thead>
                <tbody>
                  {upstream.upstream.length === 0 && (
                    <tr>
                      <td colSpan={3} className='muted'>
                        {tr(
                          'console.flows.no_new_models',
                          'upstream returned no models',
                        )}
                      </td>
                    </tr>
                  )}
                  {upstream.upstream.map((m) => {
                    const isNew = (upstream.new ?? []).includes(m);
                    return (
                      <tr key={m}>
                        <td>
                          {isNew && (
                            <input
                              type='checkbox'
                              data-testid={`newchannel-model-${m}`}
                              checked={selectedNewModels.has(m)}
                              onChange={() => onToggleNewModel(m)}
                            />
                          )}
                        </td>
                        <td className='mono strong'>{m}</td>
                        <td>
                          {isNew ? (
                            <span className='tag info'>
                              {tr('console.flows.tag_new', 'new')}
                            </span>
                          ) : (
                            <span className='tag'>
                              {tr('console.flows.tag_configured', 'configured')}
                            </span>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )}
        <div
          style={{
            display: 'flex',
            gap: 10,
            marginTop: 16,
            justifyContent: 'flex-end',
          }}
        >
          <button
            type='button'
            className='btn'
            data-testid='newchannel-skip-models-btn'
            onClick={onSkipModels}
          >
            {tr(
              'console.flows.skip_models_btn',
              'skip · continue without adding →',
            )}
          </button>
          <button
            type='button'
            className='btn primary'
            data-testid='newchannel-apply-models-btn'
            disabled={applyingModels || selectedNewModels.size === 0}
            onClick={onApplyModels}
          >
            {applyingModels
              ? tr('console.flows.applying_models', 'applying…')
              : tr(
                  'console.flows.apply_models_btn',
                  'add selected & continue →',
                )}
          </button>
        </div>
      </div>
    );
  }

  // step 4 — review & test.
  const modelsStr = createdChannel?.models ?? form.models;
  const modelCount = modelsStr
    .split(',')
    .map((m) => m.trim())
    .filter(Boolean).length;
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
        {[
          [
            tr('console.flows.field_channel_name', 'channel name'),
            createdChannel?.name ?? form.name,
          ],
          [
            tr('console.flows.field_base_url', 'base url'),
            form.baseURL ||
              tr('console.flows.base_url_default', 'provider default'),
          ],
          [
            tr('console.flows.review_models_count', 'models configured'),
            String(modelCount),
          ],
        ].map(([l, v], i, arr) => (
          <div
            key={l}
            style={{
              display: 'grid',
              gridTemplateColumns: '180px 1fr',
              padding: '10px 0',
              borderBottom:
                i < arr.length - 1 ? '1px dashed var(--hf-rule)' : 0,
            }}
          >
            <span className='lbl'>{l}</span>
            <span className='strong' style={{ fontSize: 13 }}>
              {v}
            </span>
          </div>
        ))}
      </div>
      <div
        style={{
          display: 'flex',
          gap: 12,
          alignItems: 'center',
          marginTop: 16,
        }}
      >
        <button
          type='button'
          className='btn'
          data-testid='newchannel-test-btn'
          disabled={testing || !createdChannel}
          onClick={onTestChannel}
        >
          {testing
            ? tr('console.flows.testing_channel', 'testing…')
            : tr('console.flows.test_channel_btn', '▶ test channel')}
        </button>
        {testResult && (
          <span
            data-testid='newchannel-test-result'
            className='mono'
            style={{
              fontSize: 11,
              color: testResult.success ? 'var(--hf-ok)' : 'var(--hf-err)',
            }}
          >
            {testResult.success
              ? tr(
                  'console.flows.test_result_success',
                  'reachable · {{ms}}ms',
                  {
                    ms: testResult.latency_ms ?? 0,
                  },
                )
              : tr(
                  'console.flows.test_result_failure',
                  'unreachable: {{error}}',
                  {
                    error: testResult.error ?? '',
                  },
                )}
          </span>
        )}
      </div>
      <div style={{ marginTop: 22, textAlign: 'right' }}>
        <button
          type='button'
          className='btn primary'
          data-testid='newchannel-finish-btn'
          onClick={onFinish}
        >
          {tr('console.flows.finish_channel_btn', 'finish · create another')}
        </button>
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

// The newChannel wizard's draft is plain useState, NOT useFormDraft — unlike
// the token draft above, it carries a real upstream provider api key, and
// useFormDraft persists to localStorage (hooks/common/useFormDraft.js).
// Writing a live credential to localStorage would be a new exposure, so this
// draft never survives a refresh or a step re-mount from elsewhere in the app.
const CHANNEL_DRAFT_INIT = {
  type: 1,
  name: '',
  baseURL: '',
  key: '',
  models: '',
  orgId: '',
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

  // newChannel wizard state — see CHANNEL_DRAFT_INIT for why this is plain
  // useState rather than useFormDraft.
  const [channelForm, setChannelForm] = useState(CHANNEL_DRAFT_INIT);
  const [channelCreating, setChannelCreating] = useState(false);
  const [channelCreateError, setChannelCreateError] = useState(null);
  const [createdChannel, setCreatedChannel] = useState(null);
  const [discovery, setDiscovery] = useState({
    loading: false,
    error: null,
    data: null,
  });
  const [selectedNewModels, setSelectedNewModels] = useState(new Set());
  const [applyingModels, setApplyingModels] = useState(false);
  const [channelTesting, setChannelTesting] = useState(false);
  const [channelTestResult, setChannelTestResult] = useState(null);

  const handleChannelField = useCallback((field, value) => {
    setChannelForm((f) => ({ ...f, [field]: value }));
  }, []);

  const handleSelectVendor = useCallback((vendor) => {
    setChannelForm((f) => ({
      ...f,
      type: vendor.type,
      baseURL: f.baseURL || (CHANNEL_PRESETS[vendor.type]?.base_url ?? ''),
    }));
  }, []);

  const handleCreateChannel = useCallback(async () => {
    if (channelCreating) return;
    setChannelCreating(true);
    setChannelCreateError(null);
    try {
      const payload = {
        name: channelForm.name.trim(),
        type: Number(channelForm.type) || 1,
        base_url: channelForm.baseURL.trim(),
        key: channelForm.key.trim(),
        models: channelForm.models.trim(),
      };
      if (channelForm.orgId.trim()) {
        payload.openai_organization = channelForm.orgId.trim();
      }
      const res = await API.post(`/api/v2/${tenantSlug}/channels`, payload);
      const data = res?.data?.data ?? {};
      setCreatedChannel({
        id: data.id,
        name: data.name ?? payload.name,
        models: payload.models,
      });
      setStep(3);
    } catch (err) {
      setChannelCreateError(describeChannelWriteError(err, tr));
    } finally {
      setChannelCreating(false);
    }
  }, [channelForm, tenantSlug, channelCreating, tr]);

  const handleDiscoverModels = useCallback(async () => {
    if (!createdChannel || discovery.loading) return;
    setDiscovery({ loading: true, error: null, data: null });
    try {
      const res = await API.get(
        `/api/v2/${tenantSlug}/channels/${createdChannel.id}/upstream-models`,
      );
      if (res?.data?.success) {
        const d = res.data.data;
        setDiscovery({ loading: false, error: null, data: d });
        setSelectedNewModels(new Set(d.new ?? []));
      } else {
        setDiscovery({
          loading: false,
          error:
            res?.data?.message ??
            tr(
              'console.flows.discover_failed',
              'Failed to fetch upstream models',
            ),
          data: null,
        });
      }
    } catch (err) {
      setDiscovery({
        loading: false,
        error: describeChannelWriteError(err, tr),
        data: null,
      });
    }
  }, [createdChannel, tenantSlug, discovery.loading, tr]);

  const handleToggleNewModel = useCallback((m) => {
    setSelectedNewModels((prev) => {
      const n = new Set(prev);
      if (n.has(m)) n.delete(m);
      else n.add(m);
      return n;
    });
  }, []);

  const handleApplyModels = useCallback(async () => {
    if (!createdChannel || applyingModels || selectedNewModels.size === 0) {
      return;
    }
    setApplyingModels(true);
    try {
      const currentSet = new Set(
        (createdChannel.models || '')
          .split(',')
          .map((m) => m.trim())
          .filter(Boolean),
      );
      for (const m of selectedNewModels) currentSet.add(m);
      const merged = [...currentSet].join(',');
      const res = await API.put(
        `/api/v2/${tenantSlug}/channels/${createdChannel.id}`,
        { models: merged },
      );
      if (res?.data?.success) {
        setCreatedChannel((c) => ({ ...c, models: merged }));
        setStep(4);
      } else {
        setDiscovery((d) => ({
          ...d,
          error:
            res?.data?.message ??
            tr('console.flows.sync_failed', 'Failed to update models'),
        }));
      }
    } catch (err) {
      setDiscovery((d) => ({
        ...d,
        error: describeChannelWriteError(err, tr),
      }));
    } finally {
      setApplyingModels(false);
    }
  }, [createdChannel, tenantSlug, applyingModels, selectedNewModels, tr]);

  const handleSkipModels = useCallback(() => {
    setStep(4);
  }, []);

  const handleTestChannel = useCallback(async () => {
    if (!createdChannel || channelTesting) return;
    setChannelTesting(true);
    setChannelTestResult(null);
    try {
      const res = await API.post(
        `/api/v2/${tenantSlug}/channels/${createdChannel.id}/test`,
        {},
      );
      setChannelTestResult(res?.data ?? null);
    } catch (err) {
      setChannelTestResult({
        success: false,
        error: describeChannelWriteError(err, tr),
      });
    } finally {
      setChannelTesting(false);
    }
  }, [createdChannel, tenantSlug, channelTesting, tr]);

  const handleFinishChannel = useCallback(() => {
    setChannelForm(CHANNEL_DRAFT_INIT);
    setChannelCreateError(null);
    setCreatedChannel(null);
    setDiscovery({ loading: false, error: null, data: null });
    setSelectedNewModels(new Set());
    setChannelTestResult(null);
    setStep(1);
  }, []);

  // When user switches flow or step, keep step in bounds.
  const maxStep = meta[2];
  // Top-level Back is hidden only on newToken's terminal review/success step
  // (it owns its own confirm/copy actions there). Top-level Next is ALSO
  // hidden for newChannel past step 1 — steps 2-4 progress through the
  // wizard-owned Create / Discover / Test buttons above, each of which is a
  // real async call, not a plain step increment.
  const showBack = !(flow === 'newToken' && step === maxStep);
  const showNext = showBack && !(flow === 'newChannel' && step >= 2);

  return (
    <HFShell
      active='channels'
      crumbs={[
        tr('console.flows.crumb', 'flows'),
        tr(`console.flows.flow_${meta[0]}`, meta[1]),
      ]}
      actions={
        showBack || showNext ? (
          <>
            {showBack && (
              <button
                type='button'
                className='btn'
                data-testid='flows-back'
                onClick={() => setStep(Math.max(1, step - 1))}
              >
                {tr('console.flows.back', '← back')}
              </button>
            )}
            {showNext && (
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
            )}
          </>
        ) : null
      }
    >
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
            <NewChannelStep
              step={step}
              form={channelForm}
              onFieldChange={handleChannelField}
              onSelectVendor={handleSelectVendor}
              creating={channelCreating}
              createError={channelCreateError}
              onCreate={handleCreateChannel}
              createdChannel={createdChannel}
              discovery={discovery}
              selectedNewModels={selectedNewModels}
              onToggleNewModel={handleToggleNewModel}
              onDiscoverModels={handleDiscoverModels}
              applyingModels={applyingModels}
              onApplyModels={handleApplyModels}
              onSkipModels={handleSkipModels}
              testing={channelTesting}
              testResult={channelTestResult}
              onTestChannel={handleTestChannel}
              onFinish={handleFinishChannel}
            />
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
    </HFShell>
  );
};

export default HFFlows;
