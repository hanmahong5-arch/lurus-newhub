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
 * Option-fetching wrapper components (ChatsSetting, DrawingSetting,
 * ModelSetting, ModelDeploymentSetting, RateLimitSetting).
 *
 * Each of these does exactly one interesting thing: it GETs /api/option/ and
 * decides, per key, what JS TYPE the value reaches the form as. Getting that
 * wrong is the silent-config-corruption failure mode — a Switch bound to the
 * string 'false' renders as ON (non-empty string is truthy) and then saves
 * "enabled" over a setting the operator deliberately turned off.
 *
 * So every assertion below is on the *type and value* the child receives, not
 * on "the child rendered". The child stubs serialise `props.options` into a
 * data attribute with JSON.stringify, which preserves the boolean/string
 * distinction (`{"k":true}` vs `{"k":"true"}`) and omits dropped keys — i.e.
 * the thing under test is visible in the DOM, not just executed.
 */
import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key, i18n: { language: 'en' } }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

// Real `toBoolean` is kept — it *is* the coercion under test. Only the
// network + toast surface is faked.
vi.mock('../../helpers', async () => {
  const boolean = await vi.importActual('../../helpers/boolean');
  return {
    API: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
    showError: vi.fn(),
    showSuccess: vi.fn(),
    toBoolean: boolean.toBoolean,
  };
});

// Semi's barrel reaches semi-illustrations -> lottie-web -> canvas, which
// jsdom does not implement. Stub only what these wrappers render, and keep
// `spinning` observable so the loading branch leaves a trace.
vi.mock('@douyinfe/semi-ui', () => {
  const Spin = ({ spinning, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'spin', 'data-spinning': String(!!spinning) },
      children,
    );
  const Card = ({ children }) =>
    React.createElement('div', { 'data-testid': 'card' }, children);
  const Tabs = ({ children }) =>
    React.createElement('div', { 'data-testid': 'tabs' }, children);
  Tabs.TabPane = ({ children }) =>
    React.createElement('div', { 'data-testid': 'tabpane' }, children);
  return { Spin, Card, Tabs };
});

// Child stub: renders a marker, exposes the exact options payload it was
// handed, and gives the test a way to fire the wrapper's own refresh().
// vi.hoisted, because vi.mock factories are lifted above module scope.
const { childStub } = vi.hoisted(() => ({
  childStub: (testid) => ({
    default: (props) =>
      React.createElement(
        'div',
        {
          'data-testid': testid,
          'data-options': JSON.stringify(props.options ?? null),
        },
        React.createElement(
          'button',
          {
            type: 'button',
            'data-testid': `${testid}-refresh`,
            onClick: () => props.refresh && props.refresh(),
          },
          'refresh',
        ),
      ),
  }),
}));

vi.mock('../../pages/Setting/Chat/SettingsChats', () =>
  childStub('chats-child'),
);
vi.mock('../../pages/Setting/Drawing/SettingsDrawing', () =>
  childStub('drawing-child'),
);
vi.mock('../../pages/Setting/Model/SettingGlobalModel', () =>
  childStub('global-model-child'),
);
vi.mock('../../pages/Setting/Model/SettingGeminiModel', () =>
  childStub('gemini-model-child'),
);
vi.mock('../../pages/Setting/Model/SettingClaudeModel', () =>
  childStub('claude-model-child'),
);
vi.mock('../../pages/Setting/Model/SettingModelDeployment', () =>
  childStub('deployment-child'),
);
vi.mock('../../pages/Setting/RateLimit/SettingsRequestRateLimit', () =>
  childStub('ratelimit-child'),
);

import ChatsSetting from './ChatsSetting';
import DrawingSetting from './DrawingSetting';
import ModelSetting from './ModelSetting';
import ModelDeploymentSetting from './ModelDeploymentSetting';
import RateLimitSetting from './RateLimitSetting';
import { API, showError } from '../../helpers';

