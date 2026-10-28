import * as Dialog from '@radix-ui/react-dialog';
import { Check, Globe, Link as LinkIcon, Plus, ShieldCheck } from 'lucide-react';
import { useState } from 'react';
import type { FormEvent } from 'react';
import { Button, ConfirmDialog, CopyButton, ErrorAlert } from '../../../components';
import { addAppDomain, fetchAppDomains, recheckAppDomain, removeAppDomain, removeAppDomainCertificate, uploadAppDomainCertificate } from '../../../lib/appsApi';
import type { App, AppAccess, AppDomain } from '../../../lib/appsApi';
import { ApiError, describeError } from '../../../lib/http';
import type { Repository } from '../../../lib/reposApi';
import { useLoad } from '../../../lib/useLoad';

interface Props {
  repo: Repository;
  app: App;
  access: AppAccess;
  /** admin dell'app: aggiunge, ricontrolla, carica certificati, rimuove. */
  canEdit: boolean;
}

function day(iso: string | null | undefined): string {
  if (!iso) return '';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' });
}

/** Testo del record DNS da creare, copiabile: «CNAME docs.acme.com → apps.acme.it». */
function dnsRecordText(d: AppDomain): string {
  return `${d.dns.type} ${d.dns.name} → ${d.dns.value}`;
}

/** Badge del certificato di un dominio proprio (mockup H7, variante pubblica). */
function CertBadge({ domain }: { domain: AppDomain }) {
  const c = domain.certificate;
  if (c.state === 'none') return null;
  if (c.state === 'issuing') return <span className="badge badge-run">Issuing certificate…</span>;
  if (c.state === 'error') return <span className="badge badge-err">Certificate error</span>;
  const until = day(c.validUntil);
  const label = c.mode === 'custom' ? 'Uploaded certificate' : "Let's Encrypt";
  return (
    <span className="badge badge-ok">
      <ShieldCheck size={12} aria-hidden="true" />
      {label}
      {until ? ` · until ${until}` : ''}
    </span>
  );
}

function StatusBadge({ domain }: { domain: AppDomain }) {
  switch (domain.status) {
    case 'verified':
      return (
        <span className="badge badge-ok">
          <Check size={12} aria-hidden="true" />
          DNS points here
        </span>
      );
    case 'error':
      return <span className="badge badge-err">DNS error</span>;
    default:
      return <span className="badge badge-run">Waiting for DNS</span>;
  }
}

/**
 * Domini dell'app (H7, mockup data-screen="access", variante «pub»): l'indirizzo automatico
 * e i domini propri con record DNS, stato, «Check now», certificato e rimozione.
 * Solo admin modifica. L'elenco dei domini lo legge solo l'admin dell'app (403 per gli altri).
 */
