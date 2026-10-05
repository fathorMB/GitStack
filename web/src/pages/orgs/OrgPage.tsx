import { Plus, Trash2, User as UserIcon, Users } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Button, ConfirmDialog, EmptyState, ErrorAlert, FormField, TextInput, useToast } from '../../components';
import { fetchSession } from '../../lib/authApi';
import type { User } from '../../lib/authApi';
import { describeError, fieldErrors } from '../../lib/http';
import {
  createOrgTeam,
  deleteOrgMember,
  deleteOrgTeam,
  deleteTeamMember,
  fetchOrganization,
  fetchOrgMembers,
  fetchTeamMembers,
  fetchTeams,
  saveOrgMember,
  saveTeamMember,
} from '../../lib/orgsApi';
import type { OrgMember, OrgRole, Team, TeamMember, TeamRole } from '../../lib/orgsApi';
import { useLoad } from '../../lib/useLoad';

type Tab = 'members' | 'teams';

const ORG_ROLES: { value: OrgRole; label: string }[] = [
  { value: 'owner', label: 'Owner' },
  { value: 'member', label: 'Member' },
];
const TEAM_ROLES: { value: TeamRole; label: string }[] = [
  { value: 'maintainer', label: 'Maintainer' },
  { value: 'member', label: 'Member' },
];

function initials(user: User): string {
  return (user.displayName || user.username).slice(0, 2).toUpperCase();
}