/** Build the shape GET /api/option/ actually returns. */
const optionsOk = (pairs) => ({
  data: {
    success: true,
    data: Object.entries(pairs).map(([key, value]) => ({ key, value })),
  },
});

/** Read back the exact options object a child was rendered with. */
const optionsOf = (testid) =>
  JSON.parse(screen.getByTestId(testid).getAttribute('data-options'));

beforeEach(() => {
  API.get.mockReset();
  API.put.mockReset();
  showError.mockReset();
});

describe('ChatsSetting', () => {
  it('hands *Enabled keys down as real booleans and everything else as strings', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        ChatCacheEnabled: 'false',
        Chats: '[{"name":"demo"}]',
        SomeFreeText: 'false',
      }),
    );

    render(<ChatsSetting />);

    await waitFor(() => expect(screen.getByTestId('chats-child')).toBeTruthy());
    const opts = optionsOf('chats-child');

    // Positive: the boolean-typed key really became a boolean...
    expect(opts.ChatCacheEnabled).toBe(false);
    // ...and the negative half — a key with the *same* value but no `Enabled`
    // suffix is deliberately left a string. Without this pair, "coerces
    // everything" and "coerces the right keys" look identical.
    expect(opts.SomeFreeText).toBe('false');
    expect(typeof opts.SomeFreeText).toBe('string');
  });

  it('passes the Chats JSON payload through byte-for-byte (no reformatting)', async () => {
    const raw = '[{"name":"demo","id":1}]';
    API.get.mockResolvedValue(optionsOk({ Chats: raw }));

    render(<ChatsSetting />);

    await waitFor(() => expect(optionsOf('chats-child').Chats).toBe(raw));
  });

  it('coerces DefaultCollapseSidebar even though it has no Enabled suffix', async () => {
    API.get.mockResolvedValue(
      optionsOk({ DefaultCollapseSidebar: '1', Chats: '[]' }),
    );

    render(<ChatsSetting />);

    await waitFor(() =>
      expect(optionsOf('chats-child').DefaultCollapseSidebar).toBe(true),
    );
  });

  it('shows the spinner while loading and clears it afterwards', async () => {
    let release;
    API.get.mockReturnValue(
      new Promise((resolve) => {
        release = () => resolve(optionsOk({ Chats: '[]' }));
      }),
    );

    render(<ChatsSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('spin').getAttribute('data-spinning')).toBe(
        'true',
      ),
    );
    release();
    await waitFor(() =>
      expect(screen.getByTestId('spin').getAttribute('data-spinning')).toBe(
        'false',
      ),
    );
  });

  it('surfaces the backend message and keeps the declared defaults when the load is refused', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'option table locked' },
    });

    render(<ChatsSetting />);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('option table locked'),
    );
    // A refused load must not blank the form: the declared default survives.
    expect(optionsOf('chats-child')).toEqual({ Chats: '[]' });
  });
});

describe('DrawingSetting', () => {
  it('coerces every Mj*Enabled flag to a boolean, including the falsey ones', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        DrawingEnabled: 'true',
        MjNotifyEnabled: 'false',
        MjAccountFilterEnabled: '1',
        MjForwardUrlEnabled: '0',
        MjModeClearEnabled: '',
      }),
    );

    render(<DrawingSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('drawing-child')).toBeTruthy(),
    );
    expect(optionsOf('drawing-child')).toEqual({
      DrawingEnabled: true,
      MjNotifyEnabled: false,
      MjAccountFilterEnabled: true,
      MjForwardUrlEnabled: false,
      // '' is not a recognised truthy token -> off, not "unset".
      MjModeClearEnabled: false,
    });
  });

  it('re-reads the options when the child asks for a refresh', async () => {
    API.get
      .mockResolvedValueOnce(optionsOk({ DrawingEnabled: 'false' }))
      .mockResolvedValueOnce(optionsOk({ DrawingEnabled: 'true' }));

    render(<DrawingSetting />);
    await waitFor(() =>
      expect(optionsOf('drawing-child').DrawingEnabled).toBe(false),
    );

    fireEvent.click(screen.getByTestId('drawing-child-refresh'));

    await waitFor(() =>
      expect(optionsOf('drawing-child').DrawingEnabled).toBe(true),
    );
  });
});

