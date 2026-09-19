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
 * Which languages this console ships, and the evidence for each.
 *
 * Six locale files sat in src/i18n/locales and all six were registered with
 * i18next, so the browser language detector could land an operator on fr / ja /
 * ru / vi. Measured against en.json those four carry 58% of the keys, and the
 * missing 42% resolved through fallbackLng: 'zh' — a French operator read a
 * console that was two fifths Chinese, and nothing in the suite noticed.
 *
 * The rule these tests hold to is: a language is registered when its file
 * covers at least COVERAGE_FLOOR of en.json, plus zh, which is the source
 * language every key is written in and therefore ships whatever its ratio is.
 * The equality is deliberate in both directions. Registering an under-translated
 * locale is the defect above; leaving a finished one unregistered is a
 * different lie (the translation exists and nobody can pick it), so finishing
 * fr to the floor makes this test demand that fr be registered.
 *
 * The four unpublished files stay in the tree — this is a reversible honesty
 * measure, not a deletion (owner item O-lang decides which languages the
 * product sells). The last test pins that they are still there.
 */
import fs from 'node:fs';
import path from 'node:path';
import { describe, it, expect, afterAll } from 'vitest';

import i18n, { PUBLISHED_LANGUAGES } from './i18n';
import en from './locales/en.json';

const LOCALE_DIR = path.resolve(process.cwd(), 'src/i18n/locales');
const SELECTOR = path.resolve(
  process.cwd(),
  'src/components/layout/headerbar/LanguageSelector.jsx',
);

// zh is the language every t() key is literally written in, so a key it omits
// still renders correct Chinese (the key IS the Chinese text). Its ratio
// against en.json is therefore not a measure of how Chinese the console is.
const SOURCE_LANGUAGE = 'zh';
const COVERAGE_FLOOR = 95;
const UNPUBLISHED = ['fr', 'ja', 'ru', 'vi'];

// A value identical to its own key is what an extractor writes when nobody has
// translated the entry yet. For a key written as Chinese source text that is
// the correct string; for a dotted identifier such as common.changeLanguage it
// puts the identifier on screen. Only the dotted family is checked here.
const DOTTED_KEY = /^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z0-9_]+)+$/;

// Distinct enough that finding it anywhere proves ru.json was pulled in.
const RU_SENTINEL = 'Сменить язык';

function leafKeys(obj, prefix = '', out = new Map()) {
  for (const key of Object.keys(obj)) {
    const value = obj[key];
    const dotted = prefix ? `${prefix}.${key}` : key;
    if (value && typeof value === 'object') leafKeys(value, dotted, out);
    else out.set(dotted, value);
  }
  return out;
}

const readBundle = (lng) =>
  leafKeys(
    JSON.parse(fs.readFileSync(path.join(LOCALE_DIR, `${lng}.json`), 'utf8'))
      .translation,
  );

const EN_KEYS = [...leafKeys(en.translation).keys()];

const LOCALE_FILES = fs
  .readdirSync(LOCALE_DIR)
  .filter((f) => f.endsWith('.json'))
  .map((f) => f.slice(0, -'.json'.length))
  .sort();

const coverage = (lng) => {
  const bundle = readBundle(lng);
  const present = EN_KEYS.filter((k) => bundle.has(k)).length;
  return (100 * present) / EN_KEYS.length;
};

const COVERAGE = new Map(LOCALE_FILES.map((lng) => [lng, coverage(lng)]));
const report = [...COVERAGE]
  .map(([lng, pct]) => `${lng} ${pct.toFixed(2)}%`)
  .join(', ');

// Read once, at import time, before any test calls changeLanguage: i18next
// creates store entries on demand, so sampling later would report languages
// that were asked for rather than languages that were shipped.
const REGISTERED = Object.keys(i18n.store.data).sort();
const INITIAL_LANGUAGE = i18n.language;
// Sampled at import time too: what <html lang> holds after init and before any
// test has switched language is the value a first paint gets.
const INITIAL_RESOLVED_LANGUAGE = i18n.resolvedLanguage;
const INITIAL_DOCUMENT_LANG = document.documentElement.lang;

