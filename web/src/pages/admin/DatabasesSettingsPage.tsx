import { useState } from 'react';
import type { FormEvent } from 'react';
import { Button, ErrorAlert, FormField, TextInput, useToast } from '../../components';
import { fetchSession } from '../../lib/authApi';
import { fetchAdminDatabasesData, resetDatabaseOwnerQuota, saveDatabaseOwnerQuota, saveDatabasesSettings } from '../../lib/adminDatabasesApi';
import type { AppOwnerQuota, Database, DatabasesInstanceSettings } from '../../lib/adminDatabasesApi';
import { describeError } from '../../lib/http';
import { useLoad } from '../../lib/useLoad';

const GiB = 1024 ** 3;
const MiB = 1024 ** 2;
const vcpu = (n: number) => `${(n / 1000).toLocaleString(undefined, { maximumFractionDigits: 2 })} vCPU`;
const memory = (n: number) => n >= GiB ? `${(n / GiB).toLocaleString(undefined, { maximumFractionDigits: 2 })} GB` : `${Math.round(n / MiB)} MB`;
const disk = (n: number) => `${(n / GiB).toLocaleString(undefined, { maximumFractionDigits: 1 })} GB`;
const MAJORS = [16, 17] as const;

export function DatabasesSettingsPage() {
  const { data: session, loading, error } = useLoad(fetchSession);
  if (loading && !session) return <p className="muted">Loading…</p>;
  if (error) return <ErrorAlert message={error} />;
  if (!session?.user.isAdmin) return <><div className="page-h"><h1>Database settings</h1></div><ErrorAlert message="Only installation administrators can manage database settings." /></>;
  return <DatabasesSettingsConsole />;
}

