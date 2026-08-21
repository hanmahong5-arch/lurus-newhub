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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

// NOTE: `marked` is deliberately NOT mocked. These panels pipe admin-authored
// markdown through marked and straight into dangerouslySetInnerHTML; a mocked
// parser would only prove the mock. The real library is a pure JS module and
// imports fine under jsdom.

vi.mock('@douyinfe/semi-ui', () => {
  const Collapse = ({ children, expandIcon, collapseIcon }) =>
    React.createElement(
      'div',
      { 'data-testid': 'collapse' },
      React.createElement('span', { 'data-testid': 'expand-icon' }, expandIcon),
      React.createElement(
        'span',
        { 'data-testid': 'collapse-icon' },
        collapseIcon,
      ),
      children,
    );
  Collapse.Panel = ({ header, children, itemKey }) =>
    React.createElement(
      'section',
      { 'data-item-key': itemKey },
      React.createElement('h4', null, header),
      children,
    );
  const Timeline = ({ children, mode }) =>
    React.createElement('ol', { 'data-mode': mode }, children);
  Timeline.Item = ({ children, time, type, extra }) =>
    React.createElement(
      'li',
      { 'data-tl-type': type, 'data-tl-time': time },
      children,
      React.createElement('aside', { 'data-testid': 'tl-extra' }, extra),
    );
  const Tabs = ({ children, activeKey, onChange, renderArrow }) =>
    React.createElement(
      'div',
      { 'data-testid': 'tabs', 'data-active': activeKey },
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'tab-next',
          onClick: () => onChange && onChange('__next__'),
        },
        'next',
      ),
      children,
    );
  const TabPane = ({ tab, itemKey, children }) =>
    React.createElement(
      'div',
      { 'data-pane': itemKey },
      React.createElement('span', null, tab),
      children,
    );
  const Form = ({ children }) => React.createElement('form', null, children);
  const field = (kind) => (props) =>
    React.createElement(
      'label',
      { 'data-field-kind': kind, 'data-field': props.field },
      props.label,
      React.createElement('input', {
        'data-testid': `field-${props.field}`,
        placeholder: props.placeholder,
        defaultValue:
          props.value ?? props.initValue ?? (props.optionList ? '' : ''),
        onChange: (e) => props.onChange && props.onChange(e.target.value),
      }),
    );
  Form.DatePicker = field('date');
  Form.Select = field('select');
  Form.Input = field('input');
  return {
    Card: ({ children, title, ...rest }) =>
      React.createElement(
        'section',
        rest,
        React.createElement('header', null, title),
        children,
      ),
    Tag: ({ children, color, onClick, prefixIcon }) =>
      React.createElement(
        'span',
        {
          'data-tag-color': color,
          onClick,
          role: onClick ? 'button' : undefined,
        },
        prefixIcon,
        children,
      ),
    Timeline,
    Empty: ({ title, description, image }) =>
      React.createElement(
        'div',
        { 'data-testid': 'empty' },
        image,
        React.createElement('strong', null, title),
        React.createElement('p', null, description),
      ),
    Avatar: ({ children, color }) =>
      React.createElement('span', { 'data-avatar-color': color }, children),
    Divider: () => React.createElement('hr', null),
    Collapse,
    Tabs,
    TabPane,
    Spin: ({ spinning, children }) =>
      React.createElement(
        'div',
        { 'data-spinning': spinning ? 'yes' : 'no' },
        children,
      ),
    Button: ({ icon, children, onClick, loading, 'aria-label': label }) =>
      React.createElement(
        'button',
        {
          type: 'button',
          onClick,
          'aria-label': label,
          'data-loading': loading ? 'true' : 'false',
        },
        icon,
        children,
      ),
    Modal: ({ visible, title, children, onOk, onCancel, size }) =>
      visible
        ? React.createElement(
            'div',
            { role: 'dialog', 'data-size': size },
            React.createElement('h2', null, title),
            children,
            React.createElement(
              'button',
              { type: 'button', 'data-testid': 'modal-ok', onClick: onOk },
              'ok',
            ),
            React.createElement(
              'button',
              {
                type: 'button',
                'data-testid': 'modal-cancel',
                onClick: onCancel,
              },
              'cancel',
            ),
          )
        : null,
    Form,
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconPlus: () => React.createElement('i', { 'data-testid': 'icon-plus' }),
  IconMinus: () => React.createElement('i', { 'data-testid': 'icon-minus' }),
}));

vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationConstruction: () =>
    React.createElement('i', { 'data-testid': 'illustration' }),
  IllustrationConstructionDark: () =>
    React.createElement('i', { 'data-testid': 'illustration-dark' }),
}));

vi.mock('lucide-react', () => ({
  Bell: () => React.createElement('i', { 'data-testid': 'icon-bell' }),
  HelpCircle: () => React.createElement('i', { 'data-testid': 'icon-help' }),
  Server: () => React.createElement('i', { 'data-testid': 'icon-server' }),
  Gauge: () => React.createElement('i', { 'data-testid': 'icon-gauge' }),
  ExternalLink: () => React.createElement('i', { 'data-testid': 'icon-ext' }),
  PieChart: () => React.createElement('i', { 'data-testid': 'icon-pie' }),
  RefreshCw: () => React.createElement('i', { 'data-testid': 'icon-refresh' }),
  Search: () => React.createElement('i', { 'data-testid': 'icon-search' }),
}));

vi.mock('@visactor/react-vchart', () => ({
  VChart: ({ spec }) =>
    React.createElement('div', {
      'data-testid': 'vchart',
      'data-spec': JSON.stringify(spec ?? null),
    }),
}));

vi.mock('../common/ui/ScrollableContainer', () => ({
  default: ({ children, maxHeight }) =>
    React.createElement(
      'div',
      { 'data-testid': 'scroll', 'data-max-height': maxHeight },
      children,
    ),
}));

import AnnouncementsPanel from './AnnouncementsPanel';
import ApiInfoPanel from './ApiInfoPanel';
import FaqPanel from './FaqPanel';
import ChartsPanel from './ChartsPanel';
import DashboardHeader from './DashboardHeader';
import UptimePanel from './UptimePanel';
import SearchModal from './modals/SearchModal';

const t = (k) => k;

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('AnnouncementsPanel', () => {
  const common = {
    announcementLegendData: [],
    CARD_PROPS: {},
    ILLUSTRATION_SIZE: {},
    t,
  };

  it('shows the empty-state illustration and guidance when there are no announcements', () => {
    render(
      React.createElement(AnnouncementsPanel, {
        ...common,
        announcementData: [],
      }),
    );
    expect(screen.getByTestId('empty')).toHaveTextContent('暂无系统公告');
    expect(screen.queryByRole('list')).not.toBeInTheDocument();
  });

  it('renders markdown content, the severity type and a relative+absolute timestamp', () => {
    render(
      React.createElement(AnnouncementsPanel, {
        ...common,
        announcementData: [
          {
            content: '**maintenance** tonight',
            time: '2026-08-20 09:00',
            relative: '2h ago',
            type: 'warning',
          },
        ],
      }),
    );
    const item = screen.getByRole('listitem');
    expect(item).toHaveAttribute('data-tl-type', 'warning');
    expect(item).toHaveAttribute('data-tl-time', '2h ago 2026-08-20 09:00');
    // marked really ran: the ** ** became a <strong>.
    expect(item.querySelector('strong')).toHaveTextContent('maintenance');
  });

  it('falls back to the default timeline type and omits the relative prefix', () => {
    render(
      React.createElement(AnnouncementsPanel, {
        ...common,
        announcementData: [{ content: 'plain', time: 'T1' }],
      }),
    );
    const item = screen.getByRole('listitem');
    expect(item).toHaveAttribute('data-tl-type', 'default');
    expect(item).toHaveAttribute('data-tl-time', 'T1');
    expect(screen.getByTestId('tl-extra')).toBeEmptyDOMElement();
  });

  it('renders the extra block only for announcements that carry one', () => {
    render(
      React.createElement(AnnouncementsPanel, {
        ...common,
        announcementData: [{ content: 'c', time: 'T', extra: '_note_' }],
      }),
    );
    expect(
      screen.getByTestId('tl-extra').querySelector('em'),
    ).toHaveTextContent('note');
  });

  it('maps each legend colour name to a distinct swatch and unknown names to grey', () => {
    render(
      React.createElement(AnnouncementsPanel, {
        ...common,
        announcementData: [],
        announcementLegendData: [
          { color: 'grey', label: 'g' },
          { color: 'blue', label: 'b' },
          { color: 'green', label: 'gr' },
          { color: 'orange', label: 'o' },
          { color: 'red', label: 'r' },
          { color: 'chartreuse', label: 'unknown' },
        ],
      }),
    );
    const swatch = (label) =>
      screen.getByText(label).parentElement.querySelector('div');
    expect(swatch('g')).toHaveStyle({ backgroundColor: '#8b9aa7' });
    expect(swatch('b')).toHaveStyle({ backgroundColor: '#3b82f6' });
    expect(swatch('gr')).toHaveStyle({ backgroundColor: '#10b981' });
    expect(swatch('o')).toHaveStyle({ backgroundColor: '#f59e0b' });
    expect(swatch('r')).toHaveStyle({ backgroundColor: '#ef4444' });
    // Unknown colour must degrade to grey, not to "no colour at all".
    expect(swatch('unknown')).toHaveStyle({ backgroundColor: '#8b9aa7' });
  });

  // DEFECT (security): announcement bodies go
  // `marked.parse(...) -> dangerouslySetInnerHTML` with no sanitiser anywhere in
  // between, and marked passes raw HTML through by design. Whoever can edit the
  // announcement text (a tenant admin in this multi-tenant hub, or anyone who
  // reaches the settings write path) gets script execution in every other
  // user's authenticated console session. FaqPanel has the same shape.
  // INVARIANT, not a pin on the defect above. Asserting that the `onerror`
  // handler survives would turn red the instant a sanitiser is added, making
  // the security fix read as a regression. What must hold either side of that
  // fix is that the announcement body really does go through the markdown
  // pipeline and reach the user — a sanitiser keeps <strong>, it only strips
  // handlers. This still catches a dropped `marked.parse` or a dropped prop.
  it('renders the announcement body through the markdown pipeline', () => {
    render(
      React.createElement(AnnouncementsPanel, {
        ...common,
        announcementData: [{ content: '**shipping notice**', time: 'T' }],
      }),
    );
    const item = screen.getByRole('listitem');
    expect(item.querySelector('strong')).not.toBeNull();
    expect(item.textContent).toContain('shipping notice');
  });

  it('announcement HTML must be sanitised before it reaches the DOM', () => {
    render(
      React.createElement(AnnouncementsPanel, {
        ...common,
        announcementData: [
          { content: '<img src=x onerror="window.__pwned=1">', time: 'T' },
        ],
      }),
    );
    const injected = screen.getByRole('listitem').querySelector('img');
    expect(injected?.getAttribute('onerror') ?? null).toBeNull();
  });
});

