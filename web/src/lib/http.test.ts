import { describe, expect, it } from 'vitest';
import { ApiError, describeError, unwrap } from './http';

describe('unwrap / describeError', () => {
  it('trasforma un errore API in ApiError con status e Retry-After', () => {
    const response = new Response(null, { status: 429, headers: { 'Retry-After': '120' } });
    const call = () => unwrap({ error: { error: { code: 'too_many_attempts', message: 'slow down' } }, response });
    expect(call).toThrow(ApiError);
    try {
      call();
    } catch (e) {
      expect((e as ApiError).status).toBe(429);
      expect((e as ApiError).retryAfter).toBe(120);
      expect(describeError(e)).toBe('Too many attempts. Try again in 2 minutes.');
    }
  });

  it('un corpo di errore non standard non resta muto', () => {
    expect(() => unwrap({ error: {}, response: new Response(null, { status: 501 }) })).toThrow(/501/);
  });

  it('403 e 401 hanno messaggi leggibili; la rete assente pure', () => {
    expect(describeError(new ApiError({ error: { code: 'forbidden', message: 'Admins only.' } }, 403))).toBe(
      "You don't have permission to do this. Admins only.",
    );
    expect(describeError(new ApiError({ error: { code: 'unauthenticated', message: 'x' } }, 401))).toMatch(/sign in again/);
    expect(describeError(new TypeError('fetch failed'))).toMatch(/Could not reach the server/);
  });
});
