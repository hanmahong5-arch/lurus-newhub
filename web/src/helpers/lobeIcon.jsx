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
import { useEffect, useState } from 'react';
import { Avatar } from '@douyinfe/semi-ui';

// Vendor logos, loaded off the critical path.
//
// This module exists so that `@lobehub/icons` is reachable ONLY through the
// dynamic import below. It used to be a static `import * as LobeIcons` in
// render.jsx, which helpers/index.js re-exports, so the whole vendor-logo pack
// sat in the entry chunk that every visitor downloads before the first paint —
// and because the lookup is by runtime string ('OpenAI.Color' -> icons.OpenAI),
// no bundler could shake the unused logos out. src/z9_entry_icon_barrel.test.js
// is the structural guard: it walks the entry's synchronous import graph and
// fails if any module in it imports '@lobehub/icons' statically again.
//
// The trade for moving it off the critical path is one frame of placeholder:
// an icon renders the initial-letter <Avatar> that unknown icon names have
// always rendered, then swaps to the real logo when the chunk lands.

let loadedIcons = null;
let loadPromise = null;
let warnedAboutLoadFailure = false;

// Memoised at module scope: every icon on the page shares one chunk request,
// and once it has resolved a newly mounted icon renders the logo with no
// placeholder frame at all.
//
// Module-private on purpose — LobeHubIcon is the only caller, and an exported
// loader would be a second way to reach the pack that the entry-graph gate
// (src/z9_entry_icon_barrel.test.js) reasons about.
function loadLobeIcons() {
  if (loadedIcons) return Promise.resolve(loadedIcons);
  if (!loadPromise) {
    loadPromise = import('@lobehub/icons')
      .then((mod) => {
        loadedIcons = mod;
        return mod;
      })
      .catch((err) => {
        // A failed chunk fetch must not pin every future icon to the
        // placeholder: drop the memo so the next mount retries.
        loadPromise = null;
        // ...and it must not be silent. The realistic trigger is ordinary: a
        // deploy rotates the hashed chunk names while a tab is open, this
        // request 404s, and every vendor logo on the page quietly degrades to
        // an initial letter. Once per page load, not once per icon — there can
        // be dozens on screen and they all share this promise.
        if (!warnedAboutLoadFailure) {
          warnedAboutLoadFailure = true;
          // eslint-disable-next-line no-console
          console.warn(
            'vendor icon pack failed to load; icons fall back to their initial letter. ' +
              'A stale tab after a deploy is the usual cause — reload to recover.',
            err,
          );
        }
        throw err;
      });
  }
  return loadPromise;
}

function isRenderableIcon(candidate) {
  return (
    !!candidate &&
    (typeof candidate === 'function' || typeof candidate === 'object')
  );
}

// The icon name comes from operator-editable model/vendor rows, so it is an
// arbitrary string being used as a property key on a module namespace. Read it
// as an own property only: that keeps a name like 'toString' off the prototype
// chain of the icon functions the sub-component lookup walks, and it degrades
// to the placeholder for a name the pack does not export instead of whatever
// the host object's getter does with it.
function ownMember(container, key) {
  if (!container || !key) return undefined;
  return Object.prototype.hasOwnProperty.call(container, key)
    ? container[key]
    : undefined;
}

// Mirrors what the synchronous version did with the loaded namespace: prefer
// the dotted sub-component (Claude.Color), and when the icon pack has no such
// member treat that segment as a bare boolean prop on the base icon instead.
//
// Exported because it is the half of the old getLobeHubIcon that a test can
// still check against the real pack without waiting for a chunk: the
// descriptor round-trip assertions in h1_render_tags.test.jsx pair the
// name/sub this module parsed out with the component the pack exports.
export function resolveLobeIcon(icons, name, sub) {
  const BaseIcon = ownMember(icons, name);
  if (!isRenderableIcon(BaseIcon)) return null;
  if (sub) {
    const SubIcon = ownMember(BaseIcon, sub);
    if (isRenderableIcon(SubIcon))
      return { Component: SubIcon, subProps: null };
    return { Component: BaseIcon, subProps: { [sub]: true } };
  }
  return { Component: BaseIcon, subProps: null };
}

/**
 * 厂商图标；图标包到达前先渲染首字母占位。
 * @param {string} name - 图标包顶层导出名，如 'OpenAI'
 * @param {string} [sub] - 点号子组件名，如 'Color'
 */
