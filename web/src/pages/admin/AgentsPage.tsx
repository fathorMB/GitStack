import { Bot, Check, Info, Key, Plus, Search } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import {
  Button,
  Checkbox,
  ConfirmDialog,
  CopyButton,
  EmptyState,
  ErrorAlert,
  FormField,
  Select,
  TextInput,
  useToast,
} from '../../components';
import { TOKEN_SCOPES } from '../../lib/accessApi';
import type { TokenScope } from '../../lib/accessApi';
import {
  createAgent,
  createAgentToken,
  deleteAgent,
  fetchAgents,
  fetchAgentTeams,
  fetchAgentTokens,
  revokeAgentToken,
  setAgentActive,
} from '../../lib/agentsApi';
import type { CreatedToken, Token, User } from '../../lib/agentsApi';
import { fetchSession } from '../../lib/authApi';
import { daysFromNow, formatDate, usedText } from '../../lib/format';
import { describeError } from '../../lib/http';
import { useLoad } from '../../lib/useLoad';

const EXPIRATIONS = [
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' },
  { value: '365', label: '365 days' },
];

// Admin · Agents (mockup 17). Solo l'amministratore dell'installazione (P4,
// P5): per chi non lo e' la pagina non carica niente e dice perche'. Il
// controllo vero e' nel backend (403); qui si evita di mostrare una console
// che non puo' funzionare.
export function AgentsPage() {
  const { data: session, loading, error } = useLoad(fetchSession);
  if (session === null) {
    return (
      <div>
        {error ? <ErrorAlert message={error} /> : null}
        {loading ? <p className="muted">Loading…</p> : null}
      </div>
    );
  }
  if (session.user.isAdmin !== true) {
    return (
      <div>
        <div className="page-h">
          <h1>Agents</h1>
        </div>
        <ErrorAlert message="Only installation administrators can manage agents." />
      </div>
    );
  }
  return <AgentsConsole />;
}

