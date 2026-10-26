import { AlertTriangle, HardDrive, Globe, Key, Plus, Settings, ShieldCheck } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { Button, ConfirmDialog, EmptyState, ErrorAlert, FormField, TextInput, useToast } from '../../../components';
import { addAppVolume, fetchAppVersions, fetchAppVolumes, removeAppVolume, saveAppResources, updateApp } from '../../../lib/appsApi';
import type { App, AppVolume } from '../../../lib/appsApi';
import type { AppPermissions } from '../../../lib/appsPermissions';
import { formatCpu, formatMemory, percent } from '../../../lib/appsView';
import { formatDisk, looksLikeDatabase, parseBytes, parseCpuMillis } from '../../../lib/appVolumes';
import { describeError } from '../../../lib/http';
import type { Repository } from '../../../lib/reposApi';
import { useLoad } from '../../../lib/useLoad';
import { AppHeader } from './AppHeader';
import { appsPath } from './appMode';

interface Props {
  repo: Repository;
  app: App;
  perms: AppPermissions;
  onChange: (app: App) => void;
}

const MIN_VOLUME = 1024 * 1024;

/**
 * Volumes & resources (H3, H9, H11; mockup data-screen="volumes"): volumi con dimensione e mount, avviso database,
 * nota sulla copia unica, risorse garantite e tetto, modalità restricted. L'uscita di rete (H16) è di un altro item.
 */