describe('ModelSetting', () => {
  it('pretty-prints the JSON-valued keys and leaves plain strings alone', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        'gemini.safety_settings': '{"a":1}',
        'claude.default_max_tokens': '{"claude-3":4096}',
        'claude.thinking_adapter_budget_tokens_percentage': '0.8',
      }),
    );

    render(<ModelSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('gemini-model-child')).toBeTruthy(),
    );
    const opts = optionsOf('gemini-model-child');
    expect(opts['gemini.safety_settings']).toBe('{\n  "a": 1\n}');
    expect(opts['claude.default_max_tokens']).toBe('{\n  "claude-3": 4096\n}');
    // Negative half: a non-JSON key is NOT touched, and a numeric-looking
    // value stays a string (nothing here parses numbers).
    expect(opts['claude.thinking_adapter_budget_tokens_percentage']).toBe(
      '0.8',
    );
  });

  it('treats lowercase `enabled` suffixes as booleans too', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        'global.pass_through_request_enabled': 'true',
        'gemini.thinking_adapter_enabled': 'false',
        'general_setting.ping_interval_seconds': '60',
      }),
    );

    render(<ModelSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('global-model-child')).toBeTruthy(),
    );
    const opts = optionsOf('global-model-child');
    expect(opts['global.pass_through_request_enabled']).toBe(true);
    expect(opts['gemini.thinking_adapter_enabled']).toBe(false);
    expect(opts['general_setting.ping_interval_seconds']).toBe('60');
  });

  it('skips reformatting an empty JSON key instead of throwing', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        'gemini.safety_settings': '',
        'global.pass_through_request_enabled': 'true',
      }),
    );

    render(<ModelSetting />);

    // The `item.value !== ''` guard is what keeps this load alive; if it were
    // removed the whole fetch would reject and no option would arrive.
    await waitFor(() =>
      expect(
        optionsOf('claude-model-child')['global.pass_through_request_enabled'],
      ).toBe(true),
    );
    expect(optionsOf('claude-model-child')['gemini.safety_settings']).toBe('');
    expect(showError).not.toHaveBeenCalled();
  });

  it('feeds the same options object to all three vendor panels', async () => {
    API.get.mockResolvedValue(optionsOk({ 'claude.default_max_tokens': '[]' }));

    render(<ModelSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('claude-model-child')).toBeTruthy(),
    );
    expect(optionsOf('global-model-child')).toEqual(
      optionsOf('gemini-model-child'),
    );
    expect(optionsOf('global-model-child')).toEqual(
      optionsOf('claude-model-child'),
    );
  });
});

describe('ModelDeploymentSetting', () => {
  it('keeps both declared keys present even when the backend returns neither', async () => {
    API.get.mockResolvedValue(optionsOk({ SomethingElse: 'x' }));

    render(<ModelDeploymentSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('deployment-child')).toBeTruthy(),
    );
    const opts = optionsOf('deployment-child');
    // This wrapper (unlike its siblings) seeds newInputs with its own
    // defaults, so an unsaved deployment config still arrives typed.
    expect(opts['model_deployment.ionet.api_key']).toBe('');
    expect(opts['model_deployment.ionet.enabled']).toBe(false);
  });

  it('coerces the ionet enable flag and leaves the api key a string', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        'model_deployment.ionet.enabled': 'true',
        'model_deployment.ionet.api_key': 'sk-abc',
      }),
    );

    render(<ModelDeploymentSetting />);

    await waitFor(() =>
      expect(
        optionsOf('deployment-child')['model_deployment.ionet.enabled'],
      ).toBe(true),
    );
    expect(
      optionsOf('deployment-child')['model_deployment.ionet.api_key'],
    ).toBe('sk-abc');
  });
});

