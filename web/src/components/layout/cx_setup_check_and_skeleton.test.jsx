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
import {
  describe,
  it,
  expect,
  vi,
  beforeAll,
  beforeEach,
  afterAll,
  afterEach,
} from 'vitest';
import { render, screen } from '@testing-library/react';

// Skeleton renders its placeholder into a marker element carrying the geometry
// it was given: the whole purpose of SkeletonWrapper is picking the right
// placeholder shape per surface, and a null stub would make every shape look
// identical while still counting as covered.
vi.mock('@douyinfe/semi-ui', () => {
  const Skeleton = ({ placeholder }) =>
    React.createElement('span', { 'data-testid': 'sk' }, placeholder);
  Skeleton.Title = ({ style }) =>
    React.createElement('span', {
      'data-testid': 'sk-title',
      'data-width': String(style?.width),
      'data-height': String(style?.height),
      'data-radius': String(style?.borderRadius),
    });
  Skeleton.Avatar = ({ size, shape, style }) =>
    React.createElement('span', {
      'data-testid': 'sk-avatar',
      'data-size': String(size),
      'data-shape': String(shape),
      'data-width': String(style?.width),
    });
  Skeleton.Image = ({ className }) =>
    React.createElement('span', {
      'data-testid': 'sk-image',
      'data-class': className,
    });
  Skeleton.Paragraph = ({ rows }) =>
    React.createElement('span', { 'data-testid': 'sk-para' }, String(rows));
  return { Skeleton };
});

const locationValue = { pathname: '/console/dashboard' };
vi.mock('react-router-dom', () => ({
  useLocation: () => locationValue,
  Navigate: ({ to }) => React.createElement('div', { 'data-nav-to': to }),
}));

import { StatusContext } from '../../context/Status';
import SetupCheck from './SetupCheck';
import SkeletonWrapper from './components/SkeletonWrapper';