export function AppVolumesPage({ repo, app, perms, onChange }: Props) {
  const owner = repo.owner.name;
  const name = repo.name;
  // Lo stato di «restricted» non è in App: sta nella configurazione della versione attiva.
  const { data, loading, error, reload } = useLoad(async () => {
    const [volumes, versions] = await Promise.all([fetchAppVolumes(owner, name, app.name), fetchAppVersions(owner, name, app.name)]);
    const active = versions.items.find((v) => v.number === app.activeVersion);
    return { volumes, restricted: active?.config?.restricted ?? false };
  });
  const [actionError, setActionError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [target, setTarget] = useState<AppVolume | null>(null);
  const [restricted, setRestricted] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const { toast } = useToast();

  const volumes = data?.volumes.items ?? [];
  const quota = data?.volumes.quota;
  const isRestricted = restricted ?? data?.restricted ?? false;
  const canEdit = perms.canConfigure;
  const dbVolume = volumes.find((v) => looksLikeDatabase(v.mountPath, app.image.reference));
  const dumpTarget = volumes.find((v) => v !== dbVolume);

  async function confirmRemove() {
    if (!target) return;
    try {
      await removeAppVolume(owner, name, app.name, target.name);
      setTarget(null);
      setActionError(null);
      onChange({ ...app, volumes: volumes.filter((v) => v.name !== target.name) });
      await reload();
    } catch (err) {
      setTarget(null);
      setActionError(describeError(err));
    }
  }

  async function toggleRestricted(next: boolean) {
    setBusy(true);
    setActionError(null);
    try {
      await updateApp(owner, name, app.name, { restricted: next });
      setRestricted(next);
      toast(next ? 'Restricted mode on. A new version was created.' : 'Restricted mode off. A new version was created.', 'success');
    } catch (err) {
      setActionError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <AppHeader repo={repo} app={app} mode="app-volumes" />
      <div className="page">
        <div className="settings-grid">
          <nav className="subnav" aria-label="App settings">
            <span className="subnav-disabled" aria-disabled="true">
              <Settings size={16} aria-hidden="true" />
              General
            </span>
            <Link to={appsPath(owner, name, app.name, 'settings')}>
              <Key size={16} aria-hidden="true" />
              Variables &amp; secrets
            </Link>
            <Link to={appsPath(owner, name, app.name, 'settings/access')}>
              <Globe size={16} aria-hidden="true" />
              Access &amp; domains
            </Link>
            <Link className="active" to={appsPath(owner, name, app.name, 'settings/volumes')} aria-current="page">
              <HardDrive size={16} aria-hidden="true" />
              Volumes &amp; resources
            </Link>
          </nav>
          <div className="stack" style={{ gap: 24 }}>
            {loading && data === null ? <p className="muted">Loading volumes…</p> : null}
            {error ? <ErrorAlert message={error} /> : null}
            {actionError ? <ErrorAlert message={actionError} /> : null}
            {data ? (
              <>
                <section aria-label="Volumes">
                  <div className="sec-h">
                    <h2>Volumes</h2>
                    <span className="badge">admin only</span>
                    <span className="sp" />
                    {canEdit ? (
                      <Button size="sm" onClick={() => setAdding((v) => !v)}>
                        <Plus size={14} aria-hidden="true" />
                        Add volume
                      </Button>
                    ) : null}
                  </div>
                  {adding && canEdit ? (
                    <AddVolumeForm
                      onCancel={() => setAdding(false)}
                      save={async (input) => {
                        await addAppVolume(owner, name, app.name, input);
                        setAdding(false);
                        setActionError(null);
                        onChange({ ...app, copies: 1 });
                        await reload();
                      }}
                    />
                  ) : null}
                  {volumes.length === 0 ? (
                    <EmptyState title="No volumes" description="Without a volume, the app's files are lost at every restart or deploy." />
                  ) : (
                    <div className="list" role="list" aria-label="Volumes">
                      {volumes.map((v) => (
                        <div className="list-row" role="listitem" key={v.name}>
                          <HardDrive size={16} className="muted" aria-hidden="true" />
                          <div style={{ flex: 1 }}>
                            <span className="mono" style={{ fontWeight: 600 }}>{v.name}</span> → <span className="mono">{v.mountPath}</span>
                            <div className="small muted">
                              {formatDisk(v.sizeBytes)} · counts toward the {data.volumes.quota.owner} disk quota
                            </div>
                          </div>
                          <label className="row small">
                            <input
                              type="checkbox"
                              role="switch"
                              aria-label={`${v.name} in backups`}
                              checked
                              disabled
                              title="Volumes are always in backups; excluding one per app is not available yet"
                              readOnly
                            />
                            In backups
                          </label>
                          {canEdit ? (
                            <Button size="sm" variant="ghost" aria-label={`Remove volume ${v.name}`} onClick={() => setTarget(v)}>
                              Remove
                            </Button>
                          ) : null}
                        </div>
                      ))}
                    </div>
                  )}
                  {quota ? <DiskQuota quota={quota} /> : null}
                  {dbVolume ? (
                    <div className="alert alert-warning" role="alert" style={{ marginTop: 10 }}>
                      <AlertTriangle size={16} aria-hidden="true" />
                      <div>
                        <b>Looks like a database.</b> Backups copy files while the app runs, so a database copy may not be consistent.{' '}
                        {dumpTarget ? (
                          <>
                            Schedule a dump into the <span className="mono">{dumpTarget.name}</span> volume.
                          </>
                        ) : (
                          <>Add a second volume and schedule a dump into it.</>
                        )}
                      </div>
                    </div>
                  ) : null}
                  <p className="hint" style={{ marginTop: 8 }}>
                    Apps with volumes run as a single copy. Managed databases are not part of GitStack v3.
                  </p>
                </section>

                <ResourcesSection
                  key={`${app.resources.guaranteed.cpuMillis}-${app.resources.limit.memoryBytes}`}
                  app={app}
                  hasVolumes={volumes.length > 0}
                  canEdit={canEdit}
                  onSaved={(resources) => {
                    onChange({ ...app, resources });
                    toast('Resources saved. A new version was created.', 'success');
                  }}
                />

                <section aria-label="Security">
                  <div className="sec-h">
                    <h2>Security</h2>
                  </div>
                  <div className="card card-b stack" style={{ gap: 10, fontSize: 13 }}>
                    <label className="row">
                      <input
                        type="checkbox"
                        role="switch"
                        aria-label="Restricted mode"
                        checked={isRestricted}
                        disabled={!canEdit || busy}
                        onChange={(e) => void toggleRestricted(e.target.checked)}
                      />
                      <b>Restricted mode</b>
                      <span className="muted">— refuse images that run as root</span>
                    </label>
                    {app.image.runsAsRoot ? (
                      <div className="alert alert-info">
                        <ShieldCheck size={16} aria-hidden="true" />
                        <div>
                          This image runs as <b>root</b> inside its container. It still can&apos;t use privileges, the server&apos;s disks or network, GitStack&apos;s internal services, or apps of other repositories.
                          Turn on restricted mode, or ask an administrator to require it for the whole instance.
                        </div>
                      </div>
                    ) : null}
                  </div>
                </section>
              </>
            ) : null}
          </div>
        </div>
      </div>
      <ConfirmDialog
        open={target !== null}
        onOpenChange={(open) => {
          if (!open) setTarget(null);
        }}
        title={`Remove volume ${target?.name ?? ''}?`}
        description={`The volume and all the data it contains are deleted. This cannot be undone. Type ${target?.name ?? ''} to confirm.`}
        confirmLabel="Remove volume"
        confirmInput={target ? { placeholder: target.name, expected: target.name } : undefined}
        destructive
        onConfirm={() => void confirmRemove()}
      />
    </>
  );
}

function DiskQuota({ quota }: { quota: { owner: string; limits: { diskBytes: number }; used: { diskBytes: number }; warning: boolean } }) {
  const pct = percent(quota.used.diskBytes, quota.limits.diskBytes);
  return (
    <div style={{ marginTop: 10 }}>
      <div className="meter" style={{ maxWidth: 320 }} role="img" aria-label={`Disk quota used ${pct}%`}>
        <i style={{ width: `${pct}%` }} />
      </div>
      <div className="small muted">
        {quota.owner} disk quota: {formatDisk(quota.used.diskBytes)} of {formatDisk(quota.limits.diskBytes)} used by volumes
        {quota.warning ? ' · above 80%' : ''}
      </div>
    </div>
  );
}

function AddVolumeForm({ save, onCancel }: { save: (input: { name: string; mountPath: string; sizeBytes: number }) => Promise<void>; onCancel: () => void }) {
  const [volName, setVolName] = useState('');
  const [mountPath, setMountPath] = useState('');
  const [size, setSize] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const sizeBytes = parseBytes(size, 'GB');
    if (!/^[a-z0-9]([a-z0-9-]{0,28}[a-z0-9])?$/.test(volName)) return setError('Use lowercase letters, digits and dashes, up to 30 characters.');
    if (!mountPath.startsWith('/') || mountPath === '/') return setError('The mount path must be absolute, such as /wiki/data.');
    if (sizeBytes === null || sizeBytes < MIN_VOLUME) return setError('Enter a size of at least 1 MB, such as 10 GB.');
    setBusy(true);
    setError(null);
    try {
      await save({ name: volName, mountPath, sizeBytes });
    } catch (err) {
      setError(describeError(err));
      setBusy(false);
    }
  }

  return (
    <form className="card stack" style={{ padding: 16, marginBottom: 10 }} onSubmit={(e) => void submit(e)} aria-label="Add volume">
      <FormField label="Volume name">
        <TextInput value={volName} onChange={(e) => setVolName(e.target.value)} placeholder="data" autoComplete="off" required />
      </FormField>
      <FormField label="Mount path">
        <TextInput value={mountPath} onChange={(e) => setMountPath(e.target.value)} placeholder="/wiki/data" autoComplete="off" required />
      </FormField>
      <FormField label="Size" hint="Fixed size, counted in the owner's disk quota. Without a unit, GB.">
        <TextInput value={size} onChange={(e) => setSize(e.target.value)} placeholder="10 GB" autoComplete="off" required />
      </FormField>
      {error ? <ErrorAlert message={error} /> : null}
      <div className="row">
        <Button type="submit" variant="primary" disabled={busy}>
          Add volume
        </Button>
        <Button onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

function ResourcesSection({ app, hasVolumes, canEdit, onSaved }: { app: App; hasVolumes: boolean; canEdit: boolean; onSaved: (r: App['resources']) => void }) {
  const r = app.resources;
  const [gCpu, setGCpu] = useState(`${formatCpu(r.guaranteed.cpuMillis)} vCPU`);
  const [gMem, setGMem] = useState(formatMemory(r.guaranteed.memoryBytes));
  const [lCpu, setLCpu] = useState(`${formatCpu(r.limit.cpuMillis)} vCPU`);
  const [lMem, setLMem] = useState(formatMemory(r.limit.memoryBytes));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const max = r.maxPerApp;
  const dirty =
    gCpu !== `${formatCpu(r.guaranteed.cpuMillis)} vCPU` ||
    gMem !== formatMemory(r.guaranteed.memoryBytes) ||
    lCpu !== `${formatCpu(r.limit.cpuMillis)} vCPU` ||
    lMem !== formatMemory(r.limit.memoryBytes);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const g = { cpuMillis: parseCpuMillis(gCpu), memoryBytes: parseBytes(gMem, 'MB') };
    const l = { cpuMillis: parseCpuMillis(lCpu), memoryBytes: parseBytes(lMem, 'MB') };
    if (g.cpuMillis === null || g.memoryBytes === null || l.cpuMillis === null || l.memoryBytes === null) {
      return setError('Enter CPU as vCPU (0.5) and memory as MB or GB (768 MB).');
    }
    if (g.cpuMillis > l.cpuMillis || g.memoryBytes > l.memoryBytes) return setError('The guaranteed amount cannot exceed the limit.');
    if (max && (l.cpuMillis > max.cpuMillis || l.memoryBytes > max.memoryBytes)) return setError(`The limit is above the maximum per app on this instance (${formatCpu(max.cpuMillis)} vCPU · ${formatMemory(max.memoryBytes)}).`);
    setBusy(true);
    setError(null);
    try {
      const saved = await saveAppResources(app.owner, app.repo, app.name, {
        guaranteed: { cpuMillis: g.cpuMillis, memoryBytes: g.memoryBytes },
        limit: { cpuMillis: l.cpuMillis, memoryBytes: l.memoryBytes },
      });
      onSaved(saved);
    } catch (err) {
      setError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  const input = (label: string, value: string, set: (v: string) => void) => (
    <input className="input" aria-label={label} value={value} onChange={(e) => set(e.target.value)} disabled={!canEdit || busy} style={{ maxWidth: 120 }} />
  );
  return (
    <section aria-label="Resources">
      <div className="sec-h">
        <h2>Resources</h2>
      </div>
      <form onSubmit={(e) => void submit(e)}>
        <dl className="kv">
          <dt>Guaranteed</dt>
          <dd className="row">
            {input('Guaranteed CPU', gCpu, setGCpu)}
            {input('Guaranteed memory', gMem, setGMem)}
            <span className="hint">Always reserved for this app</span>
          </dd>
          <dt>Limit</dt>
          <dd className="row">
            {input('Limit CPU', lCpu, setLCpu)}
            {input('Limit memory', lMem, setLMem)}
            <span className="hint">{max ? `Max per app on this instance: ${formatCpu(max.cpuMillis)} vCPU · ${formatMemory(max.memoryBytes)}` : 'Maximum per app set by the instance'}</span>
          </dd>
          <dt>Copies</dt>
          <dd className="row">
            <span className="stepper">
              <button type="button" disabled aria-label="Fewer copies">−</button>
              <span aria-label="Copies">{app.copies}</span>
              <button type="button" disabled aria-label="More copies">+</button>
            </span>
            <span className="hint">{hasVolumes ? 'Fixed at 1 because the app has volumes' : 'Change the number of copies from Overview'}</span>
          </dd>
        </dl>
        {error ? <ErrorAlert message={error} /> : null}
        {canEdit ? (
          <div className="row" style={{ marginTop: 10 }}>
            <Button type="submit" variant="primary" size="sm" disabled={busy || !dirty}>
              Save resources
            </Button>
          </div>
        ) : (
          <p className="hint">Only admins of the repository can change resources.</p>
        )}
      </form>
    </section>
  );
}
