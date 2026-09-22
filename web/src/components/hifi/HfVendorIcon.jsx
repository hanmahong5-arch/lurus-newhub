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

import React, { useEffect, useState } from 'react';

// The icon pack is loaded on first use, not imported: it is large, and the
// shared helper (helpers/lobeIcon.jsx) imports Semi UI's barrel, which pulls
// lottie-web into every v2 page that renders a logo (and crashes jsdom).
let iconPack = null;
let iconPackPromise = null;
const loadIconPack = () => {
  if (iconPack) return Promise.resolve(iconPack);
  if (!iconPackPromise) {
    iconPackPromise = import('@lobehub/icons').then(
      (mod) => {
        iconPack = mod;
        return mod;
      },
      (err) => {
        iconPackPromise = null;
        throw err;
      },
    );
  }
  return iconPackPromise;
};

const pick = (container, key) =>
  container && key && Object.prototype.hasOwnProperty.call(container, key)
    ? container[key]
    : undefined;

const PackIcon = ({ name, sub, size }) => {
  const [pack, setPack] = useState(iconPack);
  useEffect(() => {
    if (pack) return undefined;
    let cancelled = false;
    loadIconPack().then(
      (mod) => !cancelled && setPack(mod),
      () => {},
    );
    return () => {
      cancelled = true;
    };
  }, [pack]);
  const Base = pick(pack, name);
  const Comp = (sub && pick(Base, sub)) || Base;
  if (!Comp || (typeof Comp !== 'function' && typeof Comp !== 'object')) {
    return null;
  }
  return <Comp size={size} />;
};

// Model-name prefixes → icon-pack export (+ its colour variant where one
// exists). First match wins, so a more specific prefix goes before a
// shorter one that would also match. Only names verified to exist in
// @lobehub/icons are listed.
const MODEL_RULES = [
  [
    /^(gpt|o1|o3|o4|chatgpt|dall-e|whisper|tts-1|text-embedding|davinci|babbage)/,
    'OpenAI',
    null,
  ],
  [/^claude/, 'Claude', 'Color'],
  [/^(gemini|gemma|imagen|veo)/, 'Gemini', 'Color'],
  [/^deepseek/, 'DeepSeek', 'Color'],
  [/^(qwen|qwq|qvq)/, 'Qwen', 'Color'],
  [/^(glm|chatglm|cogview|cogvideo)/, 'Zhipu', 'Color'],
  [/^kimi/, 'Kimi', 'Color'],
  [/^moonshot/, 'Moonshot', null],
  [/^doubao/, 'Doubao', 'Color'],
  [/^ernie/, 'Wenxin', 'Color'],
  [/^hunyuan/, 'Hunyuan', 'Color'],
  [/^(llama|meta-llama)/, 'Meta', 'Color'],
  [/^(mistral|mixtral|codestral|ministral|pixtral)/, 'Mistral', 'Color'],
  [/^grok/, 'Grok', null],
  [/^(suno|chirp)/, 'Suno', null],
  [/^(mj_|midjourney|swap_face)/, 'Midjourney', null],
  [/^yi-/, 'Yi', 'Color'],
  [/^(minimax|abab)/, 'Minimax', 'Color'],
  [/^(spark|generalv)/, 'Spark', 'Color'],
  [/^(command|cohere|rerank-)/, 'Cohere', 'Color'],
  [/^360/, 'Ai360', 'Color'],
  [/^baichuan/, 'Baichuan', 'Color'],
  [/^step-/, 'Stepfun', 'Color'],
  [/^(kling)/, 'Kling', 'Color'],
  [/^(jimeng)/, 'Jimeng', 'Color'],
  [/^(flux)/, 'Flux', null],
  [/^(stable-|sd3|sdxl)/, 'Stability', 'Color'],
  [/^(sonar|pplx)/, 'Perplexity', 'Color'],
  [/^jina/, 'Jina', null],
];

