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

/*
 * Lock for the L5 acceptor finding (cycle7 repair round 2, items 2/10/17/27):
 * the `__lurus_force_http1` param_override key had zero visibility in the
 * console — an operator could not discover the key, its scope, or the
 * bypassed side calls without reading the integration guide out of band.
 *
 * This is a structural (source-text) check, not a render test: mounting the
 * full EditChannelModal form body requires a much larger harness (see
 * tc_EditChannelModal_keyreveal.test.jsx's header comment on why the sheet is
 * kept closed in that file). Reading the source directly is the narrowest
 * check that still goes red if the help text — or its translation — is
 * removed.
 */
import fs from 'node:fs';
import path from 'node:path';
import { describe, it, expect } from 'vitest';

import en from '../../../../i18n/locales/en.json';
import zh from '../../../../i18n/locales/zh.json';

const MODAL_PATH = path.resolve(
  process.cwd(),
  'src/components/table/channels/modals/EditChannelModal.jsx',
);

const HELP_TEXT_ZH =
  '内部控制键 __lurus_force_http1：设为 true 可把该渠道的出站请求锁定为 HTTP/1.1（键本身不会转发给上游）。仅覆盖经统一转发路径发出的主请求；AWS AKSK 凭证模式、Coze 结果轮询、Vertex 换取 token、Midjourney 图片拉取、hailuo 任务查询等旁路调用不受影响，详见产品对接文档「单渠道强制 HTTP/1.1 与会话亲和运维」一节。';

describe('EditChannelModal param_override help text (__lurus_force_http1)', () => {
  it('renders help text for __lurus_force_http1 next to the param_override field', () => {
    const source = fs.readFileSync(MODAL_PATH, 'utf8');
    const paramOverrideFieldIndex = source.indexOf("field='param_override'");
    expect(paramOverrideFieldIndex).toBeGreaterThan(-1);

    const helpTextIndex = source.indexOf('__lurus_force_http1');
    expect(helpTextIndex).toBeGreaterThan(-1);

    // The help text must be part of the SAME Form.TextArea block as
    // param_override (the next field definition after it), not some
    // unrelated mention elsewhere in this 3000+ line file.
    const nextFieldIndex = source.indexOf(
      "field='header_override'",
      paramOverrideFieldIndex,
    );
    expect(nextFieldIndex).toBeGreaterThan(paramOverrideFieldIndex);
    expect(helpTextIndex).toBeGreaterThan(paramOverrideFieldIndex);
    expect(helpTextIndex).toBeLessThan(nextFieldIndex);
  });

  it('is translated (zh identity key + en translation both present)', () => {
    const zhValue = zh.translation[HELP_TEXT_ZH];
    const enValue = en.translation[HELP_TEXT_ZH];
    expect(zhValue).toBe(HELP_TEXT_ZH);
    expect(enValue).toBeTruthy();
    expect(enValue).not.toBe(HELP_TEXT_ZH);
    expect(enValue).toMatch(/HTTP\/1\.1/);
  });

  it('names the bypassed side calls, not just the covered path', () => {
    // Regression the plan's own §8 amendment got wrong (finding
    // routing-resilience-limits-13#9/#13/#14/#19): AWS/Coze/Vertex/task main
    // requests DO go through this seam; only their side calls (AKSK mode,
    // result-poll, token exchange, image fetch, status query) bypass it.
    // The help text must name the bypassed calls precisely, not the whole
    // channel type.
    for (const term of ['AKSK', 'Coze 结果轮询', 'Vertex 换取 token']) {
      expect(HELP_TEXT_ZH).toContain(term);
    }
  });
});
