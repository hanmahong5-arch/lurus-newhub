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
import {
  cleanup as cleanupRender,
  fireEvent,
  render,
  screen,
} from '@testing-library/react';

// Dropdown renders its menu inline instead of into a portal so the menu items
// stay clickable and assertable; Badge exposes its count. Rendering either as
// `() => null` would silently delete the notification counter and every menu
// action while leaving the coverage numbers untouched.
vi.mock('@douyinfe/semi-ui', () => {
  const Dropdown = ({ render: menu, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'dropdown' },
      children,
      React.createElement('div', { 'data-testid': 'dropdown-menu' }, menu),
    );
  Dropdown.Menu = ({ children }) => React.createElement('ul', null, children);
  Dropdown.Item = ({ children, onClick, className, icon }) =>
    React.createElement(
      'li',
      { onClick, className, role: 'menuitem' },
      icon,
      children,
    );
  Dropdown.Divider = () =>
    React.createElement('hr', { 'data-testid': 'divider' });
  const Typography = ({ children }) =>
    React.createElement('div', null, children);
  Typography.Text = ({ children, strong, ...rest }) =>
    React.createElement('span', rest, children);
  Typography.Title = ({ children, ...rest }) =>
    React.createElement('h4', rest, children);
  return {
    Button: ({ icon, children, onClick, className, 'aria-label': label }) =>
      React.createElement(
        'button',
        { type: 'button', onClick, className, 'aria-label': label },
        icon,
        children,
      ),
    Dropdown,
    Typography,
    Tag: ({ children, color }) =>
      React.createElement('span', { 'data-tag-color': color }, children),
    Avatar: ({ children, color }) =>
      React.createElement('span', { 'data-avatar-color': color }, children),
    Badge: ({ count, children, overflowCount }) =>
      React.createElement(
        'span',
        {
          'data-testid': 'badge',
          'data-count': String(count),
          'data-overflow': String(overflowCount),
        },
        children,
      ),
    Popover: ({ content, visible, children }) =>
      React.createElement(
        'div',
        null,
        children,
        visible
          ? React.createElement('div', { 'data-testid': 'popover' }, content)
          : null,
      ),
    Skeleton: () => React.createElement('div', { 'data-testid': 'skeleton' }),
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconClose: () => React.createElement('i', { 'data-testid': 'icon-close' }),
  IconMenu: () => React.createElement('i', { 'data-testid': 'icon-menu' }),
  IconExit: () => React.createElement('i', { 'data-testid': 'icon-exit' }),
  IconUserSetting: () =>
    React.createElement('i', { 'data-testid': 'icon-user-setting' }),
  IconCreditCard: () =>
    React.createElement('i', { 'data-testid': 'icon-card' }),
  IconKey: () => React.createElement('i', { 'data-testid': 'icon-key' }),
}));

vi.mock('lucide-react', () => ({
  Languages: () => React.createElement('i', { 'data-testid': 'icon-lang' }),
  Sun: () => React.createElement('i', { 'data-testid': 'icon-sun' }),
  Moon: () => React.createElement('i', { 'data-testid': 'icon-moon' }),
  Monitor: () => React.createElement('i', { 'data-testid': 'icon-monitor' }),
  Bell: () => React.createElement('i', { 'data-testid': 'icon-bell' }),
  BellOff: () => React.createElement('i', { 'data-testid': 'icon-bell-off' }),
  ChevronDown: () =>
    React.createElement('i', { 'data-testid': 'icon-chevron' }),
}));

const flag = vi.hoisted(
  () => (name) => () =>
    React.createElement('i', { 'data-testid': `flag-${name}` }),
);
vi.mock('country-flag-icons/react/3x2', () => ({
  CN: flag('CN'),
  GB: flag('GB'),
  FR: flag('FR'),
  RU: flag('RU'),
  JP: flag('JP'),
  VN: flag('VN'),
}));

