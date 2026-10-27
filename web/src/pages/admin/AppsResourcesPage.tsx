import { useState } from 'react';
import type { FormEvent } from 'react';
import { Button, ErrorAlert, FormField, TextInput, useToast } from '../../components';
import { fetchSession } from '../../lib/authApi';
import { addTrustedKey, fetchAdminAppsData, fetchOwnerQuotas, removeTrustedKey, resetOwnerQuota, saveAppsEgress, saveAppsSettings, saveOwnerQuota, updateAppsReserve } from '../../lib/adminAppsApi';
import type { AppOwnerQuota, AppOwnerQuotaInput, AppsInstanceSettingsInput } from '../../lib/adminAppsApi';
import { describeError } from '../../lib/http';
import { useLoad } from '../../lib/useLoad';

const GiB = 1024 ** 3;
const MiB = 1024 ** 2;
const vcpu = (n: number) => `${(n / 1000).toLocaleString(undefined, { maximumFractionDigits: 2 })} vCPU`;
const memory = (n: number) => n >= GiB ? `${(n / GiB).toLocaleString(undefined, { maximumFractionDigits: 2 })} GB` : `${Math.round(n / MiB)} MB`;
const disk = (n: number) => `${Math.round(n / GiB)} GB`;

export function AppsResourcesPage() {
  const { data: session, loading, error } = useLoad(fetchSession);
  if (loading && !session) return <p className="muted">Loading…</p>;
  if (error) return <ErrorAlert message={error} />;
  if (!session?.user.isAdmin) return <><div className="page-h"><h1>Apps &amp; resources</h1></div><ErrorAlert message="Only installation administrators can manage app resources." /></>;
  return <AppsResourcesConsole />;
}