function DatabasesSettingsConsole() {
  const { data, setData, loading, error } = useLoad(fetchAdminDatabasesData);
  const { toast } = useToast();
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState('');
  const [quotaOwner, setQuotaOwner] = useState<AppOwnerQuota | null>(null);
  if (loading && !data) return <p className="muted">Loading…</p>;
  if (error) return <><div className="page-h"><h1>Database settings</h1></div><ErrorAlert message={error} /></>;
  if (!data) return null;

  // La funzione è spenta (DB23): si accende dal chart, non da qui. Nessun 501, nessuna pagina vuota.
  if (data.disabled) {
    return <div>
      <div className="page-h"><h1>Database settings</h1></div>
      <section className="card" aria-labelledby="databases-off"><h2 className="card-h" id="databases-off">Managed databases are off</h2><div className="card-b stack">
        <p>This installation does not host managed databases. Nothing is created in the cluster while they are off.</p>
        <p>Turn them on from the server: <code>sudo gitstack config set databases on</code>, or <code>sudo gitstack upgrade --enable-databases</code>. Turning them off again never deletes a database or its data.</p>
      </div></section>
    </div>;
  }

  async function saveSettings(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const allowedMajors = MAJORS.filter((m) => form.get(`major-${m}`) === 'on');
    setBusy(true); setFormError('');
    try {
      const settings = await saveDatabasesSettings({
        enabled: true,
        allowedMajors: [...allowedMajors],
        maxPerDatabase: { sizeBytes: Math.round(Number(form.get('maxSize')) * GiB), cpuMillis: Math.round(Number(form.get('maxCpu')) * 1000), memoryBytes: Math.round(Number(form.get('maxMemory')) * GiB) },
        lanPortRange: { from: Number(form.get('lanFrom')), to: Number(form.get('lanTo')) },
        backupRetention: { scheduled: Number(form.get('keepScheduled')), onDemand: Number(form.get('keepOnDemand')) },
      });
      setData((previous) => previous ? { ...previous, settings } : previous);
      toast('Database settings saved.', 'success');
    } catch (e) { setFormError(describeError(e)); }
    finally { setBusy(false); }
  }
  async function saveQuota(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!quotaOwner) return;
    const form = new FormData(event.currentTarget);
    setBusy(true); setFormError('');
    try {
      const updated = await saveDatabaseOwnerQuota(quotaOwner.owner, { cpuMillis: Number(form.get('cpu')) * 1000, memoryBytes: Number(form.get('memory')) * GiB, diskBytes: Number(form.get('disk')) * GiB });
      setData((previous) => previous ? { ...previous, quotas: (previous.quotas ?? []).map((q) => q.owner === updated.owner ? updated : q) } : previous);
      setQuotaOwner(null); toast(`Quota for ${updated.owner} saved.`, 'success');
    } catch (e) { setFormError(describeError(e)); }
    finally { setBusy(false); }
  }
  async function resetQuota(owner: string) {
    setBusy(true); setFormError('');
    try {
      const quotas = await resetDatabaseOwnerQuota(owner);
      setData((previous) => previous ? { ...previous, quotas } : previous);
    } catch (e) { setFormError(describeError(e)); }
    finally { setBusy(false); }
  }

  const { settings, databases, quotas, errors } = data;
  const failedBackups = (databases ?? []).filter((d) => d.operations?.backupError).length;
  return <div>
    <div className="row page-h"><h1>Database settings</h1><span className="sp" />{databases ? <span className="small muted">{databases.length} database{databases.length === 1 ? '' : 's'}{failedBackups > 0 ? ` · ${failedBackups} failed backup${failedBackups === 1 ? '' : 's'}` : ''}</span> : null}</div>
    {formError ? <ErrorAlert message={formError} /> : null}
    <div className="admin-apps-grid">
      {!settings ? <SectionError id="db-instance-settings" title="Instance settings" message={errors.settings} /> : <InstanceSettings settings={settings} busy={busy} onSubmit={saveSettings} />}
      {!quotas ? <SectionError id="db-owner-quotas" title="Quotas by owner" message={errors.quotas} /> : <section className="card" aria-labelledby="db-owner-quotas"><h2 className="card-h" id="db-owner-quotas">Quotas by owner</h2>
        <p className="hint card-b">Shared with apps and volumes: databases count against the same CPU, memory and disk of each owner.</p>
        <div className="list">{quotas.map((q) => <div className="list-row" key={q.owner}><div style={{ flex: 1 }}><b>{q.owner}</b><div className="small muted">{vcpu(q.used.cpuMillis)} / {vcpu(q.limits.cpuMillis)} · {memory(q.used.memoryBytes)} / {memory(q.limits.memoryBytes)} · disk {disk(q.used.diskBytes)} / {disk(q.limits.diskBytes)}</div></div><Button size="sm" onClick={() => setQuotaOwner(q)}>Edit</Button>{!q.isDefault ? <Button size="sm" variant="ghost" disabled={busy} onClick={() => void resetQuota(q.owner)}>Reset</Button> : null}</div>)}</div></section>}
      {!databases ? <SectionError id="db-list-title" title="Databases" message={errors.databases} className="card admin-app-list" /> : <section className="card admin-app-list" aria-labelledby="db-list-title"><h2 className="card-h" id="db-list-title">Databases</h2>
        {databases.length === 0 ? <p className="muted card-b">No databases on this instance yet.</p> : <div className="list"><div className="list-row list-head"><span>Database</span><span>PostgreSQL</span><span>Status</span><span>Resources</span><span>LAN</span><span>Last backup</span></div>{databases.map((d) => <DatabaseRow key={d.id} db={d} />)}</div>}
      </section>}
    </div>
    {quotaOwner ? <div className="modal-back" role="presentation"><form className="modal" aria-label={`Edit quota for ${quotaOwner.owner}`} onSubmit={saveQuota}><h2 className="modal-h">Edit quota · {quotaOwner.owner}</h2><div className="modal-b stack"><FormField label="Guaranteed CPU (vCPU)"><TextInput name="cpu" type="number" min="0.1" step="0.1" defaultValue={quotaOwner.limits.cpuMillis / 1000} /></FormField><FormField label="Memory (GB)"><TextInput name="memory" type="number" min="0.1" step="0.1" defaultValue={quotaOwner.limits.memoryBytes / GiB} /></FormField><FormField label="Disk (GB)"><TextInput name="disk" type="number" min="0" step="1" defaultValue={quotaOwner.limits.diskBytes / GiB} /></FormField></div><div className="modal-f"><Button onClick={() => setQuotaOwner(null)}>Cancel</Button><Button type="submit" variant="primary" disabled={busy}>Save quota</Button></div></form></div> : null}
  </div>;
}

