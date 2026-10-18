import { describe, expect, it } from 'vitest';
import conf from '../../deploy/nginx.conf.template?raw';

// Estrae il corpo di un blocco `location <selettore> { ... }`.
function location(selector: string): string {
  const start = conf.indexOf(`location ${selector} {`);
  expect(start, `location ${selector}`).toBeGreaterThanOrEqual(0);
  return conf.slice(start, conf.indexOf('\n    }', start));
}

describe('nginx.conf.template: raw dei file (GIT-81, regola B3)', () => {
  const raw = location('~ ^/[^/]+/[^/]+/raw/');

  it('sta prima del fallback SPA', () => {
    expect(conf.indexOf('location ~ ^/[^/]+/[^/]+/raw/')).toBeLessThan(conf.indexOf('location / {'));
  });

  it('va al gateway in streaming, con rewrite ... break (proxy_pass con variabile)', () => {
    expect(raw).toContain('proxy_pass $gateway_upstream;');
    expect(raw).toContain('proxy_buffering off;');
    expect(raw).toMatch(/rewrite \^\/\(\[\^\/\]\+\)\/\(\[\^\/\]\+\)\/raw\/\(\.\*\)\$ \/v1\/repos\/\$1\/\$2\/raw\/\$3 break;/);
  });

  it('non scrive gli header di sicurezza del raw: li scrive il servizio', () => {
    expect(raw).not.toMatch(/add_header|proxy_hide_header|proxy_set_header (Content-|X-Content)/i);
  });

  it('riscrive davvero <owner>/<repo>/raw/<ref>/<percorso> in /v1/repos/...', () => {
    const m = /rewrite (\S+) (\S+) break;/.exec(raw);
    expect(m).not.toBeNull();
    const re = new RegExp(m![1]);
    const to = m![2];
    const apply = (uri: string) => uri.replace(re, to.replace(/\$(\d)/g, '$$$1'));
    expect(apply('/alice/demo/raw/main/src/a b.txt')).toBe('/v1/repos/alice/demo/raw/main/src/a b.txt');
    expect(apply('/alice/demo/raw/feature/x/README.md')).toBe('/v1/repos/alice/demo/raw/feature/x/README.md');
    // La location regex non prende le pagine della SPA.
    const sel = /^\/[^/]+\/[^/]+\/raw\//;
    expect(sel.test('/alice/demo')).toBe(false);
    expect(sel.test('/alice/demo/tree/main')).toBe(false);
    expect(sel.test('/alice/demo/blob/main/raw/x')).toBe(false);
  });

  it('/api/ resta davanti alla regex (^~), così /api/x/raw/y non finisce nel raw', () => {
    expect(conf).toContain('location ^~ /api/ {');
  });

  it('/api/ toglie solo /api (come strip-api dell\'Ingress): /api/v1/x arriva come /v1/x, mai /v1/v1', () => {
    const api = location('^~ /api/');
    const m = api.match(/rewrite (\S+) (\S+) break;/);
    expect(m).not.toBeNull();
    const out = '/api/v1/auth/session'.replace(new RegExp(m![1]), m![2].replace(/\$(\d)/g, '$$$1'));
    expect(out).toBe('/v1/auth/session');
  });
});

describe('nginx.conf.template: intestazioni di sicurezza (GIT-229, F3)', () => {
  const headers = ['Content-Security-Policy', 'X-Frame-Options', 'X-Content-Type-Options', 'Referrer-Policy'];

  it('sets CSP, frame-ancestors, nosniff and Referrer-Policy on the SPA and hides the nginx version', () => {
    expect(conf).toMatch(/^\s*server_tokens off;/m);
    for (const sel of ['/', '^~ /api/']) {
      const loc = location(sel);
      for (const h of headers) {
        expect(loc, `${sel}: ${h}`).toMatch(new RegExp(`add_header ${h} ".+" always;`));
      }
    }
    const spa = location('/');
    const csp = /add_header Content-Security-Policy "([^"]+)"/.exec(spa)![1];
    expect(csp).toContain("default-src 'self'");
    expect(csp).toContain("script-src 'self'");
    expect(csp).toContain("frame-ancestors 'none'");
    expect(csp).toContain("object-src 'none'");
    expect(csp).toContain("style-src 'self' 'unsafe-inline'"); // <style> iniettato da Radix a runtime (dist/assets/*.js)
    // Niente scappatoie per gli script.
    expect(csp).not.toMatch(/script-src[^;]*('unsafe-inline'|'unsafe-eval'|\*)/);
    expect(spa).toContain('X-Frame-Options "DENY"');
    expect(spa).toContain('X-Content-Type-Options "nosniff"');
    expect(spa).toContain('Referrer-Policy "no-referrer"');
  });

  it('ripete le intestazioni nelle location di /downloads, che hanno add_header propri', () => {
    for (const sel of ['^~ /downloads/', '= /downloads/index.json', '= /downloads/ca.crt']) {
      expect(location(sel)).toContain('Content-Security-Policy');
    }
  });

  it('la build di Vite non ha script inline: index.html carica solo script esterni', async () => {
    const html = (await import('../../index.html?raw')).default;
    expect(html).not.toMatch(/<script(?![^>]*\bsrc=)[^>]*>/i);
    expect(html).not.toMatch(/<style[\s>]|\sstyle=|\sonclick=/i);
  });
});