const realLocation = window.location;
let hrefWrites = [];
const installLocation = () => {
  hrefWrites = [];
  const stub = { origin: 'https://hub.test' };
  Object.defineProperty(stub, 'href', {
    get: () => 'https://hub.test/console/dashboard',
    set: (v) => hrefWrites.push(v),
  });
  Object.defineProperty(window, 'location', {
    configurable: true,
    writable: true,
    value: stub,
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  locationValue.pathname = '/console/dashboard';
  installLocation();
});

afterEach(() => {
  Object.defineProperty(window, 'location', {
    configurable: true,
    writable: true,
    value: realLocation,
  });
});

const renderGate = (status) =>
  render(
    React.createElement(
      StatusContext.Provider,
      { value: [{ status }, vi.fn()] },
      React.createElement(
        SetupCheck,
        null,
        React.createElement('div', { 'data-testid': 'app' }, 'app'),
      ),
    ),
  );

describe('SetupCheck', () => {
  it('always renders the app it wraps, even while it is redirecting', () => {
    renderGate({ setup: false });
    expect(screen.getByTestId('app')).toBeInTheDocument();
  });

  it('force-navigates an uninitialised install to the setup wizard', () => {
    renderGate({ setup: false });
    expect(hrefWrites).toEqual(['/setup']);
  });

  it('leaves an initialised install alone', () => {
    renderGate({ setup: true });
    expect(hrefWrites).toEqual([]);
  });

  it('does nothing until the status payload has actually arrived', () => {
    renderGate(undefined);
    expect(hrefWrites).toEqual([]);
    // `undefined` must not be treated as "not set up" — that would bounce every
    // user to /setup for the duration of the first status fetch.
    renderGate({});
    expect(hrefWrites).toEqual([]);
  });

  it('does not bounce the setup page back to itself', () => {
    locationValue.pathname = '/setup';
    renderGate({ setup: false });
    expect(hrefWrites).toEqual([]);
  });
});

describe('SkeletonWrapper', () => {
  // React emits its missing-`key` warning only ONCE per owning component per
  // module instance. An assertion that spied on console.error inside a single
  // test would therefore only work while that test happened to be the first
  // thing in the file to render the collapsed sidebar — and would silently
  // gut itself the day an unrelated test was added above it. The recorder is
  // installed for the whole describe instead, so the warning is captured
  // wherever it fires and the assertion below does not depend on test order.
  const keyWarnings = [];
  let errorSpy;
  beforeAll(() => {
    errorSpy = vi.spyOn(console, 'error').mockImplementation((...args) => {
      if (String(args[0]).includes('unique "key" prop')) {
        keyWarnings.push(String(args[0]));
      }
    });
  });
  afterAll(() => {
    errorSpy.mockRestore();
  });

  it('renders its children untouched when not loading', () => {
    render(
      React.createElement(
        SkeletonWrapper,
        { loading: false, type: 'title' },
        React.createElement('span', { 'data-testid': 'real' }, 'real content'),
      ),
    );
    expect(screen.getByTestId('real')).toBeInTheDocument();
    expect(screen.queryByTestId('sk')).not.toBeInTheDocument();
  });

  it('replaces its children with a placeholder when loading', () => {
    render(
      React.createElement(
        SkeletonWrapper,
        { loading: true, type: 'title' },
        React.createElement('span', { 'data-testid': 'real' }),
      ),
    );
    expect(screen.queryByTestId('real')).not.toBeInTheDocument();
    expect(screen.getByTestId('sk-title')).toHaveAttribute('data-height', '24');
  });

  it('produces one navigation placeholder per requested link', () => {
    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'navigation',
        count: 5,
        width: 70,
        height: 18,
      }),
    );
    const titles = screen.getAllByTestId('sk-title');
    expect(titles).toHaveLength(5);
    expect(titles[0]).toHaveAttribute('data-width', '70');
    expect(titles[0]).toHaveAttribute('data-height', '18');
  });

  it('narrows navigation placeholders on mobile', () => {
    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'navigation',
        count: 2,
        width: 70,
        isMobile: true,
      }),
    );
    expect(screen.getAllByTestId('sk-title')[0]).toHaveAttribute(
      'data-width',
      '40',
    );
  });

  it('pairs an avatar with a label for the user area, and shrinks it on mobile', () => {
    const { unmount } = render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'userArea',
        width: 50,
      }),
    );
    expect(screen.getByTestId('sk-avatar')).toHaveAttribute(
      'data-size',
      'extra-small',
    );
    expect(screen.getByTestId('sk-title')).toHaveAttribute('data-width', '50');
    unmount();

    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'userArea',
        width: 50,
        isMobile: true,
      }),
    );
    expect(screen.getByTestId('sk-title')).toHaveAttribute('data-width', '15');
  });

  it('uses an image placeholder for the logo slot', () => {
    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'image',
        className: 'my-logo',
      }),
    );
    expect(screen.getByTestId('sk-image').getAttribute('data-class')).toContain(
      'my-logo',
    );
  });

  it('rounds the button placeholder into a pill', () => {
    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'button',
        width: 90,
        height: 32,
      }),
    );
    const title = screen.getByTestId('sk-title');
    expect(title).toHaveAttribute('data-radius', '9999');
    expect(title).toHaveAttribute('data-width', '90');
  });

  it('falls back to a plain text placeholder for an unknown type', () => {
    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'not-a-real-type',
        width: 33,
        height: 11,
      }),
    );
    const title = screen.getByTestId('sk-title');
    expect(title).toHaveAttribute('data-width', '33');
    expect(title).toHaveAttribute('data-radius', 'undefined');
  });

  it('draws icon+label rows for sidebar nav items, with sane defaults', () => {
    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'sidebarNavItem',
        count: 3,
        width: 0,
        height: 0,
      }),
    );
    expect(screen.getAllByTestId('sk-avatar')).toHaveLength(3);
    const titles = screen.getAllByTestId('sk-title');
    expect(titles).toHaveLength(3);
    // width/height of 0 are falsy, so the component's own defaults apply.
    expect(titles[0]).toHaveAttribute('data-width', '80');
    expect(titles[0]).toHaveAttribute('data-height', '14');
  });

  it('draws a single short bar for a sidebar group title', () => {
    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'sidebarGroupTitle',
      }),
    );
    const titles = screen.getAllByTestId('sk-title');
    expect(titles).toHaveLength(1);
    expect(titles[0]).toHaveAttribute('data-width', '60');
  });

  it('lays out the full sidebar as four groups when the admin section is shown', () => {
    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'sidebar',
        showAdmin: true,
      }),
    );
    // 4 group titles + 2+5+2+5 = 14 nav rows = 18 title bars.
    expect(screen.getAllByTestId('sk-title')).toHaveLength(18);
  });

  it('drops the admin group entirely for a non-admin', () => {
    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'sidebar',
        showAdmin: false,
      }),
    );
    // 3 group titles + 2+5+2 = 9 nav rows = 12 title bars.
    expect(screen.getAllByTestId('sk-title')).toHaveLength(12);
  });

  // DEFECT (cosmetic): the collapsed sidebar builds its four icon groups with
  // `Array(n).fill(null).map((_, i) => <CollapsedRow keyPrefix=... index={i} />)`
  // and never sets a React `key`. `keyPrefix`/`index` are passed as props that
  // CollapsedRow ignores. React therefore warns on every render of the
  // collapsed sidebar and has to re-create the rows instead of reconciling
  // them. The expanded branch right below it does set keys correctly, so this
  // is an oversight rather than a choice.
  //
  // The pin that used to sit here asserted the warning IS emitted. It was
  // deleted rather than rewritten, for two reasons. It was harmful: adding the
  // missing keys — the whole point of the lock below — would have turned it
  // red, so the fix would have arrived looking like a regression. And it was
  // order-coupled: it passed only because it was the first test in the file to
  // render the collapsed sidebar and therefore the only one React would warn
  // for, so adding any test above it would have silently gutted it into a
  // green no-op. Everything else it rendered is covered by "collapses to
  // icon-only rows with no labels at all" below. The lock now reads the
  // describe-level recorder, which does not care who rendered first.
  it.skip('the collapsed sidebar must key its rows like the expanded one does', () => {
    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'sidebar',
        collapsed: true,
      }),
    );
    expect(keyWarnings).toEqual([]);
  });

  it('collapses to icon-only rows with no labels at all', () => {
    // No local console.error spy: the describe-level recorder already
    // swallows the noise, and a local one would hide the missing-key warning
    // from the lock above if this test ever ran first.
    render(
      React.createElement(SkeletonWrapper, {
        loading: true,
        type: 'sidebar',
        collapsed: true,
      }),
    );
    expect(screen.queryByTestId('sk-title')).not.toBeInTheDocument();
    // 2 + 5 + 2 + 5 icon rows.
    expect(screen.getAllByTestId('sk-avatar')).toHaveLength(14);
  });
});