const fireworksInit = vi.fn();
const fireworksStart = vi.fn();
const fireworksStop = vi.fn();
vi.mock('react-fireworks', () => ({
  default: {
    init: (...a) => fireworksInit(...a),
    start: (...a) => fireworksStart(...a),
    stop: (...a) => fireworksStop(...a),
  },
}));

vi.mock('react-router-dom', () => ({
  Link: ({ to, children, className }) =>
    React.createElement('a', { href: to, className }, children),
}));

vi.mock('../components/SkeletonWrapper', () => ({
  default: ({ loading, type, children, count, width }) =>
    loading
      ? React.createElement('div', {
          'data-testid': 'skeleton-wrapper',
          'data-type': type,
          'data-count': String(count),
          'data-width': String(width),
        })
      : children,
}));

vi.mock('../../common/StreakBadge', () => ({
  default: () => React.createElement('i', { 'data-testid': 'streak-badge' }),
}));

const stringToColor = vi.fn(() => 'indigo');
vi.mock('../../../helpers', () => ({
  stringToColor: (...a) => stringToColor(...a),
}));

const actualTheme = { current: 'light' };
vi.mock('../../../context/Theme', () => ({
  useActualTheme: () => actualTheme.current,
}));

// --- HeaderBar (index.jsx) collaborators -----------------------------------
// The three hooks are the component's entire input surface; NoticeModal is a
// marker that echoes the props it receives so "which tab does it open on?"
// stays checkable.
const headerBarState = {
  userState: { user: { username: 'erin' } },
  statusState: { status: {} },
  isMobile: false,
  collapsed: false,
  logoLoaded: true,
  currentLang: 'en',
  isLoading: false,
  systemName: 'Lurus Hub',
  logo: '/logo.png',
  isNewYear: false,
  isSelfUseMode: false,
  docsLink: 'https://docs',
  isDemoSiteMode: false,
  isConsoleRoute: true,
  theme: 'light',
  headerNavModules: {},
  pricingRequireAuth: false,
  logout: vi.fn(),
  handleLanguageChange: vi.fn(),
  handleThemeToggle: vi.fn(),
  handleMobileMenuToggle: vi.fn(),
  navigate: vi.fn(),
  t: (k) => k,
};
const useHeaderBarSpy = vi.fn();
vi.mock('../../../hooks/common/useHeaderBar', () => ({
  useHeaderBar: (...a) => {
    useHeaderBarSpy(...a);
    return headerBarState;
  },
}));

const notificationsState = {
  noticeVisible: false,
  unreadCount: 0,
  handleNoticeOpen: vi.fn(),
  handleNoticeClose: vi.fn(),
  getUnreadKeys: () => ['k1'],
};
vi.mock('../../../hooks/common/useNotifications', () => ({
  useNotifications: () => notificationsState,
}));

vi.mock('../../../hooks/common/useNavigation', () => ({
  useNavigation: (t, docsLink) => ({
    mainNavLinks: [
      { itemKey: 'home', text: 'Home', to: '/' },
      {
        itemKey: 'docs',
        text: 'Docs',
        isExternal: true,
        externalLink: docsLink,
      },
    ],
  }),
}));

vi.mock('../NoticeModal', () => ({
  default: ({ visible, defaultTab, unreadKeys, isMobile }) =>
    React.createElement('div', {
      'data-testid': 'notice-modal',
      'data-visible': String(visible),
      'data-default-tab': String(defaultTab),
      'data-unread-keys': (unreadKeys || []).join(','),
      'data-mobile': String(isMobile),
    }),
}));

import MobileMenuButton from './MobileMenuButton';
import HeaderLogo from './HeaderLogo';
import Navigation from './Navigation';
import NewYearButton from './NewYearButton';
import NotificationButton from './NotificationButton';
import ThemeToggle from './ThemeToggle';
import LanguageSelector from './LanguageSelector';
import UserArea from './UserArea';
import ActionButtons from './ActionButtons';
import HeaderBar from './index';

