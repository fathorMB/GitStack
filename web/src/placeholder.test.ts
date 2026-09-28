import { describe, expect, it } from 'vitest';
import { placeholder } from './placeholder';

describe('placeholder', () => {
  it('returns a non-empty string', () => {
    expect(placeholder().length).toBeGreaterThan(0);
  });
});
