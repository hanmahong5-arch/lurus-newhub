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
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import HFShell, { NAV_SECTIONS } from '../../../components/hifi/HFShell';
import { API, isAdmin } from '../../../helpers';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

/*
 * HiFi 6 — Cmd-K command palette.
 *
 * Every group here used to be hardcoded demo data: channels "openai/main ·
 * $412.80 / 1h · 99.4% healthy", models "gpt-4o · $2.50 / $10 · 128k ctx",
 * recent requests "req_1f4a…e90c · 504". None of it came from this
 * installation — the numbers were invented on a design canvas in 2026-05 and
 * shipped verbatim, so the palette confidently described channels that do not
 * exist at prices we do not charge. It is now wired to the same endpoints the
 * corresponding pages use.
 *
 * Two consequences, both deliberate:
 *   - The channels group is admin-only, because /channels sits behind
 *     AdminAuth (router/api-v2-router.go:150). A non-admin gets no group at
 *     all rather than an empty one implying they have no channels.
 *   - Actions with no backend are removed rather than disabled. "Set monthly
 *     budget…" had nothing behind it (migration 029 gives projects no budget
 *     column) and "Rotate api key…" had no reachable flow from here.
 */

const MAX_PER_GROUP = 6;

// The models group shows a price, which means joining two endpoints: /models
// is the catalogue (name + vendor) and /pricing carries the ratio or per-call
// price. A model with no pricing row shows its vendor alone — never an
// invented number.
const priceHint = (entry, tr) => {
  if (!entry) return '';
  if (entry.quota_type === 1 && typeof entry.model_price === 'number') {
    return tr('console.palette.per_call_price', '${{price}} / call', {
      price: entry.model_price,
    });
  }
  if (typeof entry.model_ratio === 'number') {
    return tr('console.palette.ratio', 'ratio {{ratio}}', {
      ratio: entry.model_ratio,
    });
  }
  return '';
};