describe('FaqPanel', () => {
  const common = {
    CARD_PROPS: {},
    FLEX_CENTER_GAP2: '',
    ILLUSTRATION_SIZE: {},
    t,
  };

  it('shows the empty state when no FAQ entries are configured', () => {
    render(React.createElement(FaqPanel, { ...common, faqData: [] }));
    expect(screen.getByTestId('empty')).toHaveTextContent('暂无常见问答');
    expect(screen.queryByTestId('collapse')).not.toBeInTheDocument();
  });

  it('renders one collapsible panel per question with the answer as markdown', () => {
    render(
      React.createElement(FaqPanel, {
        ...common,
        faqData: [
          { question: 'Q1', answer: '**bold**' },
          { question: 'Q2', answer: '' },
        ],
      }),
    );
    expect(screen.getByText('Q1')).toBeInTheDocument();
    expect(screen.getByText('Q2')).toBeInTheDocument();
    expect(
      screen.getByText('Q1').parentElement.querySelector('strong'),
    ).toHaveTextContent('bold');
    // Expand/collapse affordances are present, not stubbed away.
    expect(screen.getByTestId('icon-plus')).toBeInTheDocument();
    expect(screen.getByTestId('icon-minus')).toBeInTheDocument();
  });

  it('survives an entry with no answer at all', () => {
    render(
      React.createElement(FaqPanel, {
        ...common,
        faqData: [{ question: 'Q-only' }],
      }),
    );
    expect(screen.getByText('Q-only')).toBeInTheDocument();
  });

  // DEFECT (security): same unsanitised markdown -> innerHTML path as
  // AnnouncementsPanel. Recorded once per component so a fix cannot land in one
  // and quietly skip the other.
  // Same reasoning as the announcement invariant above: assert that the answer
  // reaches the DOM through the markdown pipeline (true before and after
  // sanitisation) rather than pinning the surviving handler (true only while
  // the hole is open).
  it('renders the FAQ answer through the markdown pipeline', () => {
    render(
      React.createElement(FaqPanel, {
        ...common,
        faqData: [{ question: 'Q', answer: '**escalate to support**' }],
      }),
    );
    expect(document.querySelector('strong')).not.toBeNull();
    expect(document.body.textContent).toContain('escalate to support');
  });

  it('FAQ answer HTML must be sanitised before it reaches the DOM', () => {
    render(
      React.createElement(FaqPanel, {
        ...common,
        faqData: [{ question: 'Q', answer: '<img src=x onerror="alert(1)">' }],
      }),
    );
    expect(document.querySelector('img[onerror]')).toBeNull();
  });
});

