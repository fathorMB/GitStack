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