// Vendor / owned_by names (as the catalogue and pricing report them).
const VENDOR_RULES = {
  openai: ['OpenAI', null],
  anthropic: ['Claude', 'Color'],
  claude: ['Claude', 'Color'],
  google: ['Gemini', 'Color'],
  gemini: ['Gemini', 'Color'],
  deepseek: ['DeepSeek', 'Color'],
  qwen: ['Qwen', 'Color'],
  alibaba: ['Qwen', 'Color'],
  zhipu: ['Zhipu', 'Color'],
  moonshot: ['Moonshot', null],
  bytedance: ['Doubao', 'Color'],
  doubao: ['Doubao', 'Color'],
  volcengine: ['Volcengine', 'Color'],
  baidu: ['Wenxin', 'Color'],
  tencent: ['Hunyuan', 'Color'],
  meta: ['Meta', 'Color'],
  mistral: ['Mistral', 'Color'],
  xai: ['Grok', null],
  suno: ['Suno', null],
  midjourney: ['Midjourney', null],
  minimax: ['Minimax', 'Color'],
  cohere: ['Cohere', 'Color'],
  ollama: ['Ollama', null],
  azure: ['Azure', 'Color'],
  aws: ['Aws', 'Color'],
  openrouter: ['OpenRouter', null],
  siliconflow: ['SiliconCloud', 'Color'],
};

/**
 * The icon-pack name for a model or vendor, or null when nothing matches.
 * @returns {{name: string, sub: string|null}|null}
 */
export function vendorIconFor({ model, vendor } = {}) {
  const v = String(vendor || '')
    .toLowerCase()
    .replace(/[\s_-]/g, '');
  if (v && VENDOR_RULES[v]) {
    const [name, sub] = VENDOR_RULES[v];
    return { name, sub };
  }
  // Multi-word labels such as the channel types' "Anthropic Claude",
  // "Google Gemini", "Suno API": try each word as a vendor, then as a model.
  const words = String(vendor || '')
    .toLowerCase()
    .split(/[\s/·]+/)
    .filter(Boolean);
  for (const w of words) {
    if (VENDOR_RULES[w]) {
      const [name, sub] = VENDOR_RULES[w];
      return { name, sub };
    }
  }
  for (const w of words) {
    for (const [re, name, sub] of MODEL_RULES) {
      if (re.test(w)) return { name, sub };
    }
  }
  const m = String(model || '').toLowerCase();
  // Strip a routing prefix such as "deepseek-ai/…" or "openai/…".
  const bare = m.includes('/') ? m.slice(m.lastIndexOf('/') + 1) : m;
  for (const [re, name, sub] of MODEL_RULES) {
    if (re.test(bare)) return { name, sub };
  }
  return null;
}

/**
 * A vendor logo for a model/vendor, or a lettered tile when none is known —
 * never an empty gap, so rows and cards keep their alignment.
 */
const HfVendorIcon = ({ model, vendor, size = 18, style }) => {
  const icon = vendorIconFor({ model, vendor });
  const box = {
    width: size,
    height: size,
    flex: `0 0 ${size}px`,
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    ...style,
  };
  if (icon) {
    return (
      <span style={box} data-testid='hf-vendor-icon' data-icon={icon.name}>
        <PackIcon name={icon.name} sub={icon.sub} size={size} />
      </span>
    );
  }
  const letter = String(vendor || model || '?')
    .trim()
    .charAt(0)
    .toUpperCase();
  return (
    <span
      data-testid='hf-vendor-icon'
      data-icon=''
      aria-hidden='true'
      style={{
        ...box,
        borderRadius: 4,
        background: 'var(--hf-sunken)',
        color: 'var(--hf-ink-3)',
        fontFamily: 'var(--hf-mono)',
        fontSize: Math.max(9, Math.round(size * 0.55)),
        fontWeight: 600,
      }}
    >
      {letter}
    </span>
  );
};

export default HfVendorIcon;
