import { useState } from 'react';
import type { FormEvent } from 'react';
import { Button, ErrorAlert, FormField, TextInput, useToast } from '../../../components';
import { clearAppEgress, fetchAppEgress, saveAppEgress } from '../../../lib/appsApi';
import type { App, AppEgressDestination } from '../../../lib/appsApi';
import { describeError } from '../../../lib/http';
import { useLoad } from '../../../lib/useLoad';

interface Props {
  app: App;
  canEdit: boolean;
}

/**
 * Outbound network (H16; mockup data-screen="volumes"): internet aperto, rete privata chiusa salvo le
 * destinazioni aperte dall'amministratore, e la restrizione per app: l'app raggiunge solo le destinazioni
 * elencate, mai più di quanto permette l'amministratore. Le destinazioni private le sceglie fra quelle aperte.
 */
export function AppOutboundSection({ app, canEdit }: Props) {
  const { data, loading, error, setData } = useLoad(() => fetchAppEgress(app.owner, app.repo, app.name));
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [host, setHost] = useState('');
  const [port, setPort] = useState('');
  const { toast } = useToast();

  async function run(work: () => Promise<void>) {
    setBusy(true);
    setActionError(null);
    try {
      await work();
    } catch (err) {
      setActionError(describeError(err));
    } finally {
      setBusy(false);
    }
  }
  const save = (destinations: AppEgressDestination[], done: string) =>
    run(async () => {
      setData(await saveAppEgress(app.owner, app.repo, app.name, destinations));
      toast(done, 'success');
    });
  const toggle = (on: boolean) =>
    on
      ? save([], 'Outbound restricted. Add the destinations this app may reach.')
      : run(async () => {
          await clearAppEgress(app.owner, app.repo, app.name);
          setData(await fetchAppEgress(app.owner, app.repo, app.name));
          toast('Outbound restriction removed.', 'success');
        });
  function add(event: FormEvent) {
    event.preventDefault();
    const n = Number(port);
    if (!host.trim() || !Number.isInteger(n) || n < 1 || n > 65535) {
      setActionError('Enter a host and a port from 1 to 65535.');
      return;
    }
    void save([...(data?.destinations ?? []), { host: host.trim(), port: n }], 'Destination added.').then(() => {
      setHost('');
      setPort('');
    });
  }

  if (loading && !data) return <p className="muted">Loading outbound network…</p>;
  if (error || !data) return error ? <ErrorAlert message={error} /> : null;
  const opened = data.allowedPrivate;
  return (
    <section aria-label="Outbound network">
      <div className="sec-h">
        <h2>Outbound network</h2>
        <span className="badge">admin only</span>
      </div>
      {actionError ? <ErrorAlert message={actionError} /> : null}
      <dl className="kv">
        <dt>Internet</dt>
        <dd>{data.restricted ? 'closed · this app is restricted to the destinations below' : 'open · through the company proxy, if one is configured'}</dd>
        <dt>Private network</dt>
        <dd>
          closed
          {opened.length > 0 ? (
            <>
              {' '}· the instance administrator opened{' '}
              {opened.map((d, i) => (
                <span key={`${d.host}:${d.port}`}>
                  {i > 0 ? ', ' : ''}
                  <span className="mono">{d.host}:{d.port}</span>
                </span>
              ))}{' '}
            </>
          ) : (
            ' · the administrator has not opened any destination '
          )}
          <span className="hint">· nothing else in 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16</span>
        </dd>
        <dt>Always blocked</dt>
        <dd className="muted">loopback, cluster network, link-local, Kubernetes ports</dd>
        <dt>Restrict this app</dt>
        <dd className="row">
          <input type="checkbox" role="switch" aria-label="Restrict this app" checked={data.restricted} disabled={!canEdit || busy} onChange={(e) => void toggle(e.target.checked)} />
          <span className="hint">
            {data.restricted ? 'On' : 'Off'} · when on, {app.name} can only reach the destinations you list, never more than the administrator allows
          </span>
        </dd>
      </dl>
      {data.instanceProxy ? (
        <p className="hint" role="note" aria-label="Restricted apps and the proxy">
          A restricted app connects directly to the listed destinations, without the instance proxy: if the installation network only reaches out through the proxy, a restricted app cannot reach the internet.
        </p>
      ) : null}
      {data.restricted ? (
        <div className="stack" style={{ gap: 8, marginTop: 8 }}>
          {data.destinations.length === 0 ? <p className="hint">No destination listed: the app reaches only the apps of this repository and DNS.</p> : null}
          <div className="list" role="list" aria-label="Allowed destinations">
            {data.destinations.map((d) => (
              <div className="list-row" role="listitem" key={`${d.host}:${d.port}`}>
                <span className="mono" style={{ flex: 1 }}>{d.host}:{d.port}</span>
                {canEdit ? (
                  <Button size="sm" variant="ghost" aria-label={`Remove ${d.host}:${d.port}`} disabled={busy} onClick={() => void save(data.destinations.filter((x) => x.host !== d.host || x.port !== d.port), 'Destination removed.')}>
                    Remove
                  </Button>
                ) : null}
              </div>
            ))}
          </div>
          {canEdit ? (
            <form className="row" onSubmit={add} aria-label="Add destination">
              <FormField label="Host">
                <TextInput value={host} onChange={(e) => setHost(e.target.value)} placeholder="api.example.com" autoComplete="off" list="outbound-opened" />
              </FormField>
              <FormField label="Port">
                <TextInput value={port} onChange={(e) => setPort(e.target.value)} placeholder="443" inputMode="numeric" autoComplete="off" />
              </FormField>
              <datalist id="outbound-opened">
                {opened.map((d) => (
                  <option key={`${d.host}:${d.port}`} value={d.host} />
                ))}
              </datalist>
              <Button type="submit" size="sm" disabled={busy}>
                Add destination
              </Button>
            </form>
          ) : null}
          <p className="hint">Internet addresses are always allowed to list. A private address must be one the administrator opened. Names are resolved when the policy is applied.</p>
        </div>
      ) : null}
      {!canEdit ? <p className="hint">Only admins of the repository can change this.</p> : null}
    </section>
  );
}
