import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { isInsecureHttp } from '../lib/insecureHttp';
import { InsecureHttpBanner } from './InsecureHttpBanner';

function stubLocation(protocol: string, hostname: string) {
  vi.stubGlobal('location', { ...window.location, protocol, hostname });
}

describe('InsecureHttpBanner (N5)', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('http su un host di rete: mostra l avviso', () => {
    stubLocation('http:', '192.168.1.50');
    render(<InsecureHttpBanner />);
    const banner = screen.getByRole('status');
    expect(banner).toHaveTextContent(/non protetta/i);
    expect(banner).toHaveTextContent('--insecure-http');
  });

  it('https: nessun avviso', () => {
    stubLocation('https:', 'homehub.local');
    render(<InsecureHttpBanner />);
    expect(screen.queryByTestId('insecure-http-banner')).toBeNull();
  });

  it('http su loopback (prove locali): nessun avviso', () => {
    for (const h of ['localhost', '127.0.0.1', '[::1]']) {
      expect(isInsecureHttp({ protocol: 'http:', hostname: h })).toBe(false);
    }
  });
});
