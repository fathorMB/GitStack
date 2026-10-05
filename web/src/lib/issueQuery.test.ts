import { describe, expect, it } from 'vitest';
import { hasFreeText, qualifierValues, setQualifier, tokenize } from './issueQuery';

describe('issueQuery', () => {
  it('spezza rispettando le virgolette', () => {
    expect(tokenize('is:open label:"good first issue" crash')).toEqual(['is:open', 'label:"good first issue"', 'crash']);
  });
  it('legge e sostituisce i qualificatori', () => {
    expect(qualifierValues('is:open label:"a b" label:x', 'label')).toEqual(['a b', 'x']);
    expect(setQualifier('is:open label:bug crash', 'label', 'good first issue')).toBe('is:open crash label:"good first issue"');
    expect(setQualifier('is:open label:bug', 'label', null)).toBe('is:open');
  });
  it('lascia stare i qualificatori negati', () => {
    expect(setQualifier('-label:wontfix label:bug', 'label', 'x')).toBe('-label:wontfix label:x');
  });
  it('riconosce il testo libero', () => {
    expect(hasFreeText('is:open')).toBe(false);
    expect(hasFreeText('is:open crash')).toBe(true);
  });
});