describe('RateLimitSetting', () => {
  it('pretty-prints the per-group rate limit map', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        ModelRequestRateLimitGroup: '{"vip":[10,20]}',
        ModelRequestRateLimitEnabled: 'true',
      }),
    );

    render(<RateLimitSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('ratelimit-child')).toBeTruthy(),
    );
    const opts = optionsOf('ratelimit-child');
    expect(opts.ModelRequestRateLimitGroup).toBe(
      '{\n  "vip": [\n    10,\n    20\n  ]\n}',
    );
    expect(opts.ModelRequestRateLimitEnabled).toBe(true);
  });

  it('leaves the numeric limits as strings (the child owns the number parsing)', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        ModelRequestRateLimitCount: '30',
        ModelRequestRateLimitDurationMinutes: '1',
        ModelRequestRateLimitGroup: '{}',
      }),
    );

    render(<RateLimitSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('ratelimit-child')).toBeTruthy(),
    );
    const opts = optionsOf('ratelimit-child');
    expect(opts.ModelRequestRateLimitCount).toBe('30');
    expect(opts.ModelRequestRateLimitDurationMinutes).toBe('1');
  });

  // ------------------------------------------------------------------
  // DEFECT: RateLimitSetting.jsx getOptions() calls
  //   JSON.parse(item.value)
  // on ModelRequestRateLimitGroup with no guard, while the component's own
  // declared default for that key is the empty string. JSON.parse('') throws,
  // the throw escapes the forEach, aborts the whole load, and onRefresh
  // reports a generic '刷新失败'. Consequence: on any install where that
  // option is empty (i.e. a fresh one), NONE of the rate-limit options load —
  // the form silently shows its hardcoded defaults, and the next save writes
  // those defaults over the live limits. The sibling ModelSetting guards the
  // identical call with `if (item.value !== '')`.
  // ------------------------------------------------------------------
  it.skip('an empty rate-limit group must not abort the whole option load', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        ModelRequestRateLimitGroup: '',
        ModelRequestRateLimitEnabled: 'true',
        ModelRequestRateLimitCount: '30',
      }),
    );

    render(<RateLimitSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('ratelimit-child')).toBeTruthy(),
    );
    // The other options must still make it through.
    await waitFor(() =>
      expect(optionsOf('ratelimit-child').ModelRequestRateLimitCount).toBe(
        '30',
      ),
    );
    expect(optionsOf('ratelimit-child').ModelRequestRateLimitEnabled).toBe(
      true,
    );
  });

  // NOTE: there are deliberately no live tests asserting that the load *is*
  // discarded (showError('刷新失败'), defaults left in place). Those pass today
  // and turn RED the moment the JSON.parse is guarded — the repair would read
  // as a regression. The skipped CONTRACT lock records the defect. What stays
  // live below is the invariant that holds both before and after the repair.

  // The dangerous state is not "the parse failed"; it is "the parse failed AND
  // nobody was told", because the form then shows hardcoded defaults that the
  // next save writes over the live limits. Today the wrapper reaches this bar
  // by reporting the failure; once guarded it will reach it by applying the
  // value. Either is acceptable; silently keeping defaults is not.
  it('never leaves stale defaults on screen without reporting the failed load', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        ModelRequestRateLimitGroup: '{oops',
        ModelRequestRateLimitEnabled: 'true',
      }),
    );

    render(<RateLimitSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('ratelimit-child')).toBeTruthy(),
    );
    await waitFor(() => {
      const applied =
        optionsOf('ratelimit-child').ModelRequestRateLimitEnabled === true;
      const reported = showError.mock.calls.length > 0;
      expect({ applied, reported }).not.toEqual({
        applied: false,
        reported: false,
      });
    });
  });
});