export function AppDomainsSection({ repo, app, access, canEdit }: Props) {
  const owner = repo.owner.name;
  const name = repo.name;
  const isPublic = access.mode === 'public';
  const { data, loading, error, setData } = useLoad(() => fetchAppDomains(owner, name, app.name), canEdit ? undefined : null);
  const forbidden = !canEdit || (error !== null && /permission/i.test(error));
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [adding, setAdding] = useState(false);
  const [newDomain, setNewDomain] = useState('');
  const [removing, setRemoving] = useState<AppDomain | null>(null);
  const [certFor, setCertFor] = useState<AppDomain | null>(null);
  const [cert, setCert] = useState('');
  const [key, setKey] = useState('');

  const items = data?.items ?? [];

  function replace(next: AppDomain) {
    setData({ items: items.map((d) => (d.id === next.id ? next : d)) });
  }

  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setActionError(null);
    try {
      await fn();
    } catch (err) {
      setActionError(describeDomainError(err));
    } finally {
      setBusy(false);
    }
  }

  const add = (e: FormEvent) => {
    e.preventDefault();
    const value = newDomain.trim().toLowerCase();
    if (!value) return;
    void run(async () => {
      const created = await addAppDomain(owner, name, app.name, value);
      setData({ items: [...items, created] });
      setNewDomain('');
      setAdding(false);
    });
  };

  const recheck = (d: AppDomain) => run(async () => replace(await recheckAppDomain(owner, name, app.name, d.domain)));

  const remove = (d: AppDomain) =>
    run(async () => {
      await removeAppDomain(owner, name, app.name, d.domain);
      setData({ items: items.filter((x) => x.id !== d.id) });
    });

  const upload = (e: FormEvent) => {
    e.preventDefault();
    const d = certFor;
    if (!d) return;
    void run(async () => {
      replace(await uploadAppDomainCertificate(owner, name, app.name, d.domain, cert.trim(), key.trim()));
      setCertFor(null);
      setCert('');
      setKey('');
    });
  };

  const useAutomatic = () => {
    const d = certFor;
    if (!d) return;
    void run(async () => {
      replace(await removeAppDomainCertificate(owner, name, app.name, d.domain));
      setCertFor(null);
    });
  };

  return (
    <div aria-label="Domains" role="region">
      <div className="sec-h">
        <h2>Domains</h2>
        {canEdit ? <span className="badge">admin only</span> : null}
        <span className="sp"></span>
        {canEdit && isPublic ? (
          <Button onClick={() => setAdding((v) => !v)}>
            <Plus size={16} aria-hidden="true" />
            Add domain
          </Button>
        ) : null}
      </div>
      {actionError ? <ErrorAlert message={actionError} /> : null}
      {error && !forbidden ? <ErrorAlert message={error} /> : null}
      {canEdit && isPublic && adding ? (
        <form className="row" style={{ gap: 8, marginBottom: 10 }} onSubmit={add}>
          <input
            className="input"
            aria-label="Domain name"
            placeholder="docs.acme.com"
            autoComplete="off"
            value={newDomain}
            onChange={(e) => setNewDomain(e.target.value)}
            style={{ flex: 1 }}
          />
          <Button type="submit" variant="primary" disabled={busy || newDomain.trim() === ''}>
            Add
          </Button>
        </form>
      ) : null}
      <div className="list">
        <div className="list-row">
          <LinkIcon size={16} className="muted" aria-hidden="true" />
          <span className="mono" style={{ flex: 1 }}>{access.address ?? app.address ?? 'No address'}</span>
          <span className="badge">automatic</span>
          {access.certificate === 'instance_ca' ? (
            <span className="badge badge-ok">
              <ShieldCheck size={12} aria-hidden="true" />
              GitStack CA
            </span>
          ) : access.certificate === 'lets_encrypt' ? (
            <span className="badge badge-ok">
              <ShieldCheck size={12} aria-hidden="true" />
              Let&apos;s Encrypt
            </span>
          ) : null}
        </div>
        {items.map((d) => (
          <div className="list-row" key={d.id} data-testid={`domain-${d.domain}`}>
            <Globe size={16} className="muted" aria-hidden="true" />
            <div style={{ flex: 1 }}>
              <span className="mono">{d.domain}</span>
              {d.status !== 'verified' ? (
                <div className="small muted">
                  Create <span className="mono">{dnsRecordText(d)}</span>.
                  {d.dns.alternatives.map((a) => (
                    <span key={`${a.type}-${a.value}`}> Or <span className="mono">{a.type} {d.dns.name} → {a.value}</span>.</span>
                  ))}{' '}
                  Checked every 5 min.
                </div>
              ) : null}
              {d.status === 'error' && d.statusReason ? <div className="small" style={{ color: 'var(--danger)' }}>{d.statusReason}</div> : null}
              {d.certificate.state === 'error' && d.certificate.error ? (
                <div className="small" style={{ color: 'var(--danger)' }}>{d.certificate.error}</div>
              ) : null}
            </div>
            <StatusBadge domain={d} />
            <CertBadge domain={d} />
            {d.status !== 'verified' ? <CopyButton value={dnsRecordText(d)} label="Copy record" /> : null}
            {canEdit ? (
              <>
                {d.status !== 'verified' ? (
                  <Button size="sm" disabled={busy} aria-label={`Check ${d.domain} now`} onClick={() => void recheck(d)}>
                    Check now
                  </Button>
                ) : null}
                <Button size="sm" variant="ghost" disabled={busy} aria-label={`Certificate of ${d.domain}`} onClick={() => { setCertFor(d); setActionError(null); }}>
                  Certificate
                </Button>
                <Button size="sm" variant="ghost" disabled={busy} aria-label={`Remove ${d.domain}`} onClick={() => setRemoving(d)}>
                  Remove
                </Button>
              </>
            ) : null}
          </div>
        ))}
      </div>
      {loading && data === null ? <p className="muted">Loading domains…</p> : null}
      {forbidden ? <p className="hint" style={{ marginTop: 8 }}>Custom domains are visible to admins of the repository.</p> : null}
      <p className="hint" style={{ marginTop: 8 }}>
        {isPublic
          ? 'Automatic certificates need port 80 of this server reachable from the internet. You can upload your own certificate instead.'
          : 'Custom domains are available for public apps, or with a certificate you upload.'}
      </p>
      {canEdit ? (
        <>
          <ConfirmDialog
            open={removing !== null}
            onOpenChange={(open) => { if (!open) setRemoving(null); }}
            title={`Remove ${removing?.domain ?? 'domain'}?`}
            description="The app stops answering on this domain and its certificate is deleted. The DNS record is yours to remove."
            confirmLabel="Remove domain"
            destructive
            onConfirm={() => { if (removing) void remove(removing); }}
          />
          <Dialog.Root open={certFor !== null} onOpenChange={(open) => { if (!open) setCertFor(null); }}>
            <Dialog.Portal>
              <Dialog.Overlay className="dialog-overlay" />
              <Dialog.Content className="dialog dialog-wide">
                <Dialog.Title className="dialog-title">Certificate for {certFor?.domain}</Dialog.Title>
                <Dialog.Description className="dialog-desc">
                  {certFor?.certificate.mode === 'custom'
                    ? 'This domain uses a certificate you uploaded. Upload a new one, or go back to the automatic Let’s Encrypt certificate.'
                    : 'This domain uses an automatic Let’s Encrypt certificate. Upload your own certificate to replace it.'}
                </Dialog.Description>
                {actionError ? <ErrorAlert message={actionError} /> : null}
                <form className="stack" style={{ gap: 8 }} onSubmit={upload}>
                  <label className="small muted" htmlFor="cert-pem">Certificate and intermediate chain (PEM, leaf first)</label>
                  <textarea id="cert-pem" className="input mono" rows={5} value={cert} onChange={(e) => setCert(e.target.value)} placeholder="-----BEGIN CERTIFICATE-----" />
                  <label className="small muted" htmlFor="cert-key">Private key (PEM, no passphrase)</label>
                  <textarea id="cert-key" className="input mono" rows={4} value={key} onChange={(e) => setKey(e.target.value)} placeholder="-----BEGIN PRIVATE KEY-----" autoComplete="off" />
                  <div className="dialog-actions">
                    <Dialog.Close asChild>
                      <Button type="button">Close</Button>
                    </Dialog.Close>
                    {certFor?.certificate.mode === 'custom' ? (
                      <Button type="button" disabled={busy} onClick={useAutomatic}>
                        Use Let&apos;s Encrypt
                      </Button>
                    ) : null}
                    <Button type="submit" variant="primary" disabled={busy || cert.trim() === '' || key.trim() === ''}>
                      Upload certificate
                    </Button>
                  </div>
                </form>
              </Dialog.Content>
            </Dialog.Portal>
          </Dialog.Root>
        </>
      ) : null}
    </div>
  );
}

/** Errori del contratto dei domini con un testo che dice cosa fare. */
function describeDomainError(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case 'domain_taken':
        return 'This domain is already used by an app on this instance. Remove it there first.';
      case 'archived':
        return 'The repository is archived: domains cannot be changed.';
      case 'app_not_public':
        return 'Custom domains need the app to be public. Choose «Public on the internet» first.';
      case 'not_custom':
        return "This domain already uses Let's Encrypt.";
    }
    if (err.status === 404) return 'This domain no longer exists. Reload the page.';
  }
  return describeError(err);
}