describe('ApiInfoPanel', () => {
  const common = {
    handleCopyUrl: vi.fn(),
    handleSpeedTest: vi.fn(),
    CARD_PROPS: {},
    FLEX_CENTER_GAP2: '',
    ILLUSTRATION_SIZE: {},
    t,
  };
  const api = {
    id: 1,
    route: 'openai',
    url: 'https://api.example.com/v1',
    description: 'main endpoint',
    color: 'blue',
  };

  it('shows the empty state when no endpoints are configured', () => {
    render(React.createElement(ApiInfoPanel, { ...common, apiInfoData: [] }));
    expect(screen.getByTestId('empty')).toHaveTextContent('暂无API信息');
  });

  it('lists route, url and description and seeds the avatar from the route prefix', () => {
    render(
      React.createElement(ApiInfoPanel, { ...common, apiInfoData: [api] }),
    );
    expect(screen.getByText('openai')).toBeInTheDocument();
    expect(screen.getByText('https://api.example.com/v1')).toBeInTheDocument();
    expect(screen.getByText('main endpoint')).toBeInTheDocument();
    expect(screen.getByText('op')).toHaveAttribute('data-avatar-color', 'blue');
  });

  it('copies the endpoint url when the url line is clicked', () => {
    const handleCopyUrl = vi.fn();
    render(
      React.createElement(ApiInfoPanel, {
        ...common,
        handleCopyUrl,
        apiInfoData: [api],
      }),
    );
    fireEvent.click(screen.getByText('https://api.example.com/v1'));
    expect(handleCopyUrl).toHaveBeenCalledWith('https://api.example.com/v1');
  });

  it('runs a speed test against the endpoint url', () => {
    const handleSpeedTest = vi.fn();
    render(
      React.createElement(ApiInfoPanel, {
        ...common,
        handleSpeedTest,
        apiInfoData: [api],
      }),
    );
    fireEvent.click(screen.getByText('测速'));
    expect(handleSpeedTest).toHaveBeenCalledWith('https://api.example.com/v1');
  });

  // This is the counter-example that makes the two window.open findings in
  // WalletCard/TopUp defects rather than house style: the same codebase does
  // pass the feature string here.
  it('opens the endpoint in a new tab with noopener/noreferrer', () => {
    const open = vi.fn();
    vi.stubGlobal('open', open);
    render(
      React.createElement(ApiInfoPanel, { ...common, apiInfoData: [api] }),
    );
    fireEvent.click(screen.getByText('跳转'));
    expect(open).toHaveBeenCalledWith(
      'https://api.example.com/v1',
      '_blank',
      'noopener,noreferrer',
    );
  });
});

describe('ChartsPanel', () => {
  const specs = {
    spec_line: { id: 'line' },
    spec_model_line: { id: 'model' },
    spec_pie: { id: 'pie' },
    spec_rank_bar: { id: 'rank' },
  };
  const common = {
    ...specs,
    setActiveChartTab: vi.fn(),
    CARD_PROPS: {},
    CHART_CONFIG: {},
    FLEX_CENTER_GAP2: '',
    hasApiInfoPanel: false,
    t,
  };

  it.each([
    ['1', 'line'],
    ['2', 'model'],
    ['3', 'pie'],
    ['4', 'rank'],
  ])('renders exactly the chart for tab %s', (tab, id) => {
    render(
      React.createElement(ChartsPanel, { ...common, activeChartTab: tab }),
    );
    const charts = screen.getAllByTestId('vchart');
    expect(charts).toHaveLength(1);
    expect(JSON.parse(charts[0].getAttribute('data-spec'))).toEqual({ id });
  });

  it('renders no chart for an unknown tab key rather than defaulting to one', () => {
    render(
      React.createElement(ChartsPanel, { ...common, activeChartTab: '9' }),
    );
    expect(screen.queryByTestId('vchart')).not.toBeInTheDocument();
  });

  it('forwards tab changes to the parent', () => {
    const setActiveChartTab = vi.fn();
    render(
      React.createElement(ChartsPanel, {
        ...common,
        setActiveChartTab,
        activeChartTab: '1',
      }),
    );
    fireEvent.click(screen.getByTestId('tab-next'));
    expect(setActiveChartTab).toHaveBeenCalledWith('__next__');
  });

  it('widens itself only when the API info panel shares the row', () => {
    const { container, unmount } = render(
      React.createElement(ChartsPanel, {
        ...common,
        activeChartTab: '1',
        hasApiInfoPanel: true,
      }),
    );
    expect(container.querySelector('section').className).toContain(
      'lg:col-span-3',
    );
    unmount();
    const { container: c2 } = render(
      React.createElement(ChartsPanel, { ...common, activeChartTab: '1' }),
    );
    expect(c2.querySelector('section').className).not.toContain(
      'lg:col-span-3',
    );
  });
});

