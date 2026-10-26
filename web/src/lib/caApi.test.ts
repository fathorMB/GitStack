import { afterEach, describe, expect, it, vi } from 'vitest';
import { detectDevice, parseAppParams, probeTrust } from './caApi';

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('detectDevice', () => {
  it.each([
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64)', 0, 'win'],
    ['Mozilla/5.0 (X11; Linux x86_64)', 0, 'win'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)', 0, 'mac'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)', 5, 'ios'], // iPadOS
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)', 5, 'ios'],
    ['Mozilla/5.0 (iPad; CPU OS 16_0 like Mac OS X)', 5, 'ios'],
    ['Mozilla/5.0 (Linux; Android 14; Pixel 8)', 5, 'and'],
  ] as const)('%s → %s', (ua, touch, want) => {
    expect(detectDevice(ua, touch)).toBe(want);
  });
});

describe('parseAppParams', () => {
  it('tiene solo nomi host validi, senza schema, percorso e doppioni', () => {
    expect(
      parseAppParams('?app=sito.apps.lan&app=https://backoffice.apps.lan/x&app=sito.apps.lan&app=&app=bad host&app=a/../b&app=evil.com:99999x'),
    ).toEqual(['sito.apps.lan', 'backoffice.apps.lan', 'a']);
  });
  it('vuoto senza parametro', () => {
    expect(parseAppParams('')).toEqual([]);
  });
});

describe('probeTrust', () => {
  it('fetch riuscita (no-cors) = fidato', async () => {
    const f = vi.fn().mockResolvedValue(new Response(null));
    vi.stubGlobal('fetch', f);
    await expect(probeTrust('sito.apps.lan')).resolves.toBe('trusted');
    expect(f).toHaveBeenCalledWith('https://sito.apps.lan/', expect.objectContaining({ mode: 'no-cors', cache: 'no-store' }));
  }, 10000);

  it('errore di rete/TLS = non fidato', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    await expect(probeTrust('sito.apps.lan')).resolves.toBe('untrusted');
  }, 10000);

  it('nessuna risposta entro il timeout = non fidato', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((_u: string, init: RequestInit) => new Promise((_res, rej) => init.signal?.addEventListener('abort', () => rej(new DOMException('x', 'AbortError'))))),
    );
    await expect(probeTrust('sito.apps.lan', 20)).resolves.toBe('untrusted');
  }, 10000);
});
