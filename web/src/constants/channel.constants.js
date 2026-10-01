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

export const CHANNEL_OPTIONS = [
  { value: 1, color: 'green', label: 'OpenAI' },
  {
    value: 2,
    color: 'light-blue',
    label: 'Midjourney Proxy',
  },
  {
    value: 5,
    color: 'blue',
    label: 'Midjourney Proxy Plus',
  },
  {
    value: 36,
    color: 'purple',
    label: 'Suno API',
  },
  { value: 4, color: 'grey', label: 'Ollama' },
  {
    value: 14,
    color: 'indigo',
    label: 'Anthropic Claude',
  },
  {
    value: 33,
    color: 'indigo',
    label: 'AWS Claude',
  },
  { value: 41, color: 'blue', label: 'Vertex AI' },
  {
    value: 3,
    color: 'teal',
    label: 'Azure OpenAI',
  },
  {
    value: 34,
    color: 'purple',
    label: 'Cohere',
  },
  { value: 39, color: 'grey', label: 'Cloudflare' },
  { value: 43, color: 'blue', label: 'DeepSeek' },
  {
    value: 15,
    color: 'blue',
    label: '百度文心千帆',
  },
  {
    value: 46,
    color: 'blue',
    label: '百度文心千帆V2',
  },
  {
    value: 17,
    color: 'orange',
    label: '阿里通义千问',
  },
  {
    value: 18,
    color: 'blue',
    label: '讯飞星火认知',
  },
  {
    value: 16,
    color: 'violet',
    label: '智谱 ChatGLM（已经弃用，请使用智谱 GLM-4V）',
  },
  {
    value: 26,
    color: 'purple',
    label: '智谱 GLM-4V',
  },
  {
    value: 27,
    color: 'blue',
    label: 'Perplexity',
  },
  {
    value: 24,
    color: 'orange',
    label: 'Google Gemini',
  },
  {
    value: 11,
    color: 'orange',
    label: 'Google PaLM2',
  },
  {
    value: 47,
    color: 'blue',
    label: 'Xinference',
  },
  { value: 25, color: 'green', label: 'Moonshot' },
  { value: 20, color: 'green', label: 'OpenRouter' },
  { value: 19, color: 'blue', label: '360 智脑' },
  { value: 23, color: 'teal', label: '腾讯混元' },
  { value: 31, color: 'green', label: '零一万物' },
  { value: 35, color: 'green', label: 'MiniMax' },
  { value: 37, color: 'teal', label: 'Dify' },
  { value: 38, color: 'blue', label: 'Jina' },
  { value: 40, color: 'purple', label: 'SiliconCloud' },
  { value: 42, color: 'blue', label: 'Mistral AI' },
  { value: 8, color: 'pink', label: '自定义渠道' },
  {
    value: 22,
    color: 'blue',
    label: '知识库：FastGPT',
  },
  {
    value: 21,
    color: 'purple',
    label: '知识库：AI Proxy',
  },
  {
    value: 44,
    color: 'purple',
    label: '嵌入模型：MokaAI M3E',
  },
  {
    value: 45,
    color: 'blue',
    label: '字节火山方舟、豆包通用',
  },
  {
    value: 48,
    color: 'blue',
    label: 'xAI',
  },
  {
    value: 49,
    color: 'blue',
    label: 'Coze',
  },
  {
    value: 50,
    color: 'green',
    label: '可灵',
  },
  {
    value: 51,
    color: 'blue',
    label: '即梦',
  },
  {
    value: 52,
    color: 'purple',
    label: 'Vidu',
  },
  {
    value: 53,
    color: 'blue',
    label: 'SubModel',
  },
  {
    value: 54,
    color: 'blue',
    label: '豆包视频',
  },
  {
    value: 55,
    color: 'green',
    label: 'Sora',
  },
  {
    value: 56,
    color: 'blue',
    label: 'Replicate',
  },
  {
    value: 57,
    color: 'indigo',
    label: 'TypeSafe',
  },
  {
    value: 58,
    color: 'cyan',
    label: 'System One compatible (self-hosted)',
  },
];

export const CHANNEL_PRESETS = {
  1: {
    name: 'OpenAI',
    base_url: '',
    tip: 'preset_tip_openai',
  },
  14: {
    name: 'Anthropic Claude',
    base_url: 'https://api.anthropic.com',
    tip: 'preset_tip_claude',
  },
  43: {
    name: 'DeepSeek',
    base_url: 'https://api.deepseek.com',
    tip: 'preset_tip_deepseek',
  },
  24: {
    name: 'Google Gemini',
    base_url: 'https://generativelanguage.googleapis.com',
    tip: 'preset_tip_gemini',
  },
  42: {
    name: 'Mistral AI',
    base_url: 'https://api.mistral.ai',
    tip: 'preset_tip_mistral',
  },
  4: {
    name: 'Ollama',
    base_url: 'http://localhost:11434',
    tip: 'preset_tip_ollama',
  },
  17: {
    name: '阿里通义千问',
    base_url: 'https://dashscope.aliyuncs.com',
    tip: 'preset_tip_qwen',
  },
  26: {
    name: '智谱 GLM',
    base_url: 'https://open.bigmodel.cn',
    tip: 'preset_tip_zhipu',
  },
  48: {
    name: 'xAI',
    base_url: 'https://api.x.ai',
    tip: 'preset_tip_xai',
  },
  40: {
    name: 'SiliconCloud',
    base_url: 'https://api.siliconflow.cn',
    tip: 'preset_tip_siliconcloud',
  },
  57: {
    name: 'TypeSafe',
    base_url: 'https://api.typesafe.ai',
    tip: 'preset_tip_typesafe',
  },
  // No default host: a self-hosted server lives wherever the operator put it.
  58: {
    name: 'System One (self-hosted)',
    base_url: '',
    tip: 'preset_tip_systemone',
  },
};

export const MODEL_TABLE_PAGE_SIZE = 10;