describe('DashboardHeader', () => {
  const common = {
    getGreeting: 'good evening',
    greetingVisible: true,
    showSearchModal: vi.fn(),
    refresh: vi.fn(),
    loading: false,
    t,
  };

  it('shows the greeting and both actions with accessible labels', () => {
    render(React.createElement(DashboardHeader, common));
    expect(screen.getByText('good evening')).toBeInTheDocument();
    expect(screen.getByLabelText('搜索')).toBeInTheDocument();
    expect(screen.getByLabelText('刷新')).toBeInTheDocument();
  });

  it('fades the greeting out when it is not visible', () => {
    const { rerender } = render(React.createElement(DashboardHeader, common));
    expect(screen.getByText('good evening')).toHaveStyle({ opacity: '1' });
    rerender(
      React.createElement(DashboardHeader, {
        ...common,
        greetingVisible: false,
      }),
    );
    expect(screen.getByText('good evening')).toHaveStyle({ opacity: '0' });
  });

  it('wires search and refresh to their own handlers', () => {
    const showSearchModal = vi.fn();
    const refresh = vi.fn();
    render(
      React.createElement(DashboardHeader, {
        ...common,
        showSearchModal,
        refresh,
      }),
    );
    fireEvent.click(screen.getByLabelText('搜索'));
    fireEvent.click(screen.getByLabelText('刷新'));
    expect(showSearchModal).toHaveBeenCalledTimes(1);
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('marks the refresh button as busy while a refresh is running', () => {
    render(React.createElement(DashboardHeader, { ...common, loading: true }));
    expect(screen.getByLabelText('刷新')).toHaveAttribute(
      'data-loading',
      'true',
    );
    expect(screen.getByLabelText('搜索')).toHaveAttribute(
      'data-loading',
      'false',
    );
  });
});

describe('UptimePanel', () => {
  const renderMonitorList = (monitors) =>
    React.createElement(
      'ul',
      { 'data-testid': 'monitors' },
      (monitors ?? []).map((m) =>
        React.createElement('li', { key: m }, String(m)),
      ),
    );
  const common = {
    uptimeLoading: false,
    activeUptimeTab: 'core',
    setActiveUptimeTab: vi.fn(),
    loadUptimeData: vi.fn(),
    uptimeLegendData: [{ color: '#0f0', label: 'up' }],
    renderMonitorList,
    CARD_PROPS: {},
    ILLUSTRATION_SIZE: {},
    t,
  };

  it('shows the empty state and hides the legend when there is no monitor data', () => {
    render(React.createElement(UptimePanel, { ...common, uptimeData: [] }));
    expect(screen.getByTestId('empty')).toHaveTextContent('暂无监控数据');
    expect(screen.queryByText('up')).not.toBeInTheDocument();
  });

  it('renders a single group flat, with no tab strip', () => {
    render(
      React.createElement(UptimePanel, {
        ...common,
        uptimeData: [{ categoryName: 'core', monitors: ['a', 'b'] }],
      }),
    );
    expect(screen.queryByTestId('tabs')).not.toBeInTheDocument();
    expect(screen.getByTestId('monitors').children).toHaveLength(2);
    expect(screen.getByText('up')).toBeInTheDocument();
  });

  it('splits multiple groups into tabs and counts the monitors in each', () => {
    render(
      React.createElement(UptimePanel, {
        ...common,
        uptimeData: [
          { categoryName: 'core', monitors: ['a', 'b'] },
          { categoryName: 'edge' },
        ],
      }),
    );
    expect(screen.getByTestId('tabs')).toHaveAttribute('data-active', 'core');
    // The active group's badge is highlighted, the other is not.
    expect(screen.getByText('2')).toHaveAttribute('data-tag-color', 'red');
    // A group with no monitors array must read 0, not blank or NaN.
    expect(screen.getByText('0')).toHaveAttribute('data-tag-color', 'grey');
  });

  it('marks the panel busy and still shows the refresh control while loading', () => {
    render(
      React.createElement(UptimePanel, {
        ...common,
        uptimeLoading: true,
        uptimeData: [],
      }),
    );
    expect(document.querySelector('[data-spinning="yes"]')).not.toBeNull();
  });

  it('reloads on demand and forwards tab switches', () => {
    const loadUptimeData = vi.fn();
    const setActiveUptimeTab = vi.fn();
    render(
      React.createElement(UptimePanel, {
        ...common,
        loadUptimeData,
        setActiveUptimeTab,
        uptimeData: [
          { categoryName: 'core', monitors: [] },
          { categoryName: 'edge', monitors: [] },
        ],
      }),
    );
    fireEvent.click(screen.getByTestId('tab-next'));
    expect(setActiveUptimeTab).toHaveBeenCalledWith('__next__');
    fireEvent.click(document.querySelector('button[data-loading]'));
    expect(loadUptimeData).toHaveBeenCalledTimes(1);
  });
});

describe('SearchModal', () => {
  const common = {
    handleSearchConfirm: vi.fn(),
    handleCloseModal: vi.fn(),
    isMobile: false,
    isAdminUser: false,
    inputs: {
      start_timestamp: 'S',
      end_timestamp: 'E',
      username: 'bob',
    },
    dataExportDefaultTime: 'hour',
    timeOptions: [{ label: 'hour', value: 'hour' }],
    handleInputChange: vi.fn(),
    t,
  };

  it('renders nothing while hidden', () => {
    const { container } = render(
      React.createElement(SearchModal, {
        ...common,
        searchModalVisible: false,
      }),
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('exposes the time range and granularity fields when open', () => {
    render(
      React.createElement(SearchModal, { ...common, searchModalVisible: true }),
    );
    expect(screen.getByRole('dialog')).toHaveAttribute('data-size', 'small');
    expect(screen.getByTestId('field-start_timestamp')).toHaveValue('S');
    expect(screen.getByTestId('field-end_timestamp')).toHaveValue('E');
    expect(
      screen.getByTestId('field-data_export_default_time'),
    ).toBeInTheDocument();
  });

  it('hides the username filter from non-admins and shows it to admins', () => {
    const { unmount } = render(
      React.createElement(SearchModal, { ...common, searchModalVisible: true }),
    );
    expect(screen.queryByTestId('field-username')).not.toBeInTheDocument();
    unmount();

    render(
      React.createElement(SearchModal, {
        ...common,
        searchModalVisible: true,
        isAdminUser: true,
      }),
    );
    expect(screen.getByTestId('field-username')).toHaveValue('bob');
  });

  it('reports each edited field under its own key', () => {
    const handleInputChange = vi.fn();
    render(
      React.createElement(SearchModal, {
        ...common,
        handleInputChange,
        searchModalVisible: true,
        isAdminUser: true,
      }),
    );
    fireEvent.change(screen.getByTestId('field-start_timestamp'), {
      target: { value: 'S2' },
    });
    fireEvent.change(screen.getByTestId('field-username'), {
      target: { value: 'carol' },
    });
    expect(handleInputChange).toHaveBeenNthCalledWith(
      1,
      'S2',
      'start_timestamp',
    );
    expect(handleInputChange).toHaveBeenNthCalledWith(2, 'carol', 'username');
  });

  it('goes full width on mobile', () => {
    render(
      React.createElement(SearchModal, {
        ...common,
        searchModalVisible: true,
        isMobile: true,
      }),
    );
    expect(screen.getByRole('dialog')).toHaveAttribute(
      'data-size',
      'full-width',
    );
  });

  it('separates confirm from cancel', () => {
    const handleSearchConfirm = vi.fn();
    const handleCloseModal = vi.fn();
    render(
      React.createElement(SearchModal, {
        ...common,
        handleSearchConfirm,
        handleCloseModal,
        searchModalVisible: true,
      }),
    );
    fireEvent.click(screen.getByTestId('modal-ok'));
    expect(handleSearchConfirm).toHaveBeenCalledTimes(1);
    expect(handleCloseModal).not.toHaveBeenCalled();

    fireEvent.click(screen.getByTestId('modal-cancel'));
    expect(handleCloseModal).toHaveBeenCalledTimes(1);
  });
});