function AppsResourcesConsole() {
  const { data, setData, loading, error } = useLoad(fetchAdminAppsData);
  const { toast } = useToast();
  const [busy, setBusy] = useState(false);
  const [quotaOwner, setQuotaOwner] = useState<AppOwnerQuota | null>(null);
  const [keyForm, setKeyForm] = useState(false);
  const [formError, setFormError] = useState('');
  const [keyName, setKeyName] = useState('');
  const [publicKey, setPublicKey] = useState('');
  const [destForm, setDestForm] = useState(false);
  const [destHost, setDestHost] = useState('');
  const [destPort, setDestPort] = useState('');
  const [destNote, setDestNote] = useState('');
  if (loading && !data) return <p className="muted">Loading…</p>;
  if (error) return <><div className="page-h"><h1>Apps &amp; resources</h1></div><ErrorAlert message={error} /></>;
  if (!data) return null;

  async function saveSettings(patch: Partial<AppsInstanceSettingsInput>) {
    if (!data) return;
    setBusy(true); setFormError('');
    try {
      const settings = await saveAppsSettings({ publicAppsEnabled: false, signedImagesOnly: data.settings.signedImagesOnly, restricted: data.settings.restricted, maxPerApp: data.settings.maxPerApp, logRetention: data.settings.logRetention, ...patch });
      setData((previous) => previous ? { ...previous, settings } : previous);
      toast('Instance settings saved.', 'success');
    } catch (e) { setFormError(describeError(e)); }
    finally { setBusy(false); }
  }
  async function saveQuota(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!quotaOwner) return;
    const form = new FormData(event.currentTarget);
    const limits: AppOwnerQuotaInput = { cpuMillis: Number(form.get('cpu')) * 1000, memoryBytes: Number(form.get('memory')) * GiB, diskBytes: Number(form.get('disk')) * GiB };
    setBusy(true); setFormError('');
    try {
      const updated = await saveOwnerQuota(quotaOwner.owner, limits);
      setData((previous) => previous ? { ...previous, quotas: previous.quotas.map((q) => q.owner === updated.owner ? updated : q) } : previous);
      setQuotaOwner(null); toast(`Quota for ${updated.owner} saved.`, 'success');
    } catch (e) { setFormError(describeError(e)); }
    finally { setBusy(false); }
  }
  async function addKey(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); setFormError('');
    try {
      const key = await addTrustedKey(keyName.trim(), publicKey.trim());
      setData((previous) => previous ? { ...previous, keys: [...previous.keys, key] } : previous); setKeyName(''); setPublicKey(''); setKeyForm(false); toast('Trusted key added.', 'success');
    } catch (e) { setFormError(describeError(e)); }
    finally { setBusy(false); }
  }
  // Destinazioni private aperte (H16): l'API sostituisce l'elenco intero, quindi si rimanda quello nuovo.
  async function saveDestinations(next: { host: string; port: number; note?: string }[], done: string) {
    setBusy(true); setFormError('');
    try {
      const egress = await saveAppsEgress(next);
      setData((previous) => previous ? { ...previous, egress } : previous); toast(done, 'success'); return true;
    } catch (e) { setFormError(describeError(e)); return false; }
    finally { setBusy(false); }
  }
  async function addDestination(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const port = Number(destPort);
    if (!Number.isInteger(port) || port < 1 || port > 65535) { setFormError('Enter a port from 1 to 65535.'); return; }
    const current = (data?.egress?.privateDestinations ?? []).map((d) => ({ host: d.host, port: d.port, note: d.note }));
    if (await saveDestinations([...current, { host: destHost.trim(), port, note: destNote.trim() }], 'Private destination opened.')) { setDestHost(''); setDestPort(''); setDestNote(''); setDestForm(false); }
  }
  async function removeDestination(host: string, port: number) {
    const rest = (data?.egress?.privateDestinations ?? []).filter((d) => d.host !== host || d.port !== port).map((d) => ({ host: d.host, port: d.port, note: d.note }));
    await saveDestinations(rest, 'Private destination closed.');
  }
  async function changeReserve(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form = new FormData(event.currentTarget);
    setBusy(true); setFormError('');
    try {
      const capacity = await updateAppsReserve({ cpuMillis: Number(form.get('reserveCpu')) * 1000, memoryBytes: Number(form.get('reserveMemory')) * GiB });
      setData((previous) => previous ? { ...previous, capacity } : previous); toast('Apps share saved.', 'success');
    } catch (e) { setFormError(describeError(e)); }
    finally { setBusy(false); }
  }
  async function resetQuota(owner: string) {
    setBusy(true); setFormError('');
    try {
      await resetOwnerQuota(owner);
      const quotas = await fetchOwnerQuotas();
      setData((previous) => previous ? { ...previous, quotas } : previous);
    } catch (e) { setFormError(describeError(e)); }
    finally { setBusy(false); }
  }

  const { capacity, quotas, settings, keys, apps } = data;
  const privateDestinations = data.egress?.privateDestinations ?? [];
  const cpuPercent = (n: number, total: number) => Math.min(100, total ? n / total * 100 : 0);
  return <div>
    <div className="row page-h"><h1>Apps &amp; resources</h1><span className="sp" /><span className="small muted">Server: {vcpu(capacity.server.cpuMillis)} · {memory(capacity.server.memoryBytes)}{capacity.server.profile ? ` · ${capacity.server.profile} profile` : ''}</span></div>
    {formError ? <ErrorAlert message={formError} /> : null}
    <section className="card section-gap" aria-labelledby="admin-app-capacity"><h2 className="card-h" id="admin-app-capacity">Server capacity · priority GitStack services › apps › CI jobs</h2><div className="card-b stack" style={{ gap: 12 }}>
      <CapacityRow label="GitStack services" amount={capacity.services.reserved} total={capacity.server} />
      <CapacityRow label="Apps" amount={capacity.apps.guaranteed} total={capacity.apps.share} warning={capacity.appsShareWarning} />
      <div className="row small"><span style={{ width: 150 }}>CI jobs</span><div className="meter" style={{ flex: 1, margin: 0 }}><i style={{ width: `${cpuPercent(capacity.ci.usedCpuMillis ?? 0, capacity.ci.guaranteedCpuMillis)}%` }} /></div><span>{vcpu(capacity.ci.usedCpuMillis ?? 0)} / {vcpu(capacity.ci.guaranteedCpuMillis)} · {capacity.ci.slots} slots</span></div>
      <form className="row small" onSubmit={changeReserve}><span>Apps share</span><label>vCPU <input aria-label="Apps share CPU" name="reserveCpu" type="number" min="0.1" step="0.1" defaultValue={capacity.apps.share.cpuMillis / 1000} /></label><label>GB <input aria-label="Apps share memory" name="reserveMemory" type="number" min="0.1" step="0.1" defaultValue={capacity.apps.share.memoryBytes / GiB} /></label><Button type="submit" size="sm" disabled={busy}>Save</Button></form>
      {capacity.appsShareWarning ? <p className="hint" role="status">Apps share above 80%: new deploys may be refused.</p> : null}
    </div></section>
    <div className="admin-apps-grid">
      <section className="card" aria-labelledby="owner-quotas"><h2 className="card-h" id="owner-quotas">Quotas by owner</h2><div className="list">{quotas.map((q) => <div className="list-row" key={q.owner}><div style={{ flex: 1 }}><b>{q.owner}</b><div className={`meter${q.warning ? ' warn' : ''}`}><i style={{ width: `${quotaPercent(q)}%` }} /></div><div className="small muted">{vcpu(q.used.cpuMillis)} / {vcpu(q.limits.cpuMillis)} · {memory(q.used.memoryBytes)} / {memory(q.limits.memoryBytes)} · disk {disk(q.used.diskBytes)} / {disk(q.limits.diskBytes)}</div></div><Button size="sm" onClick={() => setQuotaOwner(q)}>Edit</Button>{!q.isDefault ? <Button size="sm" variant="ghost" disabled={busy} onClick={() => void resetQuota(q.owner)}>Reset</Button> : null}</div>)}</div></section>
      <section className="card" aria-labelledby="instance-settings"><h2 className="card-h" id="instance-settings">Instance settings</h2><dl className="kv admin-apps-kv">
        <dt>Apps domain</dt><dd><span className="mono">{settings.appsDomain ?? 'Not configured'}</span>{settings.appsDomainIsDefault ? <span className="hint"> · default</span> : null}<span className="hint"> · separate from the GitStack domain</span></dd>
        <dt>Public apps</dt><dd><label className="row"><input type="checkbox" role="switch" aria-label="Public apps" checked={false} disabled /> <span className="hint">Off · repository admins can&apos;t publish apps (v3-c)</span></label></dd>
        <dt>Only signed images</dt><dd><SettingToggle label="Only signed images" checked={settings.signedImagesOnly} disabled={busy} onChange={(v) => void saveSettings({ signedImagesOnly: v })} hint={settings.signedImagesOnly ? 'Unsigned images are refused.' : 'Unsigned manual deploys show not verified.'} /></dd>
        <dt>Trusted keys</dt><dd>{keys.map((key) => <div className="row" key={key.id}><span>{key.name} <span className="mono small">{key.fingerprint}</span>{key.instanceKey ? <span className="badge">instance</span> : null}</span>{!key.instanceKey ? <Button size="sm" variant="ghost" disabled={busy} onClick={async () => { try { await removeTrustedKey(key.id); setData((previous) => previous ? { ...previous, keys: previous.keys.filter((k) => k.id !== key.id) } : previous); } catch (e) { setFormError(describeError(e)); } }}>Remove</Button> : null}</div>)}<Button size="sm" onClick={() => setKeyForm(!keyForm)}>Add key</Button></dd>
        <dt>Restricted mode</dt><dd><SettingToggle label="Restricted mode" checked={settings.restricted} disabled={busy} onChange={(v) => void saveSettings({ restricted: v })} hint={settings.restricted ? 'Apps run with restricted permissions.' : 'Off · images may run as root (baseline).'} /></dd>
        <dt>App outbound</dt><dd>Internet open (through the proxy, if one is configured) · private network closed</dd>
        <dt>Private destinations</dt><dd>{privateDestinations.length === 0 ? <span className="hint">None opened · apps reach nothing in 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16</span> : privateDestinations.map((d) => <div className="row" key={`${d.host}:${d.port}`}><span className="mono">{d.host}:{d.port}</span>{d.note ? <span className="hint"> · {d.note}</span> : null}<Button size="sm" variant="ghost" aria-label={`Close ${d.host}:${d.port}`} disabled={busy} onClick={() => void removeDestination(d.host, d.port)}>Remove</Button></div>)}<Button size="sm" onClick={() => setDestForm(!destForm)}>+ Add destination</Button><div className="hint">Same allow-list rules as webhooks and mirrors. Always blocked, even if listed: loopback, the cluster network, link-local addresses and Kubernetes ports.</div></dd>
        <dt>Max per app</dt><dd>{vcpu(settings.maxPerApp.cpuMillis)} · {memory(settings.maxPerApp.memoryBytes)} · logs {settings.logRetention.days} days / {memory(settings.logRetention.maxBytes)}</dd>
      </dl></section>
      <section className="card admin-app-list" aria-labelledby="admin-app-list-title"><h2 className="card-h" id="admin-app-list-title">Apps</h2><div className="list"><div className="list-row list-head"><span>App</span><span>Guaranteed</span><span>Using</span><span>Limit</span><span>Copies</span></div>{apps.map((app) => <div className="list-row" key={`${app.owner}/${app.repo}/${app.name}`}><span><b className="mono">{app.owner}/{app.repo}/{app.name}</b>{!app.verified ? <span className="badge badge-run">not verified</span> : null}{app.runsAsRoot ? <span className="badge">root</span> : null}{app.violatesInstanceRules ? <span className="badge badge-err">instance policy</span> : null}</span><span>{vcpu(app.guaranteed.cpuMillis)} · {memory(app.guaranteed.memoryBytes)}</span><span>{vcpu(app.usage.cpuMillis)} · {memory(app.usage.memoryBytes)}{app.aboveGuaranteed ? <span className="badge badge-run">above</span> : null}</span><span>{vcpu(app.limit.cpuMillis)} · {memory(app.limit.memoryBytes)}</span><span>{app.copies}</span></div>)}</div></section>
    </div>
    {quotaOwner ? <div className="modal-back" role="presentation"><form className="modal" aria-label={`Edit quota for ${quotaOwner.owner}`} onSubmit={saveQuota}><h2 className="modal-h">Edit quota · {quotaOwner.owner}</h2><div className="modal-b stack"><FormField label="Guaranteed CPU (vCPU)"><TextInput name="cpu" type="number" min="0.1" step="0.1" defaultValue={quotaOwner.limits.cpuMillis / 1000} /></FormField><FormField label="Memory (GB)"><TextInput name="memory" type="number" min="0.1" step="0.1" defaultValue={quotaOwner.limits.memoryBytes / GiB} /></FormField><FormField label="Disk (GB)"><TextInput name="disk" type="number" min="0" step="1" defaultValue={quotaOwner.limits.diskBytes / GiB} /></FormField></div><div className="modal-f"><Button onClick={() => setQuotaOwner(null)}>Cancel</Button><Button type="submit" variant="primary" disabled={busy}>Save quota</Button></div></form></div> : null}
    {destForm ? <div className="modal-back" role="presentation"><form className="modal" aria-label="Add private destination" onSubmit={addDestination}><h2 className="modal-h">Add private destination</h2><div className="modal-b stack"><FormField label="Host" hint="An IP address or a DNS name, such as db.azienda.lan."><TextInput value={destHost} onChange={(e) => setDestHost(e.target.value)} placeholder="db.azienda.lan" required /></FormField><FormField label="Port"><TextInput value={destPort} onChange={(e) => setDestPort(e.target.value)} placeholder="5432" inputMode="numeric" required /></FormField><FormField label="Note" hint="What it is for (optional)."><TextInput value={destNote} onChange={(e) => setDestNote(e.target.value)} maxLength={200} /></FormField></div><div className="modal-f"><Button onClick={() => setDestForm(false)}>Cancel</Button><Button type="submit" variant="primary" disabled={busy}>Open destination</Button></div></form></div> : null}
    {keyForm ? <div className="modal-back" role="presentation"><form className="modal" aria-label="Add trusted key" onSubmit={addKey}><h2 className="modal-h">Add trusted key</h2><div className="modal-b stack"><FormField label="Name"><TextInput value={keyName} onChange={(e) => setKeyName(e.target.value)} required /></FormField><FormField label="Public key (PEM)"><textarea className="input mono" rows={6} value={publicKey} onChange={(e) => setPublicKey(e.target.value)} required /></FormField></div><div className="modal-f"><Button onClick={() => setKeyForm(false)}>Cancel</Button><Button type="submit" variant="primary" disabled={busy}>Add key</Button></div></form></div> : null}
  </div>;
}

function CapacityRow({ label, amount, total, warning = false }: { label: string; amount: { cpuMillis: number; memoryBytes: number }; total: { cpuMillis: number; memoryBytes: number }; warning?: boolean }) {
  return <div className="row small"><span style={{ width: 150 }}>{label}</span><div className={`meter${warning ? ' warn' : ''}`} style={{ flex: 1, margin: 0 }}><i style={{ width: `${Math.min(100, total.cpuMillis ? amount.cpuMillis / total.cpuMillis * 100 : 0)}%` }} /></div><span>{vcpu(amount.cpuMillis)} · {memory(amount.memoryBytes)} reserved</span></div>;
}
function SettingToggle({ label, checked, disabled, onChange, hint }: { label: string; checked: boolean; disabled: boolean; onChange: (value: boolean) => void; hint: string }) {
  return <label className="row"><input type="checkbox" role="switch" aria-label={label} checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} /><span className="hint">{checked ? 'On' : 'Off'} · {hint}</span></label>;
}
function quotaPercent(q: AppOwnerQuota) { return Math.min(100, q.limits.cpuMillis ? q.used.cpuMillis / q.limits.cpuMillis * 100 : 0); }