const HFCmdK = () => {
  const { t: tr } = useTranslation();
  const navigate = useNavigate();
  const tenantSlug = useTenantSlug();
  const [open, setOpen] = useState(true);
  const [q, setQ] = useState('');
  const [hover, setHover] = useState(0);
  const admin = isAdmin();

  const [models, setModels] = useState([]);
  const [pricing, setPricing] = useState([]);
  const [tokens, setTokens] = useState([]);
  const [recent, setRecent] = useState([]);
  const [channels, setChannels] = useState([]);
  const [loading, setLoading] = useState(true);

  const fetchAll = useCallback(async () => {
    if (!tenantSlug) return;
    setLoading(true);
    // allSettled: one slow or forbidden source must not blank the whole
    // palette. Each group degrades independently to "not loaded".
    const requests = [
      API.get(`/api/v2/${tenantSlug}/models?limit=50`, {
        skipErrorHandler: true,
      }),
      API.get(`/api/v2/${tenantSlug}/pricing`, { skipErrorHandler: true }),
      API.get(`/api/v2/${tenantSlug}/tokens?p=1&size=${MAX_PER_GROUP}`, {
        skipErrorHandler: true,
      }),
      API.get(`/api/v2/${tenantSlug}/logs?page=1&page_size=${MAX_PER_GROUP}`, {
        skipErrorHandler: true,
      }),
    ];
    if (admin) {
      requests.push(
        API.get(`/api/v2/${tenantSlug}/channels?p=1&size=${MAX_PER_GROUP}`, {
          skipErrorHandler: true,
        }),
      );
    }
    const [mRes, pRes, tRes, lRes, cRes] = await Promise.allSettled(requests);

    const payload = (res) =>
      res?.status === 'fulfilled' && res.value?.data?.success
        ? res.value.data.data
        : null;

    setModels(payload(mRes)?.items ?? []);
    const pricingData = payload(pRes);
    setPricing(Array.isArray(pricingData?.pricing) ? pricingData.pricing : []);
    setTokens(payload(tRes)?.items ?? []);
    setRecent(payload(lRes)?.logs ?? []);
    setChannels(admin ? (payload(cRes)?.items ?? []) : []);
    setLoading(false);
  }, [tenantSlug, admin]);

  useEffect(() => {
    fetchAll();
  }, [fetchAll]);

  const priceByModel = useMemo(() => {
    const map = new Map();
    for (const p of pricing) {
      if (p?.model_name) map.set(p.model_name, p);
    }
    return map;
  }, [pricing]);

  // Groups are built from live state. An empty group is dropped entirely: an
  // empty "channels" heading reads as "you have none", and that is a claim we
  // cannot make when the request may simply have failed.
  const groups = useMemo(() => {
    const out = [];

    out.push({
      key: 'navigate',
      title: tr('console.palette.group_navigate', 'navigate'),
      // Same source and same minRole gating as the rail itself, so the palette
      // cannot offer an admin destination to a user who has no such nav entry.
      rows: NAV_SECTIONS.filter(
        (s) => !s.minRole || (admin ? 10 : 0) >= s.minRole,
      ).flatMap((s) =>
        s.items.map((it) => ({
          label: tr(it.key, it.label),
          hint: it.href,
          href: it.href,
        })),
      ),
    });

    if (models.length) {
      out.push({
        key: 'models',
        title: tr('console.palette.group_models', 'models'),
        rows: models.map((m) => ({
          label: m.model_name,
          hint: [m.vendor, priceHint(priceByModel.get(m.model_name), tr)]
            .filter(Boolean)
            .join(' · '),
          href: '/console/v2/pricing',
        })),
      });
    }

    if (tokens.length) {
      out.push({
        key: 'tokens',
        title: tr('console.palette.group_tokens', 'tokens'),
        rows: tokens.map((t) => ({
          label: t.name,
          hint: t.unlimited_quota
            ? tr('console.palette.token_unlimited', 'unlimited')
            : tr('console.palette.token_remaining', '{{quota}} remaining', {
                quota: t.remain_quota,
              }),
          href: '/console/v2/token',
        })),
      });
    }

    if (recent.length) {
      out.push({
        key: 'recent',
        title: tr('console.palette.group_recent', 'recent requests'),
        rows: recent.map((r) => ({
          label: r.model_name || '—',
          hint: [
            r.total_latency_ms != null ? `${r.total_latency_ms}ms` : null,
            r.token_name || null,
          ]
            .filter(Boolean)
            .join(' · '),
          href: '/console/v2/log',
        })),
      });
    }

    if (admin && channels.length) {
      out.push({
        key: 'channels',
        title: tr('console.palette.group_channels', 'channels'),
        rows: channels.map((ch) => ({
          label: ch.name,
          hint:
            ch.status === 1
              ? tr('console.palette.channel_enabled', 'enabled')
              : tr('console.palette.channel_disabled', 'disabled'),
          href: '/console/v2/channel',
        })),
      });
    }

    out.push({
      key: 'actions',
      title: tr('console.palette.group_actions', 'actions'),
      rows: [
        {
          label: tr('console.palette.action_create_token', 'Create token…'),
          hint: '/console/v2/token',
          href: '/console/v2/token',
        },
      ],
    });

    return out.map((g) => ({ ...g, rows: g.rows.slice(0, MAX_PER_GROUP) }));
  }, [tr, admin, models, priceByModel, tokens, recent, channels]);

  // The search box used to be inert — typing changed nothing. Filtering is
  // client-side over what is already loaded, which is honest about its scope:
  // it searches the palette, not the whole installation.
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    if (!needle) return groups;
    return groups
      .map((g) => ({
        ...g,
        rows: g.rows.filter((r) =>
          `${r.label} ${r.hint}`.toLowerCase().includes(needle),
        ),
      }))
      .filter((g) => g.rows.length > 0);
  }, [groups, q]);

  const resultCount = filtered.reduce((n, g) => n + g.rows.length, 0);

  const go = (row) => {
    if (!row?.href) return;
    setOpen(false);
    navigate(row.href);
  };

  return (
    <HFShell
      active='dashboard'
      crumbs={[
        tr('console.palette.crumb_account', 'my account'),
        tr('console.palette.crumb_palette', 'command palette'),
      ]}
    >
      <div style={{ position: 'relative', height: '100%' }}>
        <div className='hf-page-head'>
          <div>
            <div className='lbl' style={{ marginBottom: 6 }}>
              {tr('console.palette.heading_lbl', 'command palette')}
            </div>
            <h1>{tr('console.palette.heading_plain', 'search anything')}</h1>
            <div className='sub'>
              {tr(
                'console.palette.sub',
                'jump to a page, model, token, channel or recent request',
              )}
            </div>
          </div>
        </div>
        <div style={{ padding: 28, color: 'var(--hf-ink-3)', fontSize: 13 }}>
          <div className='panel-paper' style={{ padding: 22 }}>
            <div className='lbl'>
              {tr('console.palette.kbd_reference', 'keyboard reference')}
            </div>
            {/* Only shortcuts that exist. This list used to advertise
                g d / g l / g c / g t / g p / ⌘N / ⌘. — seven bindings, none of
                them implemented anywhere in the app. ⌘K is real now (HFShell
                registers the listener). */}
            <div
              style={{
                display: 'grid',
                gridTemplateColumns: 'repeat(2, 1fr)',
                gap: '10px 24px',
                marginTop: 10,
                fontSize: 12,
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span className='kbd'>⌘</span>
                <span className='kbd'>K</span>
                <span className='muted'>
                  {tr('console.palette.kbd_open_palette', 'open palette')}
                </span>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span className='kbd'>esc</span>
                <span className='muted'>
                  {tr('console.palette.kbd_close', 'close palette')}
                </span>
              </div>
            </div>
          </div>
        </div>

        {open && (
          <div
            onClick={() => setOpen(false)}
            style={{
              position: 'absolute',
              inset: 0,
              background: 'rgba(10,9,8,0.4)',
              backdropFilter: 'blur(3px)',
              display: 'flex',
              justifyContent: 'center',
              paddingTop: 96,
              zIndex: 50,
            }}
          >
            <div
              onClick={(e) => e.stopPropagation()}
              data-testid='palette'
              style={{
                width: 600,
                maxHeight: 480,
                background: 'var(--hf-elev)',
                border: '1px solid var(--hf-rule-strong)',
                boxShadow: '0 32px 72px rgba(0,0,0,0.28)',
                display: 'flex',
                flexDirection: 'column',
                borderRadius: 4,
              }}
            >
              <div
                style={{
                  padding: 14,
                  borderBottom: '1px solid var(--hf-rule)',
                  display: 'flex',
                  alignItems: 'center',
                  gap: 12,
                }}
              >
                <span className='muted' style={{ fontSize: 16 }}>
                  ⌕
                </span>
                <input
                  autoFocus
                  data-testid='palette-input'
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Escape') setOpen(false);
                  }}
                  placeholder={tr(
                    'console.palette.ph_search',
                    'go to · search models tokens channels…',
                  )}
                  style={{
                    flex: 1,
                    border: 0,
                    outline: 0,
                    background: 'transparent',
                    fontFamily: 'var(--hf-mono)',
                    fontSize: 13,
                    color: 'var(--hf-ink)',
                  }}
                />
                <span className='kbd'>esc</span>
              </div>
              <div style={{ overflow: 'auto', flex: 1 }}>
                {loading && (
                  <div className='muted' style={{ padding: '12px 16px' }}>
                    {tr('console.common.loading', 'loading…')}
                  </div>
                )}
                {!loading && resultCount === 0 && (
                  <div
                    className='muted'
                    data-testid='palette-empty'
                    style={{ padding: '12px 16px' }}
                  >
                    {tr('console.palette.no_results', 'no matches')}
                  </div>
                )}
                {filtered.map((gr, gi) => (
                  <div key={gr.key}>
                    <div className='lbl' style={{ padding: '10px 16px 4px' }}>
                      {gr.title}
                    </div>
                    {gr.rows.map((row, i) => {
                      const idx = gi * 100 + i;
                      const active = hover === idx;
                      return (
                        <div
                          key={`${gr.key}-${i}`}
                          data-testid={`palette-row-${gr.key}`}
                          role='button'
                          tabIndex={0}
                          onMouseEnter={() => setHover(idx)}
                          onClick={() => go(row)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter') go(row);
                          }}
                          style={{
                            display: 'flex',
                            alignItems: 'center',
                            gap: 12,
                            padding: '8px 16px',
                            cursor: 'pointer',
                            background: active
                              ? 'var(--hf-sunken)'
                              : 'transparent',
                            borderLeft: active
                              ? '2px solid var(--hf-accent)'
                              : '2px solid transparent',
                          }}
                        >
                          <span
                            className='strong'
                            style={{ minWidth: 230, fontSize: 13 }}
                          >
                            {row.label}
                          </span>
                          <span
                            className='muted mono'
                            style={{ flex: 1, fontSize: 11 }}
                          >
                            {row.hint}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                ))}
              </div>
              <div
                style={{
                  padding: '8px 14px',
                  borderTop: '1px solid var(--hf-rule)',
                  display: 'flex',
                  gap: 14,
                  fontFamily: 'var(--hf-mono)',
                  fontSize: 10,
                  color: 'var(--hf-ink-3)',
                }}
              >
                <span>
                  <span className='kbd'>↵</span>{' '}
                  {tr('console.palette.footer_open', 'open')}
                </span>
                <span style={{ flex: 1 }} />
                {/* A real count of what is on screen. It used to read a
                    constant 72 regardless of content. */}
                <span data-testid='palette-count'>
                  {tr('console.palette.footer_results', '{{count}} results', {
                    count: resultCount,
                  })}
                </span>
              </div>
            </div>
          </div>
        )}

        {!open && (
          <button
            type='button'
            className='btn primary'
            onClick={() => setOpen(true)}
            style={{
              position: 'absolute',
              bottom: 24,
              right: 24,
              height: 36,
              padding: '0 16px',
            }}
          >
            {tr('console.palette.open_palette_btn', '⌘K open palette')}
          </button>
        )}
      </div>
    </HFShell>
  );
};

export default HFCmdK;