function RoleSelect<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
}) {
  return (
    <select className="select inline-select" aria-label={label} value={value} onChange={(e) => onChange(e.target.value as T)}>
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

// Organizzazione (mockup 15): intestazione, tab Members / Teams, elenco team
// a sinistra e membri del team scelto a destra. Le azioni che il ruolo
// dell'utente non consente sono nascoste; il controllo vero e' nel backend
// e un suo 403 arriva comunque come messaggio.
export function OrgPage() {
  const { org } = useParams<{ org: string }>();
  return <OrgView key={org} org={org ?? ''} />;
}

function OrgView({ org }: { org: string }) {
  const { data, loading, error, reload } = useLoad(async () => {
    const [organization, members, teams, session] = await Promise.all([
      fetchOrganization(org),
      fetchOrgMembers(org),
      fetchTeams(org),
      fetchSession(),
    ]);
    return { organization, members, teams, me: session.user };
  });
  const [tab, setTab] = useState<Tab>('teams');

  if (data === null) {
    return (
      <div>
        <p className="small">
          <Link to="/orgs">← Organizations</Link>
        </p>
        {error ? <ErrorAlert message={error} /> : null}
        {loading ? <p className="muted">Loading organization…</p> : null}
      </div>
    );
  }

  const { organization, members, teams, me } = data;
  const myRole = members.find((m) => m.user.username === me.username)?.role;
  // user.isAdmin vede tutto; l'owner gestisce membri e team.
  const canManageOrg = me.isAdmin === true || myRole === 'owner';
  const title = organization.displayName || organization.name;

  return (
    <div>
      <div className="row page-h">
        <span className="avatar avatar-lg org-avatar" aria-hidden="true">
          {title.slice(0, 2).toUpperCase()}
        </span>
        <div>
          <h1>{title}</h1>
          <div className="muted small">
            {organization.description ? `${organization.description} · ` : ''}
            {members.length} members · {teams.length} teams
          </div>
        </div>
        <span className="sp" />
        {canManageOrg ? (
          <Link to={`/orgs/${organization.name}/deleted-repos`} className="small">
            Deleted repositories
          </Link>
        ) : null}
        <Link to="/orgs" className="small">
          All organizations
        </Link>
      </div>
      {error ? <ErrorAlert message={error} /> : null}

      <div className="tabs" role="tablist" aria-label="Organization sections">
        <button type="button" role="tab" aria-selected={tab === 'members'} className={`tab${tab === 'members' ? ' active' : ''}`} onClick={() => setTab('members')}>
          <UserIcon size={16} aria-hidden="true" />
          Members <span className="counter">{members.length}</span>
        </button>
        <button type="button" role="tab" aria-selected={tab === 'teams'} className={`tab${tab === 'teams' ? ' active' : ''}`} onClick={() => setTab('teams')}>
          <Users size={16} aria-hidden="true" />
          Teams <span className="counter">{teams.length}</span>
        </button>
      </div>

      {tab === 'members' ? (
        <MembersTab org={org} members={members} me={me} canManage={canManageOrg} onChanged={reload} />
      ) : (
        <TeamsTab org={org} teams={teams} canManageOrg={canManageOrg} onChanged={reload} />
      )}
    </div>
  );
}

function MembersTab({
  org,
  members,
  me,
  canManage,
  onChanged,
}: {
  org: string;
  members: OrgMember[];
  me: User;
  canManage: boolean;
  onChanged: () => Promise<void>;
}) {
  const { toast } = useToast();
  const [username, setUsername] = useState('');
  const [role, setRole] = useState<OrgRole>('member');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [toRemove, setToRemove] = useState<OrgMember | null>(null);

  async function handleAdd(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!username.trim()) {
      setFields({ username: 'Enter a username.' });
      return;
    }
    setFields({});
    setFormError(null);
    try {
      await saveOrgMember(org, username.trim(), role);
      toast(`${username.trim()} added to ${org}.`, 'success');
      setUsername('');
      await onChanged();
    } catch (err) {
      const perField = fieldErrors(err);
      setFields(perField);
      setFormError(Object.keys(perField).length > 0 ? null : describeError(err));
    }
  }

  async function changeRole(member: OrgMember, next: OrgRole) {
    setActionError(null);
    try {
      await saveOrgMember(org, member.user.username, next);
      await onChanged();
    } catch (err) {
      // Es. 409 last_owner: il messaggio del backend resta accanto alla lista.
      setActionError(describeError(err));
    }
  }

  async function remove(member: OrgMember) {
    setActionError(null);
    try {
      await deleteOrgMember(org, member.user.username);
      toast(`${member.user.username} removed from ${org}.`, 'success');
      await onChanged();
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  return (
    <div className="stack" style={{ gap: 16 }}>
      {actionError ? <ErrorAlert message={actionError} /> : null}
      {canManage ? (
        <form className="card" onSubmit={(e) => void handleAdd(e)} noValidate aria-label="Add member">
          <div className="card-h">Add member</div>
          <div className="card-b stack form-stack">
            {formError ? <ErrorAlert message={formError} /> : null}
            <div className="row form-row">
              <div className="grow">
                <FormField label="Username" error={fields.username}>
                  <TextInput value={username} onChange={(e) => setUsername(e.target.value)} placeholder="mrossi" />
                </FormField>
              </div>
              <div className="w-200">
                <FormField label="Role" error={fields.role}>
                  <select className="select" value={role} onChange={(e) => setRole(e.target.value as OrgRole)}>
                    {ORG_ROLES.map((r) => (
                      <option key={r.value} value={r.value}>
                        {r.label}
                      </option>
                    ))}
                  </select>
                </FormField>
              </div>
            </div>
            <div className="row">
              <span className="sp" />
              <Button type="submit" variant="primary">
                <Plus size={16} aria-hidden="true" />
                Add member
              </Button>
            </div>
          </div>
        </form>
      ) : null}

      <ul className="list list-clean" aria-label="Members">
        {members.map((m) => {
          const self = m.user.username === me.username;
          return (
            <li key={m.user.username} className="list-row">
              <span className="avatar" aria-hidden="true">
                {initials(m.user)}
              </span>
              <div className="grow">
                <b>{m.user.username}</b> <span className="muted small">{m.user.displayName}</span>
                {m.user.kind === 'agent' ? <span className="badge badge-agent">agent</span> : null}
              </div>
              {canManage ? (
                <RoleSelect
                  label={`Role of ${m.user.username}`}
                  value={m.role}
                  options={ORG_ROLES}
                  onChange={(next) => void changeRole(m, next)}
                />
              ) : (
                <span className={`badge${m.role === 'owner' ? ' badge-accent' : ''}`}>{m.role === 'owner' ? 'Owner' : 'Member'}</span>
              )}
              {canManage || self ? (
                <Button size="sm" variant="danger" onClick={() => setToRemove(m)} aria-label={`${self && !canManage ? 'Leave' : 'Remove'} ${m.user.username}`}>
                  {self && !canManage ? 'Leave' : 'Remove'}
                </Button>
              ) : null}
            </li>
          );
        })}
      </ul>

      <ConfirmDialog
        open={toRemove !== null}
        onOpenChange={(open) => {
          if (!open) setToRemove(null);
        }}
        title="Remove member?"
        description={`"${toRemove?.user.username ?? ''}" will lose access through ${org}, including all its teams.`}
        confirmLabel="Remove"
        destructive
        onConfirm={() => {
          if (toRemove) void remove(toRemove);
        }}
      />
    </div>
  );
}

function TeamsTab({
  org,
  teams,
  canManageOrg,
  onChanged,
}: {
  org: string;
  teams: Team[];
  canManageOrg: boolean;
  onChanged: () => Promise<void>;
}) {
  const { toast } = useToast();
  const [filter, setFilter] = useState('');
  const [selected, setSelected] = useState<string | null>(teams[0]?.name ?? null);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);

  const visible = teams.filter((t) => t.name.toLowerCase().includes(filter.trim().toLowerCase()));
  const current = teams.find((t) => t.name === selected) ?? null;

  function resetForm() {
    setAdding(false);
    setName('');
    setDescription('');
    setFields({});
    setFormError(null);
  }

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!name.trim()) {
      setFields({ name: 'Enter a name.' });
      return;
    }
    setFields({});
    setFormError(null);
    try {
      const team = await createOrgTeam(org, {
        name: name.trim(),
        ...(description.trim() ? { description: description.trim() } : {}),
      });
      toast(`Team "${team.name}" created.`, 'success');
      resetForm();
      setSelected(team.name);
      await onChanged();
    } catch (err) {
      const perField = fieldErrors(err);
      setFields(perField);
      setFormError(Object.keys(perField).length > 0 ? null : describeError(err));
    }
  }

  return (
    <div className="org-grid">
      <div className="stack" style={{ gap: 16 }}>
        {adding ? (
          <form className="card" onSubmit={(e) => void handleCreate(e)} noValidate aria-label="New team">
            <div className="card-h">New team</div>
            <div className="card-b stack form-stack">
              {formError ? <ErrorAlert message={formError} /> : null}
              <FormField label="Team name" error={fields.name}>
                <TextInput value={name} onChange={(e) => setName(e.target.value)} placeholder="platform" />
              </FormField>
              <FormField label="Description" error={fields.description}>
                <TextInput value={description} onChange={(e) => setDescription(e.target.value)} />
              </FormField>
              <div className="row">
                <span className="sp" />
                <Button onClick={resetForm}>Cancel</Button>
                <Button type="submit" variant="primary">
                  Create team
                </Button>
              </div>
            </div>
          </form>
        ) : null}
        <div className="list">
          <div className="list-row list-head">
            <TextInput aria-label="Find a team" placeholder="Find a team…" value={filter} onChange={(e) => setFilter(e.target.value)} />
            {canManageOrg && !adding ? (
              <Button variant="primary" size="sm" onClick={() => setAdding(true)}>
                <Plus size={16} aria-hidden="true" />
                New team
              </Button>
            ) : null}
          </div>
          {visible.length === 0 ? (
            <div className="list-row muted small">{teams.length === 0 ? 'No teams yet.' : 'No team matches.'}</div>
          ) : (
            visible.map((t) => (
              <div key={t.id} className={`list-row${t.name === selected ? ' selected' : ''}`}>
                <button type="button" className="row-main" onClick={() => setSelected(t.name)} aria-current={t.name === selected ? 'true' : undefined}>
                  <Users size={16} className={t.name === selected ? '' : 'muted'} aria-hidden="true" />
                  <span>
                    <b>{t.name}</b>
                    {t.description ? <span className="small muted"> · {t.description}</span> : null}
                  </span>
                </button>
              </div>
            ))
          )}
        </div>
      </div>

      {current ? (
        <TeamPanel
          key={current.name}
          org={org}
          team={current}
          canManageOrg={canManageOrg}
          onDeleted={async () => {
            setSelected(null);
            await onChanged();
          }}
        />
      ) : (
        <EmptyState icon={<Users size={32} />} title="No team selected" description="Select a team to see its members." />
      )}
    </div>
  );
}

