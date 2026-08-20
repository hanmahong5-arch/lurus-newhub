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
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'en' } }),
}));

const navigate = vi.fn();
vi.mock('react-router-dom', () => ({ useNavigate: () => navigate }));

// Steps.Step surfaces its "done" tick as an attribute so a step's completion
// state stays assertable; the Banner renders its description so the curl
// example is visible if it is ever produced.
vi.mock('@douyinfe/semi-ui', () => {
  const Steps = ({ current, children }) =>
    React.createElement(
      'ol',
      { 'data-testid': 'steps', 'data-current': String(current) },
      children,
    );
  Steps.Step = ({ title, description, icon }) =>
    React.createElement(
      'li',
      { 'data-done': icon ? 'yes' : 'no' },
      React.createElement('span', null, title),
      React.createElement('small', null, description),
    );
  const Typography = ({ children }) =>
    React.createElement('div', null, children);
  Typography.Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);
  return {
    Card: ({ children }) =>
      React.createElement('section', { 'data-testid': 'card' }, children),
    Steps,
    Typography,
    Button: ({ children, onClick }) =>
      React.createElement('button', { type: 'button', onClick }, children),
    Banner: ({ description }) =>
      React.createElement('div', { 'data-testid': 'banner' }, description),
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconTickCircle: () => React.createElement('i', { 'data-testid': 'tick' }),
  IconArrowRight: () => React.createElement('i', { 'data-testid': 'arrow' }),
}));

const apiGet = vi.fn();
const isAdmin = vi.fn(() => true);
vi.mock('../../helpers', () => ({
  API: { get: (...a) => apiGet(...a) },
  isAdmin: (...a) => isAdmin(...a),
}));

import OnboardingChecklist from './OnboardingChecklist';

const page = (rows) => ({ data: { data: rows } });

