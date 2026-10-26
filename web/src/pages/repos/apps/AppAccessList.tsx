import * as Dialog from '@radix-ui/react-dialog';
import { Link as LinkIcon, Plus, Users } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { Button, CopyButton, ErrorAlert } from '../../../components';
import { addAppAccessMember, fetchAppAccessInvite, fetchAppAccessMembers, removeAppAccessMember } from '../../../lib/appsApi';
import type { App, AppAccessInvite, AppAccessMember } from '../../../lib/appsApi';
import { describeError } from '../../../lib/http';
import { searchUsers } from '../../../lib/issuesApi';
import { fetchTeams } from '../../../lib/orgsApi';
import type { Repository } from '../../../lib/reposApi';
import { useLoad } from '../../../lib/useLoad';

interface Props {
  repo: Repository;
  app: App;
  /** admin dell'app: aggiunge, toglie, invita. */
  canEdit: boolean;
}

type Kind = 'user' | 'team';

function initials(name: string): string {
  const base = name.split('/').pop() ?? name;
  return base.slice(0, 2).toUpperCase();
}

function addedLine(m: AppAccessMember): string {
  const by = m.addedBy?.username ? `added by ${m.addedBy.username}` : 'added';
  const day = new Date(m.addedAt);
  const when = Number.isNaN(day.getTime()) ? '' : ` · ${day.toLocaleDateString('en-GB', { day: 'numeric', month: 'short' })}`;
  return by + when;
}

/**
 * Lista di accesso dell'app (H6): mockup H7 data-screen="access" (variante interna) e
 * H13 data-screen="testers". Solo utenti e team di GitStack, nessun accesso anonimo.
 */