function TeamPanel({
  org,
  team,
  canManageOrg,
  onDeleted,
}: {
  org: string;
  team: Team;
  canManageOrg: boolean;
  onDeleted: () => Promise<void>;
}) {
  const { toast } = useToast();
  const { data, loading, error, reload } = useLoad(async () => {
    const [members, session] = await Promise.all([fetchTeamMembers(org, team.name), fetchSession()]);
    return { members, me: session.user };
  });
  const [username, setUsername] = useState('');
  const [role, setRole] = useState<TeamRole>('member');
  const [fields, setFields] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const members = data?.members ?? [];
  const me = data?.me;
  const myTeamRole = me ? members.find((m) => m.user.username === me.username)?.role : undefined;
  // Owner/admin gestiscono ogni team; un maintainer solo il proprio.
  const canManage = canManageOrg || myTeamRole === 'maintainer';

  async function handleAdd(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!username.trim()) {
      setFields({ username: 'Enter a username.' });
      return;
    }
    setFields({});
    setFormError(null);
    try {
      await saveTeamMember(org, team.name, username.trim(), role);
      toast(`${username.trim()} added to ${team.name}.`, 'success');
      setUsername('');
      await reload();
    } catch (err) {
      const perField = fieldErrors(err);
      setFields(perField);
      setFormError(Object.keys(perField).length > 0 ? null : describeError(err));
    }
  }

  async function changeRole(member: TeamMember, next: TeamRole) {
    setActionError(null);
    try {
      await saveTeamMember(org, team.name, member.user.username, next);
      await reload();
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  async function remove(member: TeamMember) {
    setActionError(null);
    try {
      await deleteTeamMember(org, team.name, member.user.username);
      await reload();
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  async function removeTeam() {
    setActionError(null);
    try {
      await deleteOrgTeam(org, team.name);
      toast(`Team "${team.name}" deleted.`, 'success');
      await onDeleted();
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  return (
    <div className="card" aria-label={`Team ${team.name}`}>
      <div className="card-h">
        <Users size={16} aria-hidden="true" />
        {team.name}
        <span className="sp" />
        {canManageOrg ? (
          <Button size="sm" variant="danger" onClick={() => setConfirmDelete(true)}>
            <Trash2 size={14} aria-hidden="true" />
            Delete team
          </Button>
        ) : null}
      </div>
      <div className="card-b stack form-stack">
        {error ? <ErrorAlert message={error} /> : null}
        {actionError ? <ErrorAlert message={actionError} /> : null}
        {canManage ? (
          <form className="stack form-stack" onSubmit={(e) => void handleAdd(e)} noValidate aria-label="Add team member">
            {formError ? <ErrorAlert message={formError} /> : null}
            <div className="row form-row">
              <div className="grow">
                <FormField label="Team member username" error={fields.username}>
                  <TextInput value={username} onChange={(e) => setUsername(e.target.value)} placeholder="lbianchi" />
                </FormField>
              </div>
              <div className="w-200">
                <FormField label="Team role" error={fields.role}>
                  <select className="select" value={role} onChange={(e) => setRole(e.target.value as TeamRole)}>
                    {TEAM_ROLES.map((r) => (
                      <option key={r.value} value={r.value}>
                        {r.label}
                      </option>
                    ))}
                  </select>
                </FormField>
              </div>
            </div>
            <div className="row">
              <span className="sp" />
              <Button type="submit" size="sm">
                <Plus size={16} aria-hidden="true" />
                Add to team
              </Button>
            </div>
          </form>
        ) : null}
      </div>
      {loading && data === null ? (
        <p className="muted card-b">Loading members…</p>
      ) : members.length === 0 ? (
        error ? null : <p className="muted card-b small">No members in this team.</p>
      ) : (
        <ul className="list list-clean" aria-label={`Members of ${team.name}`}>
          {members.map((m) => (
            <li key={m.user.username} className="list-row">
              <span className="avatar" aria-hidden="true">
                {initials(m.user)}
              </span>
              <div className="grow">
                <b>{m.user.username}</b> <span className="muted small">{m.user.displayName}</span>
              </div>
              {canManage ? (
                <RoleSelect
                  label={`Team role of ${m.user.username}`}
                  value={m.role}
                  options={TEAM_ROLES}
                  onChange={(next) => void changeRole(m, next)}
                />
              ) : (
                <span className={`badge${m.role === 'maintainer' ? ' badge-accent' : ''}`}>
                  {m.role === 'maintainer' ? 'Maintainer' : 'Member'}
                </span>
              )}
              {canManage ? (
                <Button size="sm" variant="danger" onClick={() => void remove(m)} aria-label={`Remove ${m.user.username} from ${team.name}`}>
                  Remove
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title="Delete team?"
        description={`"${team.name}" and its memberships will be deleted.`}
        confirmLabel="Delete"
        destructive
        onConfirm={() => void removeTeam()}
      />
    </div>
  );
}
