import { TOKEN_SCOPES } from './accessApi';
import type { TokenScope } from './accessApi';

export const TOKEN_EXPIRATIONS = ['30', '90', '365'] as const;

export interface TokenPrefill {
  name: string;
  scopes: TokenScope[];
  /** Giorni di scadenza, solo se tra i valori ammessi dal modulo. */
  days: string | null;
  /** Avvisi da mostrare all'utente per i valori scartati. */
  warnings: string[];
}

// Legge i valori proposti da `gs auth login --web` in /settings/tokens/new:
// ?name=gs&scopes=read:user,write:resource&expires=90. Gli scope fuori dal
// catalogo e una scadenza non ammessa sono scartati e nominati negli avvisi.
export function parseTokenPrefill(params: URLSearchParams): TokenPrefill {
  const warnings: string[] = [];
  const known = new Set<string>(TOKEN_SCOPES.map((s) => s.value));
  const wanted = new Set<TokenScope>();
  const unknown: string[] = [];
  for (const raw of (params.get('scopes') ?? '').split(',')) {
    const s = raw.trim();
    if (!s) continue;
    if (known.has(s)) wanted.add(s as TokenScope);
    else if (!unknown.includes(s)) unknown.push(s);
  }
  if (unknown.length > 0) {
    warnings.push(`Ignored unknown ${unknown.length === 1 ? 'scope' : 'scopes'}: ${unknown.join(', ')}.`);
  }
  const rawDays = params.get('expires');
  let days: string | null = null;
  if (rawDays !== null && rawDays.trim() !== '') {
    if ((TOKEN_EXPIRATIONS as readonly string[]).includes(rawDays.trim())) days = rawDays.trim();
    else warnings.push(`Ignored expiration "${rawDays}": use ${TOKEN_EXPIRATIONS.join(', ')} days.`);
  }
  return {
    name: (params.get('name') ?? '').trim(),
    scopes: TOKEN_SCOPES.map((s) => s.value).filter((s) => wanted.has(s)),
    days,
    warnings,
  };
}