export function AppAccessList({ repo, app, canEdit }: Props) {
  const owner = repo.owner.name;
  const name = repo.name;
  const { data, loading, error, setData } = useLoad(() => fetchAppAccessMembers(owner, name, app.name));
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState(false);
  const [kind, setKind] = useState<Kind>('user');
  const [query, setQuery] = useState('');
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const seq = useRef(0);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [invite, setInvite] = useState<AppAccessInvite | null>(null);
  const [inviteError, setInviteError] = useState<string | null>(null);

  useEffect(() => {
    const q = query.trim();
    const mine = ++seq.current;
    if (!adding || q === '') {
      setSuggestions([]);
      return;
    }
    const run = kind === 'user'
      ? searchUsers(q).then((users) => users.map((u) => u.username as string))
      : fetchTeams(owner).then((teams) => teams.map((t) => `${owner}/${t.name}`).filter((n) => n.toLowerCase().includes(q.toLowerCase())));
    run
      .then((names) => {
        if (mine === seq.current) setSuggestions(names.slice(0, 8));
      })
      .catch(() => {
        if (mine === seq.current) setSuggestions([]);
      });
  }, [adding, kind, query, owner]);

  async function add(e: FormEvent) {
    e.preventDefault();
    const value = query.trim();
    if (!value) return;
    setBusy(true);
    setActionError(null);
    try {
      const created = await addAppAccessMember(owner, name, app.name, kind, value);
      setData([...(data ?? []).filter((m) => m.id !== created.id), created]);
      setQuery('');
      setSuggestions([]);
    } catch (err) {
      setActionError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove(m: AppAccessMember) {
    setBusy(true);
    setActionError(null);
    try {
      await removeAppAccessMember(owner, name, app.name, m.id);
      setData((data ?? []).filter((x) => x.id !== m.id));
    } catch (err) {
      setActionError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  async function openInvite() {
    setInviteOpen(true);
    setInvite(null);
    setInviteError(null);
    try {
      setInvite(await fetchAppAccessInvite(owner, name, app.name));
    } catch (err) {
      setInviteError(describeError(err));
    }
  }

  return (
    <div aria-label="Access list" role="region">
      <div className="sec-h">
        <h2>Access list</h2>
        {canEdit ? <span className="badge">admin only</span> : null}
        <span className="sp"></span>
        {canEdit ? (
          <>
            <Button onClick={() => void openInvite()}>
              <LinkIcon size={16} aria-hidden="true" />
              Invite testers
            </Button>
            <Button variant="primary" onClick={() => setAdding((v) => !v)}>
              <Plus size={16} aria-hidden="true" />
              Add people or team
            </Button>
          </>
        ) : null}
      </div>
      <p className="hint" style={{ marginBottom: 10 }}>
        People with read access to <b>{owner}/{name}</b> can always open {app.name}. People on this list can open <b>this app only</b>: they don&apos;t see code, issues or anything else in the repository, and get no role on it.
      </p>
      {error ? <ErrorAlert message={error} /> : null}
      {actionError ? <ErrorAlert message={actionError} /> : null}
      {canEdit && adding ? (
        <form className="row" style={{ gap: 8, marginBottom: 10, alignItems: 'flex-start' }} onSubmit={(e) => void add(e)}>
          <select className="select" aria-label="Kind" value={kind} onChange={(e) => { setKind(e.target.value as Kind); setQuery(''); }}>
            <option value="user">Person</option>
            <option value="team">Team</option>
          </select>
          <div className="stack" style={{ gap: 4, flex: 1 }}>
            <input
              className="input"
              aria-label={kind === 'user' ? 'Username' : 'Team (organization/team)'}
              placeholder={kind === 'user' ? 'Username' : 'organization/team'}
              autoComplete="off"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            {suggestions.length > 0 ? (
              <ul className="list" aria-label="Suggestions" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
                {suggestions.map((s) => (
                  <li key={s} className="list-row">
                    <button type="button" className="btn btn-sm btn-ghost" onClick={() => { setQuery(s); setSuggestions([]); }}>
                      {s}
                    </button>
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
          <Button type="submit" variant="primary" disabled={busy || query.trim() === ''}>
            Add
          </Button>
        </form>
      ) : null}
      {loading && data === null ? <p className="muted">Loading access list…</p> : null}
      {data && data.length === 0 ? <p className="muted">Nobody on the list yet.</p> : null}
      {data && data.length > 0 ? (
        <ul className="list" aria-label="Access list members" style={{ listStyle: 'none', margin: 0, padding: 0 }}>
          {data.map((m) => (
            <li key={m.id} className="list-row">
              <span className="avatar" style={m.kind === 'team' ? { borderRadius: 'var(--r)' } : undefined} aria-hidden="true">{initials(m.name)}</span>
              <div style={{ flex: 1 }}>
                <b>{m.name}</b>{' '}
                {m.kind === 'team' ? (
                  <span className="badge">team{m.memberCount != null ? ` · ${m.memberCount} ${m.memberCount === 1 ? 'person' : 'people'}` : ''}</span>
                ) : m.email ? (
                  <span className="small muted">{m.email}</span>
                ) : null}
                <div className="small muted">{addedLine(m)}</div>
              </div>
              {canEdit ? (
                <Button size="sm" variant="ghost" disabled={busy} aria-label={`Remove ${m.name}`} onClick={() => void remove(m)}>
                  Remove
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
      <div className="alert alert-info" style={{ marginTop: 10 }}>
        <Users size={16} aria-hidden="true" />
        <div>
          Only GitStack users and teams can be added: there is no anonymous access. A tester without an account needs one first, created by an administrator or through your company SSO.
        </div>
      </div>
      {canEdit ? (
        <Dialog.Root open={inviteOpen} onOpenChange={setInviteOpen}>
          <Dialog.Portal>
            <Dialog.Overlay className="dialog-overlay" />
            <Dialog.Content className="dialog dialog-wide">
              <Dialog.Title className="dialog-title">Invite testers to {app.name}</Dialog.Title>
              <Dialog.Description className="dialog-desc">
                Copy this text and send it to the people on the access list by email or chat. It contains the link to the certificate guide and the fingerprint they must check.
              </Dialog.Description>
              {inviteError ? <ErrorAlert message={inviteError} /> : null}
              {!invite && !inviteError ? <p className="muted">Loading invite…</p> : null}
              {invite ? (
                <>
                  <div className="cmdblock" data-testid="invite-text" style={{ whiteSpace: 'pre-wrap', fontSize: 12 }}>{invite.text}</div>
                  <p className="hint" style={{ marginTop: 8 }}>
                    {invite.fingerprintSha256
                      ? 'The fingerprint is taken from the instance certificate; it changes only if the GitStack CA is replaced.'
                      : 'The instance has no certificate to install: the text has no fingerprint.'}
                  </p>
                </>
              ) : null}
              <div className="dialog-actions">
                <Dialog.Close asChild>
                  <Button>Close</Button>
                </Dialog.Close>
                {invite ? <CopyButton value={invite.text} label="Copy text" /> : null}
              </div>
            </Dialog.Content>
          </Dialog.Portal>
        </Dialog.Root>
      ) : null}
    </div>
  );
}