describe('locale coverage', () => {
  afterAll(async () => {
    await i18n.changeLanguage(INITIAL_LANGUAGE);
  });

  it('registers exactly the locales that are translated, plus the source language', () => {
    const translated = LOCALE_FILES.filter(
      (lng) => COVERAGE.get(lng) >= COVERAGE_FLOOR,
    );
    const shippable = [...new Set([...translated, SOURCE_LANGUAGE])].sort();
    expect(
      REGISTERED,
      `i18n.js registers ${REGISTERED.join(', ')} but the honest set is ` +
        `${shippable.join(', ')} (>= ${COVERAGE_FLOOR}% of en.json, plus ` +
        `${SOURCE_LANGUAGE}). Measured coverage: ${report}.`,
    ).toEqual(shippable);
  });

  it('screens an unshipped tag inside i18next, with no second list to keep in step', () => {
    // supportedLngs IS PUBLISHED_LANGUAGES, and every tag reaching i18next goes
    // through this screening — the detector's localStorage cache included. That
    // is why no page guards the stored 'i18nextLng' itself: one ?lng=ja visit
    // parks 'ja' in localStorage, and the next load reduces it here, before
    // i18n.language is ever assigned, and the detector then caches the reduced
    // tag. A guard in a page would be a second copy of this list that can drift.
    expect([...PUBLISHED_LANGUAGES].sort()).toEqual(REGISTERED);
    const { languageUtils } = i18n.services;
    // Region subtags are shipped tags: load: 'languageOnly' reduces them first.
    for (const tag of ['zh', 'zh-CN', 'en', 'en-US'])
      expect(languageUtils.isSupportedCode(tag), tag).toBe(true);
    for (const tag of ['ja', 'ja-JP', 'fr', 'ru', 'vi', 'de'])
      expect(languageUtils.isSupportedCode(tag), tag).toBe(false);
    // What the detector's output is reduced to before anything reads it.
    expect(languageUtils.getBestMatchFromCodes(['ja', 'ja-JP'])).toBe('en');
    expect(languageUtils.getBestMatchFromCodes(['de'])).toBe('en');
    // A supported region tag is kept as it stands — i18n.language really is
    // 'zh-CN' for a Chinese browser, and only resolvedLanguage is 'zh'. Any
    // code comparing i18n.language against 'zh' or 'en' therefore matches
    // nothing for a mainstream browser; see LanguageSelector.jsx.
    expect(languageUtils.getBestMatchFromCodes(['zh-CN', 'zh'])).toBe('zh-CN');
  });

  it('resolves a language it does not ship to en, not to Chinese', async () => {
    await i18n.changeLanguage('ja');
    expect(i18n.resolvedLanguage).toBe('en');
    expect(i18n.languages).not.toContain('ja');
    // A real key, to prove the resolution reaches English text and not just an
    // English-looking language tag.
    expect(i18n.t('自动定价')).toBe('Auto Pricing');
  });

  it('keeps a zh operator on Chinese for a key zh omits', async () => {
    // do-not-regress: en.json carries 265 keys zh.json does not, and for the
    // Chinese-source family the key itself is the correct Chinese string.
    // A blanket fallbackLng: 'en' would answer this lookup with 'Auto Pricing'.
    await i18n.changeLanguage('zh-CN');
    expect(i18n.resolvedLanguage).toBe('zh');
    expect(i18n.t('自动定价')).toBe('自动定价');
  });

  it('declares the rendered language on <html lang>', async () => {
    // index.html ships lang="en" for the document before JavaScript runs; from
    // there this follows what is actually on screen. Declaring a document
    // Chinese while it renders English is what makes a browser offer to
    // machine-translate an already-English page, and what sends a screen
    // reader into the wrong voice.
    //
    // The init emit counts: the listener is registered before init, so the
    // attribute is right on first paint rather than after the first manual
    // switch. INITIAL_DOCUMENT_LANG was read at import time.
    expect(INITIAL_DOCUMENT_LANG).toBe(INITIAL_RESOLVED_LANGUAGE);
    expect(INITIAL_DOCUMENT_LANG).not.toBe('');

    await i18n.changeLanguage('zh-CN');
    expect(document.documentElement.lang).toBe('zh');
    // resolvedLanguage, not language: this build renders English for 'ja'.
    await i18n.changeLanguage('ja');
    expect(i18n.language).toBe('ja');
    expect(document.documentElement.lang).toBe('en');
  });

  it('offers in the language selector exactly the languages it registers', () => {
    const src = fs.readFileSync(SELECTOR, 'utf8');
    const offered = [
      ...new Set(
        [...src.matchAll(/onLanguageChange\(\s*'([A-Za-z-]+)'\s*\)/g)].map(
          (m) => m[1],
        ),
      ),
    ].sort();
    expect(
      offered,
      `The picker offers ${offered.join(', ')} while i18next registers ` +
        `${REGISTERED.join(', ')}. An offered language with no bundle silently ` +
        `falls back; a registered language with no menu entry is unreachable.`,
    ).toEqual(REGISTERED);
  });

  it('ships no bundle whose value is its own dotted key', () => {
    const echoes = [];
    for (const lng of REGISTERED) {
      for (const [key, value] of readBundle(lng))
        if (value === key && DOTTED_KEY.test(key))
          echoes.push(`${lng}: ${key}`);
    }
    expect(
      echoes,
      `These render the identifier itself on screen — an aria-label reading ` +
        `"common.changeLanguage" is what an untranslated extractor entry looks ` +
        `like in production:\n  ${echoes.join('\n  ')}`,
    ).toEqual([]);
  });

  it('keeps the unpublished translations in the tree but out of the runtime', () => {
    for (const lng of UNPUBLISHED)
      expect(
        fs.existsSync(path.join(LOCALE_DIR, `${lng}.json`)),
        `${lng}.json was deleted; unregistering a locale is reversible, ` +
          `deleting the translations is not.`,
      ).toBe(true);

    // ru carries a sentinel no other bundle holds; if it is reachable at
    // runtime the file was pulled into the graph after all. The shipped-asset
    // side of this claim is the bundle probe in the lane's UAT step — this is
    // the runtime-registration side.
    expect(readBundle('ru').get('common.changeLanguage')).toBe(RU_SENTINEL);
    expect(i18n.getResourceBundle('ru', 'translation')).toBeUndefined();
  });
});
