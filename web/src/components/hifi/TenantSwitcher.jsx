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
import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import UsageRing from './UsageRing';

/**
 * TenantSwitcher — multi-tenant / mode switcher for the HFShell sidebar.
 *
 * Three modes:
 *   Personal   — individual user scope (blue / hf-info)
 *   Reseller   — admin managing tenant pool (orange / hf-accent)
 *   EndUser    — end-user inside a tenant (green / hf-ok)
 *
 * Usage:
 *   <TenantSwitcher />   — standalone with local demo state
 *
 * When real API data is available, pass props:
 *   mode        {'Personal'|'Reseller'|'EndUser'}
 *   tenantName  {string}
 *   tenants     {Array<{id, name, mode}>}
 *   quota       {{ used: number, total: number, unit: string }}
 *   onSelect    {(tenantId, mode) => void}
 */

const MODES = ['Personal', 'Reseller', 'EndUser'];

const MODE_META = {
  Personal: {
    color: 'var(--hf-info)',
    bg: 'var(--hf-info-bg)',
    glyph: '◉',
  },
  Reseller: {
    color: 'var(--hf-accent)',
    bg: 'var(--hf-accent-bg)',
    glyph: '◈',
  },
  EndUser: {
    color: 'var(--hf-ok)',
    bg: 'var(--hf-ok-bg)',
    glyph: '◎',
  },
};

// Quota visualization is still placeholder until Dashboard quota API lands —
// kept here so the switcher has something to render; not used in HFShell flow.
const DEMO_QUOTA = { used: 0, total: 0, unit: '$' };

// Mode names double as internal identifiers (MODE_META keys, t.mode values) —
// translate only at render time.
const MODE_LABEL_KEYS = {
  Personal: ['mode_personal', 'Personal'],
  Reseller: ['mode_reseller', 'Reseller'],
  EndUser: ['mode_enduser', 'EndUser'],
};
const modeLabel = (tr, m) => {
  const pair = MODE_LABEL_KEYS[m];
  return pair ? tr(`console.component.tenant_switcher.${pair[0]}`, pair[1]) : m;
};