function AgentsConsole() {
  const { data, setData, loading, error } = useLoad(fetchAgents);
  const { toast } = useToast();
  const agents = data ?? [];
  const [query, setQuery] = useState('');
  const [selected, setSelected] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [username, setUsername] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const q = query.trim().toLowerCase();
  const visible = agents.filter(
    (a) => !q || a.username.toLowerCase().includes(q) || (a.displayName ?? '').toLowerCase().includes(q),
  );
  const current = agents.find((a) => a.username === selected) ?? visible[0] ?? null;

  function resetForm() {
    setCreating(false);
    setUsername('');
    setDisplayName('');
    setFormError(null);
  }

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!username.trim()) {
      setFormError('Give the agent a username.');
      return;
    }
    setSubmitting(true);
    setFormError(null);
    try {
      const agent = await createAgent(username.trim(), displayName.trim());
      setData([...agents, agent]);
      setSelected(agent.username);
      toast(`Agent "${agent.username}" created.`, 'success');
      resetForm();
    } catch (err) {
      setFormError(describeError(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div>
      <div className="row page-h">
        <h1>Agents</h1>
        <span className="badge">
          {agents.length} {agents.length === 1 ? 'agent' : 'agents'}
        </span>
        <span className="sp" />
        {!creating ? (
          <Button variant="primary" onClick={() => setCreating(true)}>
            <Plus size={16} aria-hidden="true" />
            New agent
          </Button>
        ) : null}
      </div>
      <div className="alert alert-info section-gap">
        <Info size={16} aria-hidden="true" />
        <div className="grow">
          Agent accounts have <b>no password</b> and sign in only with tokens you create here. They see only what their
          grants and teams allow: owners give access from each repository or from Organization · Teams.
        </div>
      </div>

      {error ? <ErrorAlert message={error} /> : null}

      <div className="agents-grid">
        <div className="list agents-list">
          <div className="list-row list-head">
            <div className="input-icon grow">
              <Search size={16} aria-hidden="true" />
              <input
                className="input"
                placeholder="Find an agent…"
                aria-label="Find an agent"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </div>
          </div>
          {loading && data === null ? <p className="muted pad">Loading agents…</p> : null}
          {data !== null && visible.length === 0 ? (
            <p className="muted small pad">{agents.length === 0 ? 'No agents yet.' : 'No agent matches.'}</p>
          ) : null}
          {visible.map((a) => (
            <div key={a.id} className={`list-row${a.username === current?.username ? ' selected' : ''}`}>
              <button type="button" className="row-main" onClick={() => setSelected(a.username)} aria-label={`Open ${a.username}`}>
                <span className="avatar av-bot">
                  <Bot size={14} aria-hidden="true" />
                </span>
                <span className="grow">
                  <span className="row">
                    <b>{a.username}</b>
                    <span className="badge-agent">agent</span>
                    {a.isActive === false ? <span className="badge">inactive</span> : null}
                  </span>
                  {a.displayName ? <span className="small muted">{a.displayName}</span> : null}
                </span>
              </button>
            </div>
          ))}
        </div>

        <div className="stack agents-detail">
          {current ? (
            <AgentDetail
              key={current.username}
              agent={current}
              onChanged={(u) => setData(agents.map((a) => (a.username === u.username ? u : a)))}
              onDeleted={() => {
                setData(agents.filter((a) => a.username !== current.username));
                setSelected(null);
              }}
            />
          ) : data !== null && !creating ? (
            <EmptyState icon={<Bot size={32} />} title="No agents" description="Create an agent account to give a coding agent its own tokens." />
          ) : null}

          {creating ? (
            <form className="card" onSubmit={(e) => void handleCreate(e)} noValidate aria-label="New agent">
              <div className="card-h">New agent</div>
              <div className="card-b stack form-stack">
                {formError ? <ErrorAlert message={formError} /> : null}
                <div className="row form-row">
                  <div className="grow">
                    <FormField label="Username">
                      <TextInput value={username} onChange={(e) => setUsername(e.target.value)} placeholder="release-agent" />
                    </FormField>
                  </div>
                  <div className="grow">
                    <FormField label="Display name">
                      <TextInput value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="Release agent" />
                    </FormField>
                  </div>
                </div>
                <div className="small muted">
                  No password is set. After creating it, generate a token here and give it access from the repositories or
                  teams it needs.
                </div>
                <div className="row">
                  <span className="sp" />
                  <Button onClick={resetForm}>Cancel</Button>
                  <Button type="submit" variant="primary" disabled={submitting}>
                    {submitting ? 'Creating…' : 'Create agent'}
                  </Button>
                </div>
              </div>
            </form>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function AgentDetail({
  agent,
  onChanged,
  onDeleted,
}: {
  agent: User;
  onChanged: (user: User) => void;
  onDeleted: () => void;
}) {
  const { toast } = useToast();
  const name = agent.username;
  const tokensLoad = useLoad(() => fetchAgentTokens(name));
  const teamsLoad = useLoad(() => fetchAgentTeams(name));
  const tokens = tokensLoad.data ?? [];

  const [creating, setCreating] = useState(false);
  const [tokenName, setTokenName] = useState('');
  const [days, setDays] = useState('90');
  const [scopes, setScopes] = useState<TokenScope[]>([]);
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  // Il valore del token vive solo qui: chiuso il riquadro non si recupera.
  const [created, setCreated] = useState<CreatedToken | null>(null);
  const [toRevoke, setToRevoke] = useState<Token | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  function resetForm() {
    setCreating(false);
    setTokenName('');
    setDays('90');
    setScopes([]);
    setFormError(null);
  }

  async function handleCreateToken(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!tokenName.trim()) {
      setFormError('Give the token a name.');
      return;
    }
    if (scopes.length === 0) {
      setFormError('Select at least one scope.');
      return;
    }
    setSubmitting(true);
    setFormError(null);
    try {
      const token = await createAgentToken(name, {
        name: tokenName.trim(),
        scopes: TOKEN_SCOPES.map((s) => s.value).filter((s) => scopes.includes(s)),
        expiresAt: daysFromNow(Number(days)),
      });
      const { token: _value, ...listed } = token;
      void _value;
      tokensLoad.setData([listed, ...tokens]);
      setCreated(token);
      resetForm();
    } catch (err) {
      setFormError(describeError(err));
    } finally {
      setSubmitting(false);
    }
  }

  async function handleRevoke(token: Token) {
    setActionError(null);
    try {
      await revokeAgentToken(name, token.id);
      tokensLoad.setData(tokens.filter((t) => t.id !== token.id));
      if (created?.id === token.id) setCreated(null);
      toast(`Token "${token.name}" revoked.`, 'success');
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  async function handleToggleActive() {
    setActionError(null);
    try {
      onChanged(await setAgentActive(name, agent.isActive === false));
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  async function handleDelete() {
    setActionError(null);
    try {
      await deleteAgent(name);
      toast(`Agent "${name}" deleted.`, 'success');
      onDeleted();
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  const teams = teamsLoad.data ?? [];

  return (
    <>
      <div className="card">
        <div className="card-h">
          <span className="avatar av-bot">
            <Bot size={14} aria-hidden="true" />
          </span>
          {name}
          <span className="badge-agent">agent</span>
          <span className="sp" />
          <Button size="sm" onClick={() => void handleToggleActive()}>
            {agent.isActive === false ? 'Activate' : 'Deactivate'}
          </Button>
          <Button size="sm" variant="danger" onClick={() => setConfirmDelete(true)}>
            Delete
          </Button>
        </div>
        <div className="card-b small muted">
          {agent.displayName ? `${agent.displayName} · ` : ''}
          {agent.createdAt ? `Created ${formatDate(agent.createdAt)} · ` : ''}no password · signs in with tokens only
        </div>
        {actionError ? <ErrorAlert message={actionError} /> : null}

        <div className="list">
          <div className="list-row list-head">
            <b className="grow">Tokens</b>
            {!creating ? (
              <Button size="sm" variant="primary" onClick={() => setCreating(true)}>
                <Plus size={14} aria-hidden="true" />
                Generate token
              </Button>
            ) : null}
          </div>
          {tokensLoad.error ? <ErrorAlert message={tokensLoad.error} /> : null}
          {tokensLoad.loading && tokensLoad.data === null ? <p className="muted small pad">Loading tokens…</p> : null}
          {tokensLoad.data !== null && tokens.length === 0 ? (
            <p className="muted small pad">No tokens: this agent cannot sign in yet.</p>
          ) : null}
          {tokens.map((t) => (
            <div key={t.id} className="list-row">
              <Key size={16} className="muted" aria-hidden="true" />
              <div className="grow">
                <b>{t.name}</b>
                <span className="mono small muted token-hint"> …{t.hint}</span>
                <div className="row small scope-badges">
                  {t.scopes.map((s) => (
                    <span key={s} className="badge badge-accent">
                      {s}
                    </span>
                  ))}
                </div>
              </div>
              <div className="small muted right">
                {usedText(t.lastUsedAt)}
                <br />
                {t.expiresAt ? `Expires ${formatDate(t.expiresAt)}` : 'No expiration'}
              </div>
              <Button size="sm" variant="danger" onClick={() => setToRevoke(t)} aria-label={`Revoke ${t.name}`}>
                Revoke
              </Button>
            </div>
          ))}
        </div>

        {creating ? (
          <form className="card-b stack form-stack" onSubmit={(e) => void handleCreateToken(e)} noValidate aria-label="New agent token">
            {formError ? <ErrorAlert message={formError} /> : null}
            <div className="row form-row">
              <div className="grow">
                <FormField label="Token name">
                  <TextInput value={tokenName} onChange={(e) => setTokenName(e.target.value)} placeholder="ci-runner" />
                </FormField>
              </div>
              <div className="w-200">
                <FormField label="Expiration">
                  <Select value={days} onValueChange={setDays} options={EXPIRATIONS} />
                </FormField>
              </div>
            </div>
            <fieldset className="scope-set">
              <legend>Scopes</legend>
              <div className="scope-grid">
                {TOKEN_SCOPES.map((s) => (
                  <div key={s.value} className="scope">
                    <Checkbox
                      label={<span className="mono">{s.value}</span>}
                      hint={s.description}
                      checked={scopes.includes(s.value)}
                      onCheckedChange={(on) =>
                        setScopes((cur) => (on === true ? [...cur, s.value] : cur.filter((x) => x !== s.value)))
                      }
                    />
                  </div>
                ))}
              </div>
            </fieldset>
            <div className="row">
              <span className="sp" />
              <Button onClick={resetForm}>Cancel</Button>
              <Button type="submit" variant="primary" disabled={submitting}>
                {submitting ? 'Generating…' : 'Create token'}
              </Button>
            </div>
          </form>
        ) : null}

        {created ? (
          <div className="card-b" role="status" aria-label="New token">
            <div className="newtoken">
              <div className="row">
                <Check size={16} className="ok-icon" aria-hidden="true" />
                <b>
                  Token &quot;{created.name}&quot; created for {name}.
                </b>
                <span className="muted">Copy it now: you won&apos;t be able to see it again.</span>
              </div>
              <div className="row nowrap">
                <input className="input mono" aria-label="New token value" value={created.token} readOnly />
                <CopyButton value={created.token} />
                <Button onClick={() => setCreated(null)}>Done</Button>
              </div>
            </div>
          </div>
        ) : null}
      </div>

      <div className="card">
        <div className="card-h">
          Effective access
          <span className="sp" />
          <span className="small muted">from grants, teams and visibility</span>
        </div>
        <div className="list">
          {teamsLoad.error ? <ErrorAlert message={teamsLoad.error} /> : null}
          {teamsLoad.loading && teamsLoad.data === null ? <p className="muted small pad">Loading access…</p> : null}
          {teams.map((t) => (
            <div key={`${t.org}/${t.team}`} className="list-row">
              <span className="mono grow">
                {t.org}/{t.team}
              </span>
              <span className="small muted">team membership</span>
            </div>
          ))}
          {teamsLoad.data !== null && teams.length === 0 ? <p className="muted small pad">Not a member of any team.</p> : null}
        </div>
        <div className="card-b small muted">
          Direct grants and the repositories reachable through teams or internal visibility are not listed yet: the API
          has no per-user view of them. Manage them from each repository&apos;s access settings or from
          Organization · Teams.
        </div>
      </div>

      <ConfirmDialog
        open={toRevoke !== null}
        onOpenChange={(open) => {
          if (!open) setToRevoke(null);
        }}
        title="Revoke token?"
        description={`Anything using "${toRevoke?.name ?? ''}" will stop working immediately. This cannot be undone.`}
        confirmLabel="Revoke"
        destructive
        onConfirm={() => {
          if (toRevoke) void handleRevoke(toRevoke);
        }}
      />
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title="Delete agent?"
        description={`"${name}" will be deleted with its tokens and memberships. This cannot be undone.`}
        confirmLabel="Delete"
        destructive
        onConfirm={() => void handleDelete()}
      />
    </>
  );
}
