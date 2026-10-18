import { describe, expect, it } from 'vitest';
import { MAX_PASSWORD_LENGTH, passwordStrength } from './passwordStrength';

describe('passwordStrength', () => {
  it('sotto i 12 caratteri e\' troppo debole', () => {
    const s = passwordStrength('Abc1!');
    expect(s.score).toBe(0);
    expect(s.hint).toMatch(/at least 12/);
  });

  it('oltre i 128 caratteri e\' rifiutata', () => {
    expect(passwordStrength('aB3'.repeat(50)).hint).toMatch(/at most 128/);
    expect('aB3'.repeat(50).length).toBeGreaterThan(MAX_PASSWORD_LENGTH);
  });

  it('una password comune vale 0, anche maiuscola o con una coda', () => {
    expect(passwordStrength('Password1234').score).toBe(0);
    expect(passwordStrength('password12345!!').score).toBe(0);
    expect(passwordStrength('aaaaaaaaaaaa').score).toBe(0);
    expect(passwordStrength('abcabcabcabc').score).toBe(0);
  });

  it('le sequenze di tastiera non superano 1', () => {
    expect(passwordStrength('hello-1234-world').score).toBeLessThanOrEqual(1);
  });

  it('cresce con lunghezza e varieta\'', () => {
    const fair = passwordStrength('correctpony');
    expect(fair.score).toBe(0); // 11 caratteri
    const weak = passwordStrength('correctponies').score;
    const better = passwordStrength('Correct-Pony-9').score;
    const strong = passwordStrength('Cavallo Batteria Graffetta 42!').score;
    expect(weak).toBeLessThan(better);
    expect(better).toBeLessThanOrEqual(strong);
    expect(strong).toBe(4);
  });
});
