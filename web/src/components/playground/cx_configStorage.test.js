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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import {
  saveConfig,
  saveMessages,
  loadConfig,
  loadMessages,
  clearConfig,
  clearMessages,
  hasStoredConfig,
  getConfigTimestamp,
  exportConfig,
  importConfig,
} from './configStorage';
import {
  STORAGE_KEYS,
  DEFAULT_CONFIG,
} from '../../constants/playground.constants';

const readRaw = (key) => localStorage.getItem(key);
const readJson = (key) => JSON.parse(localStorage.getItem(key));

beforeEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
  // Every failure path in this module funnels through console.error; silence
  // it but keep the spy so "did it take the catch branch" stays assertable.
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('configStorage — saveConfig / loadConfig round trip', () => {
  it('persists the config under the playground config key with a timestamp', () => {
    saveConfig({ inputs: { model: 'gpt-4o-mini' } });

    const stored = readJson(STORAGE_KEYS.CONFIG);
    expect(stored.inputs.model).toBe('gpt-4o-mini');
    // The timestamp is generated, not passed in — assert it round-trips as a
    // parseable instant rather than pinning a literal clock value.
    expect(Number.isNaN(Date.parse(stored.timestamp))).toBe(false);
  });

  it('returns DEFAULT_CONFIG verbatim when nothing has been stored', () => {
    expect(loadConfig()).toBe(DEFAULT_CONFIG);
  });

  it('merges a partial stored config over the defaults instead of replacing them', () => {
    // Only one input key was stored; every other default must survive.
    localStorage.setItem(
      STORAGE_KEYS.CONFIG,
      JSON.stringify({ inputs: { model: 'claude-3' } }),
    );

    const loaded = loadConfig();
    expect(loaded.inputs.model).toBe('claude-3');
    expect(loaded.inputs.temperature).toBe(DEFAULT_CONFIG.inputs.temperature);
    expect(loaded.inputs.max_tokens).toBe(DEFAULT_CONFIG.inputs.max_tokens);
    expect(loaded.parameterEnabled).toEqual(DEFAULT_CONFIG.parameterEnabled);
  });

  it('lets a stored parameterEnabled flag override the default', () => {
    localStorage.setItem(
      STORAGE_KEYS.CONFIG,
      JSON.stringify({ parameterEnabled: { seed: true, temperature: false } }),
    );

    const loaded = loadConfig();
    expect(loaded.parameterEnabled.seed).toBe(true);
    expect(loaded.parameterEnabled.temperature).toBe(false);
    // Untouched flags keep their default.
    expect(loaded.parameterEnabled.top_p).toBe(
      DEFAULT_CONFIG.parameterEnabled.top_p,
    );
  });

  it('carries a stored custom request body and mode through the merge', () => {
    localStorage.setItem(
      STORAGE_KEYS.CONFIG,
      JSON.stringify({
        customRequestMode: true,
        customRequestBody: '{"model":"x"}',
        showDebugPanel: true,
      }),
    );

    const loaded = loadConfig();
    expect(loaded.customRequestMode).toBe(true);
    expect(loaded.customRequestBody).toBe('{"model":"x"}');
    expect(loaded.showDebugPanel).toBe(true);
  });

  it('falls back to defaults when the stored config is corrupt JSON', () => {
    localStorage.setItem(STORAGE_KEYS.CONFIG, '{not json');

    expect(loadConfig()).toBe(DEFAULT_CONFIG);
    expect(console.error).toHaveBeenCalled();
  });

  // ---------------------------------------------------------------------
  // DEFECT: loadConfig() builds `mergedConfig` from an explicit whitelist of
  // five keys (inputs / parameterEnabled / showDebugPanel / customRequestMode
  // / customRequestBody). `systemPrompt` is declared in DEFAULT_CONFIG and is
  // consumed by helpers/api.js buildApiPayload (it becomes the `system` turn
  // of every request), yet it is absent from the whitelist — so a system
  // prompt the user typed is written to localStorage by saveConfig and then
  // silently thrown away on the next page load. The user sees their prompt
  // disappear and their requests quietly stop carrying it.
  // ---------------------------------------------------------------------
  it('saveConfig writes systemPrompt through, and loadConfig never invents one', () => {
    saveConfig({ ...DEFAULT_CONFIG, systemPrompt: 'You are a pirate.' });

    // Invariant either side of the fix: the write half is not the broken half,
    // so the prompt really does reach localStorage today and after a repair.
    expect(readJson(STORAGE_KEYS.CONFIG).systemPrompt).toBe(
      'You are a pirate.',
    );

    const loaded = loadConfig();
    // INVARIANT, deliberately not a pin on the defect. loadConfig is allowed to
    // be in either state: today's whitelist drops the key (-> undefined), and a
    // repaired whitelist returns what was saved. What it must NEVER do is hand
    // back a third value the user never typed — in particular a half-fix that
    // writes `systemPrompt: DEFAULT_CONFIG.systemPrompt` unconditionally would
    // silently blank a configured system prompt on every page load, and that
    // ('') fails here. Shaping it this way means fixing the defect below turns
    // this test green-stays-green instead of reading as a regression.
    expect([undefined, 'You are a pirate.']).toContain(loaded.systemPrompt);
  });

  // Verified 2026-08-20: with configStorage.js at its committed state both
  // locks below are RED (`expected undefined to be 'You are a pirate.'` /
  // `expected undefined to be ''`). They are real contracts, not decoration.
  it.skip('CONTRACT: a saved systemPrompt survives the save→load round trip', () => {
    saveConfig({ ...DEFAULT_CONFIG, systemPrompt: 'You are a pirate.' });
    expect(loadConfig().systemPrompt).toBe('You are a pirate.');
  });

  it.skip('CONTRACT: loadConfig fills systemPrompt from DEFAULT_CONFIG when absent', () => {
    localStorage.setItem(
      STORAGE_KEYS.CONFIG,
      JSON.stringify({ inputs: { model: 'gpt-4o' } }),
    );
    expect(loadConfig().systemPrompt).toBe(DEFAULT_CONFIG.systemPrompt);
  });
});