const mockChecks = ({ channels, tokens }) => {
  apiGet.mockImplementation((url) => {
    if (url.startsWith('/api/channel')) return Promise.resolve(page(channels));
    if (url.startsWith('/api/token')) return Promise.resolve(page(tokens));
    return Promise.resolve(page([]));
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  isAdmin.mockReturnValue(true);
  mockChecks({ channels: [], tokens: [] });
});

describe('OnboardingChecklist — visibility', () => {
  it('never shows to a non-admin and never spends an API call on one', async () => {
    isAdmin.mockReturnValue(false);
    const { container } = render(
      React.createElement(OnboardingChecklist, { serverAddress: '' }),
    );
    await waitFor(() => expect(container).toBeEmptyDOMElement());
    expect(apiGet).not.toHaveBeenCalled();
  });

  it('stays hidden — and skips the probe calls — once dismissed', async () => {
    localStorage.setItem('onboarding_dismissed', 'true');
    const { container } = render(
      React.createElement(OnboardingChecklist, { serverAddress: '' }),
    );
    await waitFor(() => expect(container).toBeEmptyDOMElement());
    expect(apiGet).not.toHaveBeenCalled();
  });

  it('disappears for good when the admin dismisses it, and remembers that', async () => {
    render(React.createElement(OnboardingChecklist, { serverAddress: '' }));
    await screen.findByTestId('card');
    fireEvent.click(screen.getByText('onboarding_dismiss'));
    expect(screen.queryByTestId('card')).not.toBeInTheDocument();
    expect(localStorage.getItem('onboarding_dismissed')).toBe('true');
  });

  it('goes away once both a channel and a token exist', async () => {
    mockChecks({ channels: [{ id: 1 }], tokens: [{ id: 2 }] });
    const { container } = render(
      React.createElement(OnboardingChecklist, { serverAddress: '' }),
    );
    await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it('probes both the channel and the token list exactly once', async () => {
    render(React.createElement(OnboardingChecklist, { serverAddress: '' }));
    await screen.findByTestId('card');
    expect(apiGet).toHaveBeenCalledWith('/api/channel/?p=0&size=1');
    expect(apiGet).toHaveBeenCalledWith('/api/token/?p=0&size=1');
    expect(apiGet).toHaveBeenCalledTimes(2);
  });
});

describe('OnboardingChecklist — guidance', () => {
  it('points a fresh install at channel creation first', async () => {
    render(React.createElement(OnboardingChecklist, { serverAddress: '' }));
    await screen.findByTestId('card');
    expect(screen.getByTestId('steps')).toHaveAttribute('data-current', '0');
    // The primary CTA repeats the step title, so match the action button by role.
    const cta = screen
      .getAllByRole('button')
      .find((b) => b.textContent.includes('onboarding_step_channel'));
    fireEvent.click(cta);
    expect(navigate).toHaveBeenCalledWith('/console/channel');
  });

  it('advances to token creation once a channel exists, and ticks step one', async () => {
    mockChecks({ channels: [{ id: 1 }], tokens: [] });
    render(React.createElement(OnboardingChecklist, { serverAddress: '' }));
    await screen.findByTestId('card');
    expect(screen.getByTestId('steps')).toHaveAttribute('data-current', '1');
    const items = screen.getAllByRole('listitem');
    expect(items[0]).toHaveAttribute('data-done', 'yes');
    expect(items[1]).toHaveAttribute('data-done', 'no');

    const cta = screen
      .getAllByRole('button')
      .find((b) => b.textContent.includes('onboarding_step_token'));
    fireEvent.click(cta);
    expect(navigate).toHaveBeenCalledWith('/console/token');
  });

  it('treats a failed probe as "nothing configured yet" instead of blowing up', async () => {
    apiGet.mockRejectedValue(new Error('boom'));
    render(React.createElement(OnboardingChecklist, { serverAddress: '' }));
    await screen.findByTestId('card');
    expect(screen.getByTestId('steps')).toHaveAttribute('data-current', '0');
    const items = screen.getAllByRole('listitem');
    expect(items[0]).toHaveAttribute('data-done', 'no');
  });

  // DEFECT (correctness / dead code): the third step ("try it with curl") can
  // never render. `currentStep` is the index of the first not-done step, and
  // step three is hard-coded `done: false`, so reaching index 2 requires both
  // earlier steps done — but that is exactly the `allDone` condition that
  // returns `null` a few lines above. The curl snippet, `serverAddress`, and
  // the whole `currentStep === 2` Banner branch are unreachable in every
  // reachable state, so the onboarding flow silently drops its final
  // "now test it" instruction.
  //
  // Resolution is either "make step three reachable" (the it.skip below) or
  // "delete the dead branch". Whoever picks the second must delete the lock.
  //
  // The pin that used to sit here swept all four channel/token combinations
  // and asserted no curl example in ANY of them — including the both-present
  // one, which is exactly the state the lock says must show it. Making step
  // three reachable would have turned this red. Trimmed to the three states
  // where "now try it" would be premature, which is an invariant either side
  // of that fix: you cannot call the API before a channel and a token exist,
  // so offering the command there would send the user into a 401.
  it('never offers the curl example before a channel and a token both exist', async () => {
    for (const combo of [
      { channels: [], tokens: [] },
      { channels: [{ id: 1 }], tokens: [] },
      { channels: [], tokens: [{ id: 1 }] },
    ]) {
      vi.clearAllMocks();
      localStorage.clear();
      mockChecks(combo);
      const { unmount } = render(
        React.createElement(OnboardingChecklist, {
          serverAddress: 'https://hub.example.com',
        }),
      );
      await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(2));
      // The checklist itself is on screen in all three states — without this
      // the two negatives below would also pass on a component that rendered
      // nothing at all.
      expect(screen.getByTestId('card')).toBeInTheDocument();
      expect(screen.queryByTestId('banner')).not.toBeInTheDocument();
      expect(screen.queryByText(/curl /)).not.toBeInTheDocument();
      unmount();
    }
  });

  it.skip('the final onboarding step must show a runnable curl example', async () => {
    mockChecks({ channels: [{ id: 1 }], tokens: [{ id: 1 }] });
    render(
      React.createElement(OnboardingChecklist, {
        serverAddress: 'https://hub.example.com',
      }),
    );
    const banner = await screen.findByTestId('banner');
    expect(banner.textContent).toContain(
      'curl https://hub.example.com/v1/chat/completions',
    );
  });
});