const TenantSwitcher = ({
  mode: modeProp,
  tenantName: tenantNameProp,
  tenants: tenantsProp,
  quota: quotaProp,
  onSelect,
}) => {
  const { t: tr } = useTranslation();
  // No onSelect means there is no server-side tenant/mode switch to wire this
  // to (see the comment above the <TenantSwitcher> call site in HFShell.jsx)
  // — the three mode buttons below render disabled rather than silently
  // mutating local state for nothing.
  const modeSwitchEnabled = typeof onSelect === 'function';
  const [open, setOpen] = useState(false);
  const [mode, setMode] = useState(modeProp ?? 'Personal');
  const [tenantName, setTenantName] = useState(tenantNameProp ?? '');
  // Empty tenants list renders an empty list section — caller (HFShell) is
  // responsible for surfacing a single-tenant fallback before this point.
  const tenants = tenantsProp ?? [];
  const quota = quotaProp ?? DEMO_QUOTA;

  // Sync controlled props into local state when caller updates them
  // (HFShell fetches tenants async — first paint passes empty values).
  useEffect(() => {
    if (tenantNameProp !== undefined) setTenantName(tenantNameProp);
  }, [tenantNameProp]);
  useEffect(() => {
    if (modeProp !== undefined) setMode(modeProp);
  }, [modeProp]);
  const dropRef = useRef(null);

  // Close on outside click.
  useEffect(() => {
    if (!open) return;
    const handler = (e) => {
      if (dropRef.current && !dropRef.current.contains(e.target)) {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [open]);

  const meta = MODE_META[mode] ?? MODE_META.Reseller;

  const handleSelect = (t) => {
    setTenantName(t.name);
    setMode(t.mode);
    setOpen(false);
    onSelect?.(t.id, t.mode);
  };

  const handleModeSelect = (m) => {
    setMode(m);
    setOpen(false);
    onSelect?.(null, m);
  };

  const pct =
    quota.total > 0 ? Math.round((quota.used / quota.total) * 100) : 0;

  return (
    <div
      ref={dropRef}
      style={{ position: 'relative', fontFamily: 'var(--hf-mono)' }}
    >
      {/* ── Trigger button ── */}
      <div
        role='button'
        tabIndex={0}
        aria-haspopup='listbox'
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') setOpen((v) => !v);
          if (e.key === 'Escape') setOpen(false);
        }}
        style={{
          cursor: 'pointer',
          padding: '6px 8px',
          border: '1px solid var(--hf-rule)',
          borderRadius: 2,
          background: 'var(--hf-elev)',
          display: 'flex',
          flexDirection: 'column',
          gap: 4,
        }}
      >
        {/* Mode pill */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 4,
              padding: '1px 7px',
              borderRadius: 999,
              background: meta.bg,
              border: `1px solid ${meta.color}`,
              fontSize: 9,
              textTransform: 'uppercase',
              letterSpacing: '0.1em',
              color: meta.color,
              fontWeight: 600,
            }}
          >
            <span>{meta.glyph}</span>
            {modeLabel(tr, mode)}
          </span>
          <span
            style={{
              marginLeft: 'auto',
              color: 'var(--hf-ink-4)',
              fontSize: 10,
            }}
          >
            {open ? '▴' : '▾'}
          </span>
        </div>

        {/* Tenant name */}
        <div
          style={{
            fontSize: 11,
            color: 'var(--hf-ink)',
            fontWeight: 500,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {tenantName}
        </div>

        {/* Compact quota bar */}
        {quota.total > 0 && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            <div
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                fontSize: 9,
                color: 'var(--hf-ink-4)',
              }}
            >
              <span>
                {tr('console.component.tenant_switcher.quota_lbl', 'quota')}
              </span>
              <span
                style={{
                  color:
                    pct >= 90
                      ? 'var(--hf-err)'
                      : pct >= 70
                        ? 'var(--hf-warn)'
                        : 'var(--hf-ok)',
                }}
              >
                {pct}%
              </span>
            </div>
            <div
              style={{
                height: 3,
                background: 'var(--hf-sunken)',
                borderRadius: 1,
              }}
            >
              <div
                style={{
                  height: '100%',
                  width: `${pct}%`,
                  background:
                    pct >= 90
                      ? 'var(--hf-err)'
                      : pct >= 70
                        ? 'var(--hf-warn)'
                        : 'var(--hf-ok)',
                  borderRadius: 1,
                  transition: 'width 0.4s ease',
                }}
              />
            </div>
          </div>
        )}
      </div>

      {/* ── Dropdown ── */}
      {open && (
        <div
          role='listbox'
          style={{
            position: 'absolute',
            bottom: 'calc(100% + 6px)',
            left: 0,
            right: 0,
            background: 'var(--hf-elev)',
            border: '1px solid var(--hf-rule)',
            // Menu-tier elevation token, not the trigger's own 2px control
            // radius — this is a floating popover, not a form control.
            borderRadius: 'var(--hf-radius-menu)',
            zIndex: 200,
            overflow: 'hidden',
            boxShadow: 'var(--hf-shadow-menu)',
          }}
        >
          {/* Section: tenants */}
          <div
            style={{
              padding: '6px 8px 4px',
              fontSize: 9,
              textTransform: 'uppercase',
              letterSpacing: '0.14em',
              color: 'var(--hf-ink-4)',
              borderBottom: '1px solid var(--hf-rule)',
            }}
          >
            {tr('console.component.tenant_switcher.section_tenants', 'tenants')}
          </div>
          {tenants.map((t) => {
            const tm = MODE_META[t.mode] ?? MODE_META.Personal;
            const isActive = t.name === tenantName;
            return (
              <div
                key={t.id}
                role='option'
                aria-selected={isActive}
                tabIndex={0}
                onClick={() => handleSelect(t)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') handleSelect(t);
                }}
                style={{
                  padding: '7px 10px',
                  cursor: 'pointer',
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  background: isActive ? 'var(--hf-sunken)' : 'transparent',
                  borderLeft: isActive
                    ? `2px solid ${tm.color}`
                    : '2px solid transparent',
                  transition: 'background 0.08s',
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.background = 'var(--hf-paper)';
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.background = isActive
                    ? 'var(--hf-sunken)'
                    : 'transparent';
                }}
              >
                <span style={{ color: tm.color, fontSize: 10 }}>
                  {tm.glyph}
                </span>
                <span style={{ fontSize: 11, color: 'var(--hf-ink)', flex: 1 }}>
                  {t.name}
                </span>
                <span
                  style={{
                    fontSize: 9,
                    textTransform: 'uppercase',
                    letterSpacing: '0.08em',
                    color: tm.color,
                  }}
                >
                  {modeLabel(tr, t.mode)}
                </span>
              </div>
            );
          })}

          {/* Section: switch mode */}
          <div
            style={{
              padding: '6px 8px 4px',
              fontSize: 9,
              textTransform: 'uppercase',
              letterSpacing: '0.14em',
              color: 'var(--hf-ink-4)',
              borderTop: '1px solid var(--hf-rule)',
              borderBottom: '1px solid var(--hf-rule)',
            }}
          >
            {tr(
              'console.component.tenant_switcher.section_switch_mode',
              'switch mode',
            )}
          </div>
          <div style={{ display: 'flex', gap: 0 }}>
            {MODES.map((m) => {
              const mm = MODE_META[m];
              const isActive = m === mode;
              return (
                <button
                  key={m}
                  type='button'
                  role='option'
                  aria-selected={isActive}
                  disabled={!modeSwitchEnabled}
                  title={
                    modeSwitchEnabled
                      ? undefined
                      : tr(
                          'console.shell.mode_switch_unavailable',
                          'Switching mode is not available yet',
                        )
                  }
                  onClick={() => modeSwitchEnabled && handleModeSelect(m)}
                  onKeyDown={(e) => {
                    if (modeSwitchEnabled && e.key === 'Enter') {
                      handleModeSelect(m);
                    }
                  }}
                  style={{
                    flex: 1,
                    padding: '8px 4px',
                    textAlign: 'center',
                    border: 'none',
                    background: isActive ? mm.bg : 'transparent',
                    borderBottom: isActive
                      ? `2px solid ${mm.color}`
                      : '2px solid transparent',
                    transition: 'background 0.08s',
                    fontFamily: 'inherit',
                    cursor: modeSwitchEnabled ? 'pointer' : 'not-allowed',
                    opacity: modeSwitchEnabled ? 1 : 0.5,
                  }}
                  onMouseEnter={(e) => {
                    if (modeSwitchEnabled)
                      e.currentTarget.style.background = mm.bg;
                  }}
                  onMouseLeave={(e) => {
                    if (modeSwitchEnabled) {
                      e.currentTarget.style.background = isActive
                        ? mm.bg
                        : 'transparent';
                    }
                  }}
                >
                  <div style={{ fontSize: 13, color: mm.color }}>
                    {mm.glyph}
                  </div>
                  <div
                    style={{
                      fontSize: 9,
                      textTransform: 'uppercase',
                      letterSpacing: '0.08em',
                      color: isActive ? mm.color : 'var(--hf-ink-3)',
                      marginTop: 2,
                    }}
                  >
                    {modeLabel(tr, m)}
                  </div>
                </button>
              );
            })}
          </div>

          {/* UsageRing summary at bottom */}
          <div
            style={{
              padding: '12px 8px 10px',
              borderTop: '1px solid var(--hf-rule)',
              display: 'flex',
              justifyContent: 'center',
            }}
          >
            <UsageRing
              used={quota.used}
              total={quota.total}
              label={tr(
                'console.component.tenant_switcher.ring_label',
                'quota · month',
              )}
              unit={quota.unit}
              size={72}
            />
          </div>

          {/* Footer */}
          <div
            style={{
              padding: '5px 8px',
              borderTop: '1px solid var(--hf-rule)',
              fontSize: 9,
              color: 'var(--hf-ink-4)',
              textAlign: 'center',
            }}
          >
            {tr(
              'console.component.tenant_switcher.footer_attribution',
              'Built on New API · AGPL-3.0',
            )}
          </div>
        </div>
      )}
    </div>
  );
};

export default TenantSwitcher;