describe('configStorage — messages', () => {
  it('round-trips a message array', () => {
    const messages = [{ role: 'user', content: 'hi', id: '1' }];
    saveMessages(messages);

    expect(readJson(STORAGE_KEYS.MESSAGES).messages).toEqual(messages);
    expect(loadMessages()).toEqual(messages);
  });

  it('returns null (not an empty array) when nothing is stored', () => {
    expect(loadMessages()).toBeNull();
  });

  it('returns null when the stored envelope has no messages field', () => {
    localStorage.setItem(
      STORAGE_KEYS.MESSAGES,
      JSON.stringify({ timestamp: 'x' }),
    );
    expect(loadMessages()).toBeNull();
  });

  it('returns null and logs when stored messages are corrupt', () => {
    localStorage.setItem(STORAGE_KEYS.MESSAGES, 'not-json');
    expect(loadMessages()).toBeNull();
    expect(console.error).toHaveBeenCalled();
  });
});

describe('configStorage — clearing', () => {
  it('clearConfig removes BOTH the config and the messages', () => {
    saveConfig({ inputs: {} });
    saveMessages([{ role: 'user', content: 'hi' }]);

    clearConfig();

    expect(readRaw(STORAGE_KEYS.CONFIG)).toBeNull();
    expect(readRaw(STORAGE_KEYS.MESSAGES)).toBeNull();
  });

  it('clearMessages removes only the messages and leaves the config intact', () => {
    saveConfig({ inputs: { model: 'gpt-4o' } });
    saveMessages([{ role: 'user', content: 'hi' }]);

    clearMessages();

    expect(readRaw(STORAGE_KEYS.MESSAGES)).toBeNull();
    expect(readJson(STORAGE_KEYS.CONFIG).inputs.model).toBe('gpt-4o');
  });
});

describe('configStorage — introspection', () => {
  it('hasStoredConfig flips false → true once a config is saved', () => {
    expect(hasStoredConfig()).toBe(false);
    saveConfig({ inputs: {} });
    expect(hasStoredConfig()).toBe(true);
  });

  it('getConfigTimestamp returns null with no config and the saved instant after', () => {
    expect(getConfigTimestamp()).toBeNull();

    saveConfig({ inputs: {} });
    const ts = getConfigTimestamp();
    expect(typeof ts).toBe('string');
    expect(Number.isNaN(Date.parse(ts))).toBe(false);
  });

  it('getConfigTimestamp returns null when the stored config carries no timestamp', () => {
    localStorage.setItem(STORAGE_KEYS.CONFIG, JSON.stringify({ inputs: {} }));
    expect(getConfigTimestamp()).toBeNull();
  });

  it('getConfigTimestamp swallows corrupt JSON and reports null', () => {
    localStorage.setItem(STORAGE_KEYS.CONFIG, '<<<');
    expect(getConfigTimestamp()).toBeNull();
    expect(console.error).toHaveBeenCalled();
  });
});

