// Misuratore di robustezza della password (ASVS V2.1.8), scritto a mano, senza
// dipendenze. E' solo un aiuto: il server resta l'autorita' (12-128 caratteri,
// elenco di password comuni) e rifiuta con 422 quello che qui sembra buono.

export const MIN_PASSWORD_LENGTH = 12;
export const MAX_PASSWORD_LENGTH = 128;

export type StrengthScore = 0 | 1 | 2 | 3 | 4;

export interface PasswordStrength {
  score: StrengthScore;
  label: string;
  // Suggerimento per chi non e' ancora a posto; vuoto se la password e' buona.
  hint: string;
}

// Un piccolo campione delle password comuni: l'elenco completo e' sul server.
const COMMON = [
  'password1234',
  'password12345',
  'passwordpassword',
  'administrator',
  'qwertyuiop12',
  'qwertyuiopasdf',
  '123456789012',
  '1q2w3e4r5t6y',
  'iloveyou1234',
  'letmein123456',
  'welcome12345',
  'changeme1234',
  'abcdefghijkl',
  'asdfghjkl123',
];

const LABELS = ['Too weak', 'Weak', 'Fair', 'Good', 'Strong'] as const;

function isRepeatedBlock(p: string): boolean {
  for (let size = 1; size <= 4 && size * 2 <= p.length; size++) {
    if (p.length % size !== 0) continue;
    if (p.slice(size) === p.slice(0, p.length - size)) return true;
  }
  return false;
}

export function passwordStrength(password: string): PasswordStrength {
  const chars = Array.from(password);
  const n = chars.length;
  const result = (score: StrengthScore, hint = ''): PasswordStrength => ({ score, label: LABELS[score], hint });

  if (n === 0) return result(0, `Use at least ${MIN_PASSWORD_LENGTH} characters.`);
  if (n < MIN_PASSWORD_LENGTH) return result(0, `Use at least ${MIN_PASSWORD_LENGTH} characters.`);
  if (n > MAX_PASSWORD_LENGTH) return result(0, `Use at most ${MAX_PASSWORD_LENGTH} characters.`);

  const lower = password.toLowerCase().trim();
  const noSymbols = lower.replace(/[!@#$%^&*.?_-]+$/, '');
  const noTail = lower.replace(/[0-9!@#$%^&*.?_-]+$/, '');
  if (COMMON.includes(lower) || COMMON.includes(noSymbols) || COMMON.includes(noTail) || isRepeatedBlock(lower)) {
    return result(0, 'This is a very common password: choose another.');
  }

  const classes =
    Number(/[a-z]/.test(password)) +
    Number(/[A-Z]/.test(password)) +
    Number(/[0-9]/.test(password)) +
    Number(/[^A-Za-z0-9]/.test(password));
  const unique = new Set(chars).size;
  const words = password.trim().split(/\s+/).filter(Boolean).length;
  const passphrase = words >= 3 && n >= 16;

  if (unique < 6) return result(1, 'Too many repeated characters.');
  if (/(0123|1234|2345|3456|4567|5678|6789|abcd|bcde|qwer|wert|asdf|zxcv)/.test(lower) && n < 20) {
    return result(1, 'Avoid keyboard and number sequences.');
  }

  let score = 1;
  if (n >= 16) score += 1;
  if (classes >= 3 || passphrase) score += 1;
  if (n >= 20 && unique >= 10) score += 1;
  const capped = Math.min(score, 4) as StrengthScore;
  return result(capped, capped >= 3 ? '' : 'Make it longer or mix letters, digits and symbols.');
}
