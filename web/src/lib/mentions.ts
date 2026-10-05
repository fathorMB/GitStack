// Suggerimenti di @menzione (I8). Solo il suggerimento: le notifiche sono di M-06.
import { fetchTeams } from './orgsApi';
import { searchUsers } from './issuesApi';

export type MentionKind = 'human' | 'agent' | 'team';
export interface MentionCandidate {
  /** Nome da inserire dopo «@»: `utente` oppure `org/team`. */
  handle: string;
  kind: MentionKind;
  label?: string;
}
/** Sorgente iniettabile: oggi listUsers + listTeams, domani un endpoint filtrato per visibilita'. */
export type MentionSource = (prefix: string) => Promise<MentionCandidate[]>;

export const MAX_MENTIONS = 8;

/** Il token «@xxx» che finisce al cursore, oppure null. Senza prefisso (solo «@») non c'e' ricerca. */
export function mentionAt(text: string, caret: number): { start: number; prefix: string } | null {
  const before = text.slice(0, caret);
  const m = /(^|[\s(])@([A-Za-z0-9._/-]+)$/.exec(before);
  if (!m) return null;
  return { start: before.length - m[2].length - 1, prefix: m[2] };
}

/** Sostituisce il token «@prefisso» con «@handle » e ritorna il nuovo testo e la posizione del cursore. */
export function applyMention(text: string, caret: number, start: number, handle: string): { text: string; caret: number } {
  const insert = `@${handle} `;
  return { text: text.slice(0, start) + insert + text.slice(caret), caret: start + insert.length };
}

/** Persone e agenti da listUsers?q=; se il proprietario e' un'organizzazione anche i suoi team (@org/team). */
export function defaultMentionSource(owner: { type: string; name: string }): MentionSource {
  let teams: Promise<string[]> | null = null;
  const teamNames = () => (teams ??= fetchTeams(owner.name).then((t) => t.map((x) => x.name)).catch(() => [] as string[]));
  return async (prefix) => {
    const q = prefix.toLowerCase();
    if (!q) return [];
    const out: MentionCandidate[] = [];
    if (!q.includes('/')) {
      const users = await searchUsers(prefix, MAX_MENTIONS).catch(() => []);
      for (const u of users) {
        if (u.isActive === false || !u.username.toLowerCase().startsWith(q)) continue;
        out.push({ handle: u.username, kind: u.kind, label: u.displayName });
      }
    }
    if (owner.type === 'organization') {
      for (const t of await teamNames()) {
        const handle = `${owner.name}/${t}`;
        if (t.toLowerCase().startsWith(q) || handle.toLowerCase().startsWith(q)) out.push({ handle, kind: 'team' });
      }
    }
    return out.slice(0, MAX_MENTIONS);
  };
}