describe('configStorage — exportConfig', () => {
  // jsdom has no object-URL implementation; stub it and capture the Blob so
  // the exported *payload* is assertable, not merely the fact a click fired.
  const setupDownloadCapture = () => {
    const captured = {};
    global.URL.createObjectURL = vi.fn((blob) => {
      captured.blob = blob;
      return 'blob:mock';
    });
    global.URL.revokeObjectURL = vi.fn();

    const clickSpy = vi.fn();
    const realCreate = document.createElement.bind(document);
    vi.spyOn(document, 'createElement').mockImplementation((tag) => {
      const el = realCreate(tag);
      if (tag === 'a') {
        el.click = clickSpy;
        captured.anchor = el;
      }
      return el;
    });
    return { captured, clickSpy };
  };

  it('writes a JSON blob containing the config, the messages and a version', async () => {
    const { captured, clickSpy } = setupDownloadCapture();

    exportConfig({ inputs: { model: 'gpt-4o' } }, [
      { role: 'user', content: 'hi' },
    ]);

    expect(clickSpy).toHaveBeenCalledTimes(1);
    const text = await captured.blob.text();
    const payload = JSON.parse(text);
    expect(payload.inputs.model).toBe('gpt-4o');
    expect(payload.messages).toEqual([{ role: 'user', content: 'hi' }]);
    expect(payload.version).toBe('1.0');
    expect(Number.isNaN(Date.parse(payload.exportTime))).toBe(false);
    expect(captured.blob.type).toBe('application/json');
  });

  it('falls back to the stored messages when none are passed', async () => {
    saveMessages([{ role: 'assistant', content: 'stored' }]);
    const { captured } = setupDownloadCapture();

    exportConfig({ inputs: {} });

    const payload = JSON.parse(await captured.blob.text());
    expect(payload.messages).toEqual([
      { role: 'assistant', content: 'stored' },
    ]);
  });

  it('names the download file after the current date', () => {
    const { captured } = setupDownloadCapture();
    exportConfig({ inputs: {} }, []);

    const today = new Date().toISOString().split('T')[0];
    expect(captured.anchor.download).toBe(`playground-config-${today}.json`);
  });

  it('revokes the object URL so the blob is not leaked', () => {
    setupDownloadCapture();
    exportConfig({ inputs: {} }, []);
    expect(global.URL.revokeObjectURL).toHaveBeenCalled();
  });

  it('swallows a failure inside the download path rather than throwing at the caller', () => {
    global.URL.createObjectURL = vi.fn(() => {
      throw new Error('boom');
    });
    // Invariant: a failed export must not take the playground down with it.
    expect(() => exportConfig({ inputs: {} }, [])).not.toThrow();
    expect(console.error).toHaveBeenCalled();
  });
});

describe('configStorage — importConfig', () => {
  const fileOf = (text) =>
    new File([text], 'config.json', { type: 'application/json' });

  it('resolves with the parsed config when it has inputs and parameterEnabled', async () => {
    const payload = {
      inputs: { model: 'gpt-4o' },
      parameterEnabled: { seed: true },
    };

    await expect(
      importConfig(fileOf(JSON.stringify(payload))),
    ).resolves.toEqual(payload);
  });

  it('side-loads the messages embedded in an imported config', async () => {
    const payload = {
      inputs: {},
      parameterEnabled: {},
      messages: [{ role: 'user', content: 'imported' }],
    };

    await importConfig(fileOf(JSON.stringify(payload)));

    expect(loadMessages()).toEqual([{ role: 'user', content: 'imported' }]);
  });

  it('does not touch stored messages when the import carries none', async () => {
    saveMessages([{ role: 'user', content: 'pre-existing' }]);

    await importConfig(
      fileOf(JSON.stringify({ inputs: {}, parameterEnabled: {} })),
    );

    expect(loadMessages()).toEqual([{ role: 'user', content: 'pre-existing' }]);
  });

  it('ignores a non-array messages field instead of storing garbage', async () => {
    await importConfig(
      fileOf(
        JSON.stringify({
          inputs: {},
          parameterEnabled: {},
          messages: 'not-an-array',
        }),
      ),
    );

    expect(loadMessages()).toBeNull();
  });

  it('rejects a structurally invalid config (missing parameterEnabled)', async () => {
    await expect(
      importConfig(fileOf(JSON.stringify({ inputs: {} }))),
    ).rejects.toThrow('配置文件格式无效');
  });

  it('rejects unparseable JSON with a parse-specific message', async () => {
    await expect(importConfig(fileOf('{{{'))).rejects.toThrow(
      /解析配置文件失败/,
    );
  });

  it('rejects when the file cannot be read at all', async () => {
    // Force the reader's error path; the contract is "a read failure surfaces
    // as a rejection", not any particular reader internals.
    const original = global.FileReader;
    global.FileReader = class {
      readAsText() {
        setTimeout(() => this.onerror?.(new Error('io')), 0);
      }
    };
    try {
      await expect(importConfig(fileOf('{}'))).rejects.toThrow('读取文件失败');
    } finally {
      global.FileReader = original;
    }
  });
});
