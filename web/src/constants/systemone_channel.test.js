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
import { describe, it, expect } from 'vitest';

import { CHANNEL_OPTIONS, CHANNEL_PRESETS } from './channel.constants';
import en from '../i18n/locales/en.json';
import zh from '../i18n/locales/zh.json';

// The numeric values are the backend's: internal/pkg/constant/channel.go
// ChannelTypeTypeSafe / ChannelTypeSystemOneCompatible.
const TYPESAFE = 57;
const COMPATIBLE = 58;

const option = (v) => CHANNEL_OPTIONS.find((o) => o.value === v);

describe('System One channel types', () => {
  it('lists both types under the backend numeric values', () => {
    expect(option(TYPESAFE)?.label).toBe('TypeSafe');
    expect(option(COMPATIBLE)?.label).toBe(
      'System One compatible (self-hosted)',
    );
  });

  it('TypeSafe preset targets the hosted API', () => {
    expect(CHANNEL_PRESETS[TYPESAFE]).toEqual({
      name: 'TypeSafe',
      base_url: 'https://api.typesafe.ai',
      tip: 'preset_tip_typesafe',
    });
  });

  // A self-hosted server has no canonical host: the operator must type one,
  // so the preset must not pre-fill a guess.
  it('compatible preset has no default host', () => {
    expect(CHANNEL_PRESETS[COMPATIBLE].base_url).toBe('');
    expect(CHANNEL_PRESETS[COMPATIBLE].tip).toBe('preset_tip_systemone');
  });

  it.each([
    ['en', en],
    ['zh', zh],
  ])('%s carries both preset tips', (_, { translation: bundle }) => {
    expect(bundle.preset_tip_typesafe).toBeTruthy();
    expect(bundle.preset_tip_systemone).toBeTruthy();
  });

  // The compatible server's checkpoint names are its own; the gateway only
  // knows the public laya-* names, so the tip is what tells the operator
  // which model_mapping to write.
  it.each([
    ['en', en],
    ['zh', zh],
  ])(
    '%s compatible tip spells out the model_mapping and base URL',
    (_, { translation: bundle }) => {
      const tip = bundle.preset_tip_systemone;
      for (const pair of [
        'laya-english→english',
        'laya-multilingual→multilingual',
        'laya-typed-decisions→typed-decisions',
        'laya-auto→auto',
      ]) {
        expect(tip).toContain(pair);
      }
      expect(tip).toContain('http://<host>:8000');
    },
  );
});