export function LobeHubIcon({ name, sub, ...iconProps }) {
  const [icons, setIcons] = useState(loadedIcons);

  useEffect(() => {
    if (icons) return undefined;
    let cancelled = false;
    loadLobeIcons().then(
      (mod) => {
        if (!cancelled) setIcons(mod);
      },
      () => {
        // Keep the placeholder. loadLobeIcons has already warned once and
        // cleared its memo, so the next mount re-issues the import rather than
        // pinning the whole console to initial letters for the session.
      },
    );
    return () => {
      cancelled = true;
    };
  }, [icons]);

  const resolved = icons ? resolveLobeIcon(icons, name, sub) : null;
  if (!resolved) {
    const firstLetter = String(name || '')
      .charAt(0)
      .toUpperCase();
    return <Avatar size='extra-extra-small'>{firstLetter}</Avatar>;
  }

  const { Component, subProps } = resolved;
  return <Component {...subProps} {...iconProps} />;
}

// 按点号切分图标描述符，但花括号/引号内部的点号属于取值本身：直接 split('.')
// 会把 size={1.5} 这样的小数撕成 '{1' 和 '5}' 两段。
function splitIconDescriptor(descriptor) {
  const segments = [];
  let current = '';
  let depth = 0;
  let quote = null;

  for (const ch of descriptor) {
    if (quote) {
      if (ch === quote) quote = null;
    } else if (ch === '"' || ch === "'") {
      quote = ch;
    } else if (ch === '{') {
      depth++;
    } else if (ch === '}') {
      if (depth > 0) depth--;
    } else if (ch === '.' && depth === 0) {
      segments.push(current);
      current = '';
      continue;
    }
    current += ch;
  }
  segments.push(current);

  return segments;
}

// 解析点号链式属性，形如：key={...}、key='...'、key="..."、key=123、key、key=true/false
function parseValue(raw) {
  if (raw == null) return true;
  let v = String(raw).trim();
  // 去除一层花括号包裹
  if (v.startsWith('{') && v.endsWith('}')) {
    v = v.slice(1, -1).trim();
  }
  // 去除引号
  if (
    (v.startsWith('"') && v.endsWith('"')) ||
    (v.startsWith("'") && v.endsWith("'"))
  ) {
    return v.slice(1, -1);
  }
  // 布尔
  if (v === 'true') return true;
  if (v === 'false') return false;
  // 数字
  if (/^-?\d+(?:\.\d+)?$/.test(v)) return Number(v);
  // 其他原样返回字符串
  return v;
}

function parseChainedProps(segments, startIndex) {
  const props = {};
  for (let i = startIndex; i < segments.length; i++) {
    const seg = segments[i];
    if (!seg) continue;
    const eqIdx = seg.indexOf('=');
    if (eqIdx === -1) {
      props[seg.trim()] = true;
      continue;
    }
    const key = seg.slice(0, eqIdx).trim();
    const valRaw = seg.slice(eqIdx + 1).trim();
    props[key] = parseValue(valRaw);
  }
  return props;
}

/**
 * 根据图标名称动态获取 LobeHub 图标组件
 * 支持：
 * - 基础："OpenAI"、"OpenAI.Color" 等
 * - 额外属性（点号链式）："OpenAI.Avatar.type={'platform'}"、"OpenRouter.Avatar.shape={'square'}"
 * - 继续兼容第二参数 size；若字符串里有 size=，以字符串为准
 * @param {string} iconName - 图标名称/描述
 * @param {number} size - 图标大小，默认为 14；传 null 表示用图标自身默认尺寸
 * @returns {JSX.Element} - 对应的图标组件或 Avatar
 */
export function getLobeHubIcon(iconName, size = 14) {
  if (typeof iconName === 'string') iconName = iconName.trim();
  // 如果没有图标名称，返回 Avatar
  if (!iconName) {
    return <Avatar size='extra-extra-small'>?</Avatar>;
  }

  const segments = splitIconDescriptor(String(iconName));
  const name = segments[0];

  // Which descriptor segment is a sub-component and which is a chained prop is
  // decided here, before the icon pack exists, so it cannot ask the pack the
  // way the synchronous version did. A segment is taken as a sub-component
  // when it carries no '=' and starts upper-case — React's own component
  // naming rule, and the shape of every sub-export in this pack (Color,
  // Avatar, Text, Combine). LobeHubIcon still asks the loaded pack afterwards,
  // so a capitalised segment that turns out NOT to be a sub-component
  // ('OpenAI.Foo') lands as a boolean prop on the base icon exactly as before.
  const second = segments[1];
  const isSubComponent =
    !!second && !second.includes('=') && /^[A-Z]/.test(second);
  const sub = isSubComponent ? second : undefined;

  const props = parseChainedProps(segments, isSubComponent ? 2 : 1);

  // 兼容第二参数 size，若字符串中未显式指定 size，则使用函数入参
  if (props.size == null && size != null) props.size = size;

  return <LobeHubIcon name={name} sub={sub} {...props} />;
}