const t = (k) => k;

beforeEach(() => {
  vi.clearAllMocks();
  actualTheme.current = 'light';
});

describe('MobileMenuButton', () => {
  it('stays out of the way outside the console and on desktop', () => {
    const { container, unmount } = render(
      React.createElement(MobileMenuButton, {
        isConsoleRoute: false,
        isMobile: true,
        t,
      }),
    );
    expect(container).toBeEmptyDOMElement();
    unmount();

    const { container: c2 } = render(
      React.createElement(MobileMenuButton, {
        isConsoleRoute: true,
        isMobile: false,
        t,
      }),
    );
    expect(c2).toBeEmptyDOMElement();
  });

  it('offers "open" with a menu glyph when the drawer is shut', () => {
    render(
      React.createElement(MobileMenuButton, {
        isConsoleRoute: true,
        isMobile: true,
        drawerOpen: false,
        onToggle: vi.fn(),
        t,
      }),
    );
    expect(screen.getByLabelText('打开侧边栏')).toBeInTheDocument();
    expect(screen.getByTestId('icon-menu')).toBeInTheDocument();
  });

  it('flips to "close" with a close glyph when the drawer is open, and toggles', () => {
    const onToggle = vi.fn();
    render(
      React.createElement(MobileMenuButton, {
        isConsoleRoute: true,
        isMobile: true,
        drawerOpen: true,
        onToggle,
        t,
      }),
    );
    expect(screen.getByTestId('icon-close')).toBeInTheDocument();
    fireEvent.click(screen.getByLabelText('关闭侧边栏'));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  // DEFECT (cosmetic / dead code): the glyph and label are chosen with
  // `(isMobile ? drawerOpen : collapsed)`, but the component has already
  // returned null unless `isMobile` is true — so the `collapsed` arm can never
  // be evaluated and the `collapsed` prop is inert. Either the early return
  // should also admit the desktop-collapsed case or the prop should go.
  //
  // The pin that used to sit here was titled after the dead arm and asserted a
  // single label, which restates what "offers 'open' with a menu glyph" above
  // already says. Rewritten as the invariant that outlives either resolution:
  // on mobile the glyph and the label follow the DRAWER, and `collapsed` — a
  // desktop concept — must never override it. That is still true if the early
  // return is widened to admit desktop-collapsed (the lock below), and still
  // true if the prop is deleted instead; it goes red on a careless
  // `(isMobile ? collapsed : drawerOpen)` swap of the ternary's arms.
  it('follows the drawer, not `collapsed`, whenever it is the mobile button', () => {
    const view = (over) =>
      React.createElement(MobileMenuButton, {
        isConsoleRoute: true,
        isMobile: true,
        onToggle: vi.fn(),
        t,
        ...over,
      });

    const { unmount } = render(view({ drawerOpen: false, collapsed: true }));
    expect(screen.getByLabelText('打开侧边栏')).toBeInTheDocument();
    expect(screen.getByTestId('icon-menu')).toBeInTheDocument();
    expect(screen.queryByTestId('icon-close')).not.toBeInTheDocument();
    unmount();

    render(view({ drawerOpen: true, collapsed: false }));
    expect(screen.getByLabelText('关闭侧边栏')).toBeInTheDocument();
    expect(screen.getByTestId('icon-close')).toBeInTheDocument();
    expect(screen.queryByTestId('icon-menu')).not.toBeInTheDocument();
  });

  it.skip('a collapsed desktop sidebar must still get a toggle button', () => {
    const { container } = render(
      React.createElement(MobileMenuButton, {
        isConsoleRoute: true,
        isMobile: false,
        collapsed: true,
        onToggle: vi.fn(),
        t,
      }),
    );
    expect(container).not.toBeEmptyDOMElement();
  });
});

describe('HeaderLogo', () => {
  const common = {
    logo: '/logo.png',
    logoLoaded: true,
    isLoading: false,
    systemName: 'Lurus Hub',
    t,
  };

  it('gets out of the way on the mobile console, where the sidebar owns the brand', () => {
    const { container } = render(
      React.createElement(HeaderLogo, {
        ...common,
        isMobile: true,
        isConsoleRoute: true,
      }),
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('links home and shows the logo plus the system name', () => {
    render(React.createElement(HeaderLogo, common));
    expect(screen.getByRole('link')).toHaveAttribute('href', '/');
    expect(screen.getByAltText('logo')).toHaveAttribute('src', '/logo.png');
    expect(screen.getByText('Lurus Hub')).toBeInTheDocument();
  });

  it('keeps the logo transparent until it has actually loaded', () => {
    const { rerender } = render(
      React.createElement(HeaderLogo, { ...common, logoLoaded: false }),
    );
    expect(screen.getByAltText('logo').className).toContain('opacity-0');
    rerender(React.createElement(HeaderLogo, common));
    expect(screen.getByAltText('logo').className).toContain('opacity-100');
  });

  it('shows a title skeleton instead of the name while loading', () => {
    render(React.createElement(HeaderLogo, { ...common, isLoading: true }));
    const skeletons = screen.getAllByTestId('skeleton-wrapper');
    expect(skeletons.some((s) => s.getAttribute('data-type') === 'title')).toBe(
      true,
    );
    expect(screen.queryByText('Lurus Hub')).not.toBeInTheDocument();
  });

  it('badges self-use mode in purple and demo mode in blue, but never while loading', () => {
    const { unmount } = render(
      React.createElement(HeaderLogo, { ...common, isSelfUseMode: true }),
    );
    expect(screen.getByText('自用模式')).toHaveAttribute(
      'data-tag-color',
      'purple',
    );
    unmount();

    const { unmount: u2 } = render(
      React.createElement(HeaderLogo, { ...common, isDemoSiteMode: true }),
    );
    expect(screen.getByText('演示站点')).toHaveAttribute(
      'data-tag-color',
      'blue',
    );
    u2();

    render(
      React.createElement(HeaderLogo, {
        ...common,
        isSelfUseMode: true,
        isLoading: true,
      }),
    );
    expect(screen.queryByText('自用模式')).not.toBeInTheDocument();
  });
});

describe('Navigation', () => {
  const links = [
    { itemKey: 'home', text: 'Home', to: '/' },
    {
      itemKey: 'docs',
      text: 'Docs',
      isExternal: true,
      externalLink: 'https://d',
    },
    { itemKey: 'console', text: 'Console', to: '/console' },
    { itemKey: 'pricing', text: 'Pricing', to: '/pricing' },
  ];

  it('opens external links in a new tab with noopener/noreferrer', () => {
    render(
      React.createElement(Navigation, {
        mainNavLinks: links,
        isLoading: false,
        userState: { user: { id: 1 } },
        pricingRequireAuth: false,
      }),
    );
    const docs = screen.getByText('Docs').closest('a');
    expect(docs).toHaveAttribute('href', 'https://d');
    expect(docs).toHaveAttribute('target', '_blank');
    expect(docs).toHaveAttribute('rel', 'noopener noreferrer');
  });

  it('sends an anonymous visitor to login instead of the console', () => {
    render(
      React.createElement(Navigation, {
        mainNavLinks: links,
        isLoading: false,
        userState: { user: null },
        pricingRequireAuth: false,
      }),
    );
    expect(screen.getByText('Console').closest('a')).toHaveAttribute(
      'href',
      '/login',
    );
    // Pricing is public here, so it must NOT be diverted.
    expect(screen.getByText('Pricing').closest('a')).toHaveAttribute(
      'href',
      '/pricing',
    );
  });

  it('diverts pricing to login only when pricing requires auth and nobody is signed in', () => {
    const { unmount } = render(
      React.createElement(Navigation, {
        mainNavLinks: links,
        isLoading: false,
        userState: { user: null },
        pricingRequireAuth: true,
      }),
    );
    expect(screen.getByText('Pricing').closest('a')).toHaveAttribute(
      'href',
      '/login',
    );
    unmount();

    render(
      React.createElement(Navigation, {
        mainNavLinks: links,
        isLoading: false,
        userState: { user: { id: 1 } },
        pricingRequireAuth: true,
      }),
    );
    expect(screen.getByText('Pricing').closest('a')).toHaveAttribute(
      'href',
      '/pricing',
    );
  });

  it('shows navigation skeletons instead of links while loading', () => {
    render(
      React.createElement(Navigation, {
        mainNavLinks: links,
        isLoading: true,
        isMobile: true,
        userState: { user: null },
        pricingRequireAuth: false,
      }),
    );
    const sk = screen.getByTestId('skeleton-wrapper');
    expect(sk).toHaveAttribute('data-type', 'navigation');
    expect(sk).toHaveAttribute('data-count', '4');
    expect(screen.queryByText('Home')).not.toBeInTheDocument();
  });
});

describe('NewYearButton', () => {
  it('is absent outside the new-year window', () => {
    const { container } = render(
      React.createElement(NewYearButton, { isNewYear: false }),
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('fires the fireworks and stops them again after three seconds', () => {
    vi.useFakeTimers();
    render(React.createElement(NewYearButton, { isNewYear: true }));
    fireEvent.click(screen.getByText(/Happy New Year/));
    expect(fireworksInit).toHaveBeenCalledWith('root', {});
    expect(fireworksStart).toHaveBeenCalledTimes(1);
    expect(fireworksStop).not.toHaveBeenCalled();
    vi.advanceTimersByTime(3000);
    expect(fireworksStop).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });
});

describe('NotificationButton', () => {
  it('badges the unread count and opens the full notice list on click', () => {
    const onNoticeOpen = vi.fn();
    render(
      React.createElement(NotificationButton, {
        unreadCount: 4,
        onNoticeOpen,
        t,
      }),
    );
    expect(screen.getByTestId('badge')).toHaveAttribute('data-count', '4');
    expect(screen.getByTestId('badge')).toHaveAttribute('data-overflow', '99');
    fireEvent.click(screen.getByLabelText('系统公告'));
    expect(onNoticeOpen).toHaveBeenCalledTimes(1);
    // With unread items it goes straight to the modal, not the empty popover.
    expect(screen.queryByTestId('popover')).not.toBeInTheDocument();
  });

  it('drops the badge and toggles an "all caught up" popover when nothing is unread', () => {
    const onNoticeOpen = vi.fn();
    render(
      React.createElement(NotificationButton, {
        unreadCount: 0,
        onNoticeOpen,
        t,
      }),
    );
    expect(screen.queryByTestId('badge')).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText('系统公告'));
    expect(onNoticeOpen).not.toHaveBeenCalled();
    expect(screen.getByTestId('popover')).toHaveTextContent('暂无新通知');

    fireEvent.click(screen.getByLabelText('系统公告'));
    expect(screen.queryByTestId('popover')).not.toBeInTheDocument();
  });

  it('lets "view all" jump from the popover to the full notice list', () => {
    const onNoticeOpen = vi.fn();
    render(
      React.createElement(NotificationButton, {
        unreadCount: 0,
        onNoticeOpen,
        t,
      }),
    );
    fireEvent.click(screen.getByLabelText('系统公告'));
    fireEvent.click(screen.getByText('查看全部'));
    expect(onNoticeOpen).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId('popover')).not.toBeInTheDocument();
  });
});

describe('ThemeToggle', () => {
  it.each([
    ['light', 'icon-sun'],
    ['dark', 'icon-moon'],
    ['auto', 'icon-monitor'],
  ])('shows the %s glyph on the trigger', (theme, iconId) => {
    render(
      React.createElement(ThemeToggle, { theme, onThemeToggle: vi.fn(), t }),
    );
    // The menu always contains all three; the trigger button carries one.
    expect(
      screen
        .getByLabelText('切换主题')
        .querySelector(`[data-testid="${iconId}"]`),
    ).not.toBeNull();
  });

  it('falls back to the auto glyph for a theme it does not recognise', () => {
    render(
      React.createElement(ThemeToggle, {
        theme: 'solarized',
        onThemeToggle: vi.fn(),
        t,
      }),
    );
    expect(
      screen
        .getByLabelText('切换主题')
        .querySelector('[data-testid="icon-monitor"]'),
    ).not.toBeNull();
  });

  it('reports the picked theme back to the caller', () => {
    const onThemeToggle = vi.fn();
    render(
      React.createElement(ThemeToggle, { theme: 'light', onThemeToggle, t }),
    );
    fireEvent.click(screen.getByText('深色模式'));
    expect(onThemeToggle).toHaveBeenCalledWith('dark');
  });

  it('marks the active option and only the active option', () => {
    render(
      React.createElement(ThemeToggle, {
        theme: 'dark',
        onThemeToggle: vi.fn(),
        t,
      }),
    );
    const item = (label) =>
      screen.getByText(label).closest('[role="menuitem"]');
    expect(item('深色模式').className).toContain('font-semibold');
    expect(item('浅色模式').className).not.toContain('font-semibold');
  });

  it('discloses which theme "auto" currently resolves to, and only in auto mode', () => {
    actualTheme.current = 'dark';
    const { unmount } = render(
      React.createElement(ThemeToggle, {
        theme: 'auto',
        onThemeToggle: vi.fn(),
        t,
      }),
    );
    expect(screen.getByText(/当前跟随系统/).textContent).toContain('深色');
    expect(screen.getByTestId('divider')).toBeInTheDocument();
    unmount();

    render(
      React.createElement(ThemeToggle, {
        theme: 'light',
        onThemeToggle: vi.fn(),
        t,
      }),
    );
    expect(screen.queryByText(/当前跟随系统/)).not.toBeInTheDocument();
  });
});

describe('LanguageSelector', () => {
  it('offers every supported language with its flag', () => {
    render(
      React.createElement(LanguageSelector, {
        currentLang: 'en',
        onLanguageChange: vi.fn(),
        t,
      }),
    );
    ['中文', 'English', 'Français', '日本語', 'Русский', 'Tiếng Việt'].forEach(
      (label) => expect(screen.getByText(label)).toBeInTheDocument(),
    );
    ['CN', 'GB', 'FR', 'JP', 'RU', 'VN'].forEach((code) =>
      expect(screen.getByTestId(`flag-${code}`)).toBeInTheDocument(),
    );
  });

  it.each([
    ['中文', 'zh'],
    ['English', 'en'],
    ['Français', 'fr'],
    ['日本語', 'ja'],
    ['Русский', 'ru'],
    ['Tiếng Việt', 'vi'],
  ])('switches to %s using the code %s', (label, code) => {
    const onLanguageChange = vi.fn();
    render(
      React.createElement(LanguageSelector, {
        currentLang: 'en',
        onLanguageChange,
        t,
      }),
    );
    fireEvent.click(screen.getByText(label));
    expect(onLanguageChange).toHaveBeenCalledWith(code);
  });

  it('highlights exactly the active language', () => {
    render(
      React.createElement(LanguageSelector, {
        currentLang: 'ja',
        onLanguageChange: vi.fn(),
        t,
      }),
    );
    const item = (label) =>
      screen.getByText(label).closest('[role="menuitem"]');
    expect(item('日本語').className).toContain('font-semibold');
    expect(item('中文').className).not.toContain('font-semibold');
  });
});

describe('UserArea', () => {
  it('shows a user-area skeleton while the session is still resolving', () => {
    render(
      React.createElement(UserArea, {
        userState: { user: null },
        isLoading: true,
        t,
      }),
    );
    expect(screen.getByTestId('skeleton-wrapper')).toHaveAttribute(
      'data-type',
      'userArea',
    );
  });

  it('offers login and register to an anonymous visitor', () => {
    render(
      React.createElement(UserArea, {
        userState: { user: null },
        isLoading: false,
        isSelfUseMode: false,
        t,
      }),
    );
    expect(screen.getByText('登录').closest('a')).toHaveAttribute(
      'href',
      '/login',
    );
    expect(screen.getByText('注册').closest('a')).toHaveAttribute(
      'href',
      '/register',
    );
  });

  it('hides registration in self-use mode', () => {
    render(
      React.createElement(UserArea, {
        userState: { user: null },
        isLoading: false,
        isSelfUseMode: true,
        t,
      }),
    );
    expect(screen.getByText('登录')).toBeInTheDocument();
    expect(screen.queryByText('注册')).not.toBeInTheDocument();
  });

  it('shows the signed-in identity with a colour derived from the username', () => {
    render(
      React.createElement(UserArea, {
        userState: { user: { username: 'alice' } },
        isLoading: false,
        navigate: vi.fn(),
        logout: vi.fn(),
        t,
      }),
    );
    expect(stringToColor).toHaveBeenCalledWith('alice');
    expect(screen.getByText('A')).toHaveAttribute(
      'data-avatar-color',
      'indigo',
    );
    expect(screen.getByText('alice')).toBeInTheDocument();
    expect(screen.queryByText('登录')).not.toBeInTheDocument();
  });

  it.each([
    ['个人设置', '/console/personal'],
    ['令牌管理', '/console/token'],
    ['额度管理', '/console/topup'],
  ])('routes the %s menu entry to %s', (label, path) => {
    const navigate = vi.fn();
    render(
      React.createElement(UserArea, {
        userState: { user: { username: 'bob' } },
        isLoading: false,
        navigate,
        logout: vi.fn(),
        t,
      }),
    );
    fireEvent.click(screen.getByText(label));
    expect(navigate).toHaveBeenCalledWith(path);
  });

  it('wires sign-out to the logout callback, not to a route', () => {
    const navigate = vi.fn();
    const logout = vi.fn();
    render(
      React.createElement(UserArea, {
        userState: { user: { username: 'bob' } },
        isLoading: false,
        navigate,
        logout,
        t,
      }),
    );
    fireEvent.click(screen.getByText('退出'));
    expect(logout).toHaveBeenCalledTimes(1);
    expect(navigate).not.toHaveBeenCalled();
  });
});

describe('ActionButtons', () => {
  it('assembles the whole right-hand cluster and threads each prop to its owner', () => {
    const onNoticeOpen = vi.fn();
    const onThemeToggle = vi.fn();
    const onLanguageChange = vi.fn();
    const logout = vi.fn();
    render(
      React.createElement(ActionButtons, {
        isNewYear: false,
        unreadCount: 2,
        onNoticeOpen,
        theme: 'dark',
        onThemeToggle,
        currentLang: 'zh',
        onLanguageChange,
        userState: { user: { username: 'carol' } },
        isLoading: false,
        isMobile: false,
        isSelfUseMode: false,
        logout,
        navigate: vi.fn(),
        t,
      }),
    );
    expect(screen.getByTestId('streak-badge')).toBeInTheDocument();
    expect(screen.getByTestId('badge')).toHaveAttribute('data-count', '2');
    expect(screen.getByText('carol')).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('系统公告'));
    expect(onNoticeOpen).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByText('浅色模式'));
    expect(onThemeToggle).toHaveBeenCalledWith('light');
    fireEvent.click(screen.getByText('English'));
    expect(onLanguageChange).toHaveBeenCalledWith('en');
    fireEvent.click(screen.getByText('退出'));
    expect(logout).toHaveBeenCalledTimes(1);
  });

  it('omits the new-year easter egg outside the window', () => {
    render(
      React.createElement(ActionButtons, {
        isNewYear: false,
        unreadCount: 0,
        userState: { user: null },
        isLoading: false,
        t,
      }),
    );
    expect(screen.queryByText(/Happy New Year/)).not.toBeInTheDocument();
  });
});

describe('HeaderBar (index)', () => {
  const reset = () => {
    Object.assign(headerBarState, {
      isMobile: false,
      isConsoleRoute: true,
      isLoading: false,
      isNewYear: false,
      isSelfUseMode: false,
      isDemoSiteMode: false,
      theme: 'light',
      currentLang: 'en',
      userState: { user: { username: 'erin' } },
    });
    Object.assign(notificationsState, {
      noticeVisible: false,
      unreadCount: 0,
    });
  };

  beforeEach(reset);

  it('assembles logo, navigation and the action cluster in one header', () => {
    render(React.createElement(HeaderBar, { drawerOpen: false }));
    expect(screen.getByRole('banner')).toBeInTheDocument();
    expect(screen.getByText('Lurus Hub')).toBeInTheDocument();
    expect(screen.getByText('Home')).toBeInTheDocument();
    expect(screen.getByText('erin')).toBeInTheDocument();
    expect(screen.getByTestId('streak-badge')).toBeInTheDocument();
  });

  it('feeds the navigation hook the docs link the header hook produced', () => {
    render(React.createElement(HeaderBar, { drawerOpen: false }));
    expect(screen.getByText('Docs').closest('a')).toHaveAttribute(
      'href',
      'https://docs',
    );
  });

  it('passes the drawer state straight through to the header hook', () => {
    const onMobileMenuToggle = vi.fn();
    render(
      React.createElement(HeaderBar, { onMobileMenuToggle, drawerOpen: true }),
    );
    expect(useHeaderBarSpy).toHaveBeenCalledWith({
      onMobileMenuToggle,
      drawerOpen: true,
    });
  });

  it('keeps the notice modal mounted but closed until something opens it', () => {
    render(React.createElement(HeaderBar, { drawerOpen: false }));
    const modal = screen.getByTestId('notice-modal');
    expect(modal).toHaveAttribute('data-visible', 'false');
    expect(modal).toHaveAttribute('data-unread-keys', 'k1');
  });

  it('opens the notice modal on the in-app tab when nothing is unread', () => {
    notificationsState.noticeVisible = true;
    render(React.createElement(HeaderBar, { drawerOpen: false }));
    const modal = screen.getByTestId('notice-modal');
    expect(modal).toHaveAttribute('data-visible', 'true');
    expect(modal).toHaveAttribute('data-default-tab', 'inApp');
  });

  it('opens the notice modal on the system tab when announcements are unread', () => {
    notificationsState.noticeVisible = true;
    notificationsState.unreadCount = 3;
    render(React.createElement(HeaderBar, { drawerOpen: false }));
    expect(screen.getByTestId('notice-modal')).toHaveAttribute(
      'data-default-tab',
      'system',
    );
    // The same count also has to reach the bell badge, or the two disagree.
    expect(screen.getByTestId('badge')).toHaveAttribute('data-count', '3');
  });

  it('only shows the drawer toggle on the mobile console', () => {
    render(React.createElement(HeaderBar, { drawerOpen: false }));
    expect(screen.queryByLabelText('打开侧边栏')).not.toBeInTheDocument();
    cleanupRender();

    headerBarState.isMobile = true;
    render(React.createElement(HeaderBar, { drawerOpen: false }));
    expect(screen.getByLabelText('打开侧边栏')).toBeInTheDocument();
    // …and the logo yields its space to the sidebar on that surface.
    expect(screen.queryByText('Lurus Hub')).not.toBeInTheDocument();
  });
});