function InstanceSettings({ settings, busy, onSubmit }: { settings: DatabasesInstanceSettings; busy: boolean; onSubmit: (event: FormEvent<HTMLFormElement>) => void }) {
  // key: dopo il salvataggio il form riparte dai valori restituiti dal server.
  const key = JSON.stringify(settings);
  return <section className="card" aria-labelledby="db-instance-settings"><h2 className="card-h" id="db-instance-settings">Instance settings</h2>
    <form key={key} className="card-b stack" aria-label="Database instance settings" onSubmit={onSubmit}>
      <div className="row small"><span style={{ width: 150 }}>Managed databases</span><span className="badge badge-ok">on</span><span className="hint">Switched with <code>gitstack config set databases on|off</code>; off never deletes data.</span></div>
      <div className="row small"><span style={{ width: 150 }}>Allowed PostgreSQL versions</span>{MAJORS.map((m) => <label className="row" key={m}><input type="checkbox" name={`major-${m}`} aria-label={`PostgreSQL ${m}`} defaultChecked={settings.allowedMajors.includes(m)} /> {m}</label>)}</div>
      <div className="row small"><span style={{ width: 150 }}>Max per database</span>
        <label>Disk (GB) <input aria-label="Max disk" name="maxSize" type="number" min="1" step="1" defaultValue={settings.maxPerDatabase.sizeBytes / GiB} /></label>
        <label>vCPU <input aria-label="Max CPU" name="maxCpu" type="number" min="0.1" step="0.1" defaultValue={settings.maxPerDatabase.cpuMillis / 1000} /></label>
        <label>Memory (GB) <input aria-label="Max memory" name="maxMemory" type="number" min="0.1" step="0.1" defaultValue={settings.maxPerDatabase.memoryBytes / GiB} /></label></div>
      <div className="row small"><span style={{ width: 150 }}>LAN port range</span>
        <label>From <input aria-label="LAN port from" name="lanFrom" type="number" min="1024" max="65535" defaultValue={settings.lanPortRange.from} /></label>
        <label>To <input aria-label="LAN port to" name="lanTo" type="number" min="1024" max="65535" defaultValue={settings.lanPortRange.to} /></label>
        <span className="hint">Open these TCP ports in the firewall for your network only.</span></div>
      <div className="row small"><span style={{ width: 150 }}>Backups kept</span>
        <label>Scheduled <input aria-label="Scheduled backups kept" name="keepScheduled" type="number" min="1" step="1" defaultValue={settings.backupRetention.scheduled} /></label>
        <label>On demand <input aria-label="On-demand backups kept" name="keepOnDemand" type="number" min="1" step="1" defaultValue={settings.backupRetention.onDemand} /></label></div>
      <div className="row"><Button type="submit" variant="primary" size="sm" disabled={busy}>Save settings</Button></div>
    </form></section>;
}

function DatabaseRow({ db }: { db: Database }) {
  const failed = db.operations?.backupError;
  return <div className="list-row"><span><b className="mono">{db.owner}/{db.name}</b>{failed ? <span className="badge badge-err" title={failed}>backup failed</span> : null}{db.operations?.minorUpdateAvailable ? <span className="badge">update available</span> : null}</span>
    <span>{db.pgMajor}</span>
    <span><span className={`badge${db.status === 'running' ? ' badge-ok' : db.status === 'failed' ? ' badge-err' : ''}`}>{db.status}</span></span>
    <span>{disk(db.sizeBytes)} · {vcpu(db.cpuMillis)} · {memory(db.memoryBytes)}</span>
    <span>{db.lanEnabled && db.lanPort ? `port ${db.lanPort}` : 'off'}</span>
    <span>{db.operations?.lastBackupAt ? new Date(db.operations.lastBackupAt).toLocaleString() : 'never'}</span></div>;
}

// Una sezione che non si è caricata mostra il suo errore; le altre restano visibili.
function SectionError({ id, title, message, className = 'card' }: { id: string; title: string; message?: string; className?: string }) {
  return <section className={className} aria-labelledby={id}><h2 className="card-h" id={id}>{title}</h2><div className="card-b"><ErrorAlert message={`${title}: ${message ?? 'could not be loaded.'}`} /></div></section>;
}
