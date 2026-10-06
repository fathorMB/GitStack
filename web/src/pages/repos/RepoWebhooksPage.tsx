// Impostazioni del repo, sezione Webhooks (mockup 11): creazione, modifica,
// elenco con stato, consegne e Redeliver. Solo per admin del repo.
import * as Dialog from '@radix-ui/react-dialog';
import { Check, Link as LinkIcon, Plus, Trash2, X } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { useParams } from 'react-router-dom';
import { Button, ConfirmDialog, CopyButton, EmptyState, ErrorAlert, FormField, TextInput, useToast } from '../../components';
import { describeError } from '../../lib/http';
import { timeAgo } from '../../lib/format';
import { fetchRepo } from '../../lib/reposApi';
import type { Webhook, WebhookDeliverySummary } from '../../lib/webhooksApi';
import { isRepoAdmin, loadMe } from './repoAdmin';
import { VisibilityBadge } from './ReposPage';

const EVENT_DESCRIPTIONS: Record<string, string> = {
  push: 'Commits or tags pushed',
  issues: 'Opened, edited, closed, labeled',
  issue_comment: 'Comments created or edited',
  repository: 'Created, archived, deleted, restored',
};

const WEBHOOK_INFO =
  'GitStack sends a signed POST request to these URLs when selected events happen. Failed deliveries are retried up to 8 times over 24 hours; a webhook failing for 3 days is disabled automatically. Payload format: GitStack v1, with X-GitStack-Event, X-GitStack-Delivery and X-GitStack-Signature headers. Addresses inside the GitStack cluster are never allowed.';

export function RepoWebhooksPage() {
  const { owner = '', repo = '' } = useParams();
  return <RepoWebhooksView key={`${owner}/${repo}`} owner={owner} repo={repo} />;
}

function RepoWebhooksView({ owner, repo }: { owner: string; repo: string }) {
  const [loading, setLoading] = useState(true);
  const [repoData, setRepoData] = useState<Awaited<ReturnType<typeof fetchRepo>> | null>(null);
  const [admin, setAdmin] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const [r, me] = await Promise.all([fetchRepo(owner, repo), loadMe()]);
      const isAdmin = await isRepoAdmin(r, me);
      setRepoData(r);
      setAdmin(isAdmin);
    } catch (_err) {
      setError(describeError(_err));
    } finally {
      setLoading(false);
    }
  }, [owner, repo]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loading) return <p className="muted">Loading settings…</p>;

  return (
    <div>
      <div className="repo-title row">
        <h1>
          {repoData?.owner.name}/{repoData?.name}
        </h1>
        {repoData ? <VisibilityBadge visibility={repoData.visibility} /> : null}
      </div>
      {error ? <ErrorAlert message={error} /> : null}
      <div className="stack" style={{ gap: 24, marginTop: 16 }}>
        {admin && repoData ? (
          <WebhooksContent key={`${owner}/${repo}`} owner={owner} repo={repo} repoData={repoData} onReload={load} />
        ) : (
          <p className="muted">Webhooks are available only to repository admins.</p>
        )}
      </div>
    </div>
  );
}

/* ===== Componente principale (quando admin) ===== */

function WebhooksContent({
  owner,
  repo,
  onReload,
}: {
  owner: string;
  repo: string;
  repoData?: unknown;
  onReload: () => void;
}) {
  const { toast } = useToast();
  const [webhooks, setWebhooks] = useState<Webhook[]>([]);
  const [loading, setLoading] = useState(true);

  const [editing, setEditing] = useState<Webhook | null>(null);
  const [saving, setSaving] = useState(false);
  const [formErrors, setFormErrors] = useState<Record<string, string>>({});

  const [createdSecret, setCreatedSecret] = useState<string | null>(null);

  const [deliveriesLoading, setDeliveriesLoading] = useState<Record<string, boolean>>({});
  const [deliveriesByWebhook, setDeliveriesByWebhook] = useState<Record<string, WebhookDeliverySummary[]>>({});

  const [toDelete, setToDelete] = useState<Webhook | null>(null);
  const [redelivering, setRedelivering] = useState<string | null>(null);

  const loadWebhooks = useCallback(async () => {
    try {
      const api = await import('../../lib/webhooksApi');
      const data = await api.fetchRepoWebhooks(owner, repo);
      setWebhooks(data.items);
    } catch {
      setWebhooks([]);
    } finally {
      setLoading(false);
    }
  }, [owner, repo]);

  useEffect(() => {
    void loadWebhooks();
  }, [loadWebhooks]);

  const loadDeliveries = useCallback(
    async (wh: Webhook) => {
      setDeliveriesLoading((prev) => ({ ...prev, [wh.id]: true }));
      try {
        const api = await import('../../lib/webhooksApi');
        const data = await api.fetchRepoWebhookDeliveries(owner, repo, wh.id);
        setDeliveriesByWebhook((prev) => ({ ...prev, [wh.id]: data.items }));
      } catch {
        setDeliveriesByWebhook((prev) => ({ ...prev, [wh.id]: [] }));
      } finally {
        setDeliveriesLoading((prev) => ({ ...prev, [wh.id]: false }));
      }
    },
    [owner, repo],
  );

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const formData = new FormData(form);
    const url = formData.get('url')?.toString() ?? '';
    const checkedEvents: string[] = [];
    for (const ev of ['push', 'issues', 'issue_comment', 'repository'] as const) {
      if (formData.get(ev) === 'on') checkedEvents.push(ev);
    }

    setFormErrors({});
    setSaving(true);
    try {
      const api = await import('../../lib/webhooksApi');
      await api.createRepoWebhook(owner, repo, { url, events: checkedEvents as Webhook['events'] });
      setCreatedSecret('placeholder_secret');
      setEditing(null);
      toast('Webhook created.', 'success');
      await loadWebhooks();
      onReload();
    } catch (err) {
      setFormErrors({ _form: describeError(err) });
    } finally {
      setSaving(false);
    }
  }

  async function handleUpdate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!editing) return;
    const form = event.currentTarget;
    const formData = new FormData(form);
    const url = formData.get('url')?.toString() ?? '';
    const checkedEvents: string[] = [];
    for (const ev of ['push', 'issues', 'issue_comment', 'repository'] as const) {
      if (formData.get(ev) === 'on') checkedEvents.push(ev);
    }
    const active = formData.get('active') === 'on';

    setSaving(true);
    try {
      const api = await import('../../lib/webhooksApi');
      const updated = await api.updateRepoWebhook(owner, repo, editing.id, { url, events: checkedEvents as Webhook['events'], active });
      setWebhooks((prev) => prev.map((w) => (w.id === updated.id ? updated : w)));
      setEditing(null);
      toast('Webhook saved.', 'success');
      onReload();
    } catch (err) {
      setFormErrors({ _form: describeError(err) });
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!toDelete) return;
    setSaving(true);
    try {
      const api = await import('../../lib/webhooksApi');
      await api.deleteRepoWebhook(owner, repo, toDelete.id);
      setWebhooks((prev) => prev.filter((w) => w.id !== toDelete.id));
      setDeliveriesByWebhook((prev) => {
        const copy = { ...prev };
        delete copy[toDelete.id];
        return copy;
      });
      setCreatedSecret(null);
      toast('Webhook deleted.', 'success');
      await loadWebhooks();
      onReload();
    } catch (_err) {
      toast(describeError(_err), 'error');
    } finally {
      setSaving(false);
      setToDelete(null);
    }
  }

  async function handleReactivate(wh: Webhook) {
    setSaving(true);
    try {
      const api = await import('../../lib/webhooksApi');
      const updated = await api.reactivateRepoWebhook(owner, repo, wh.id);
      setWebhooks((prev) => prev.map((w) => (w.id === updated.id ? updated : w)));
      toast('Webhook re-enabled.', 'success');
      onReload();
    } catch (err) {
      toast(describeError(err), 'error');
    } finally {
      setSaving(false);
    }
  }

  async function handleRedeliver(whId: string, deliveryId: string) {
    setRedelivering(deliveryId);
    try {
      const api = await import('../../lib/webhooksApi');
      await api.redeliverRepoWebhookDelivery(owner, repo, whId, deliveryId);
      toast('Redelivery queued.', 'success');
      loadDeliveries(webhooks.find((w) => w.id === whId) ?? editing!);
    } catch (err) {
      toast(describeError(err), 'error');
    } finally {
      setRedelivering(null);
    }
  }

  return (
    <>
      {/* Lista webhook */}
      <div className="sec-h">
        <h2>Webhooks</h2>
        <span className="sp" />
        <Button variant="primary" onClick={() => setEditing({} as Webhook)}>
          <Plus size={16} aria-hidden="true" />
          Add webhook
        </Button>
      </div>

      <p className="muted" style={{ marginBottom: 16 }}>
        {WEBHOOK_INFO}
      </p>

      {loading && webhooks.length === 0 ? (
        <p className="muted">Loading webhooks…</p>
      ) : webhooks.length === 0 ? (
        <EmptyState icon={<LinkIcon size={32} />} title="No webhooks" description="Create a webhook to receive notifications at an external URL." />
      ) : (
        <ul className="list" style={{ marginBottom: 24 }} aria-label="Webhooks">
          {webhooks.map((wh) => (
            <li key={wh.id} className="list-row">
              <span className="dot" style={{ background: wh.active ? 'var(--success)' : wh.disabledAt ? 'var(--text-subtle)' : 'var(--warning)' }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="mono" style={{ fontSize: 13 }}>
                  {wh.url}
                </div>
                <div className="small muted">
                  {wh.events.join(', ')}
                  {wh.lastDelivery ? (
                    <>
                      {' · '}
                      last delivery{' '}
                      <time dateTime={wh.lastDelivery.createdAt}>{timeAgo(wh.lastDelivery.createdAt)}</time>{' '}
                      {wh.lastDelivery.statusCode ? (
                        <b style={{ color: wh.lastDelivery.statusCode < 400 ? 'var(--success)' : 'var(--danger)' }}>{wh.lastDelivery.statusCode}</b>
                      ) : null}
                    </>
                  ) : null}
                  {wh.disabledAt && !wh.active && (
                    <>
                      {' · '}
                      <b style={{ color: 'var(--danger)' }}>disabled automatically</b>{' '}
                      {wh.disabledReason ? `(${wh.disabledReason})` : ''}
                    </>
                  )}
                  {wh.failingSince && !wh.disabledAt && (
                    <>
                      {' · '}
                      <b style={{ color: 'var(--danger)' }}>failing</b>
                    </>
                  )}
                </div>
              </div>
              {wh.active || !wh.disabledAt ? (
                <Button size="sm" onClick={() => setEditing(wh)}>
                  Edit
                </Button>
              ) : (
                <Button size="sm" onClick={() => handleReactivate(wh)} disabled={saving}>
                  Re-enable
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}

      {/* Form di modifica / creazione */}
      {editing && (
        <div className="card">
          <div className="card-h">
            {editing.id ? 'Edit webhook · ' : 'New webhook · '}
            <span className="mono" style={{ fontWeight: 400, fontSize: 13 }}>
              {editing.url ? (() => { try { return new URL(editing.url).hostname; } catch { return '…'; } })() : '…'}
            </span>
          </div>
          <div className="card-b stack" style={{ gap: 16 }}>
            {formErrors._form ? <ErrorAlert message={formErrors._form} /> : null}
            <form className="stack form-stack" onSubmit={editing.id ? handleUpdate : handleCreate} noValidate>
              <FormField label="Payload URL">
                <TextInput
                  className="mono"
                  name="url"
                  defaultValue={editing.url ?? ''}
                  style={{ fontFamily: 'var(--font-mono)', fontSize: 13 }}
                  required
                  placeholder="https://example.com/hooks/gitstack"
                />
              </FormField>
              <FormField
                label="Secret"
                hint="Used to sign each request with HMAC-SHA256 in the X-GitStack-Signature header."
              >
                <TextInput
                  name="secret"
                  defaultValue={editing.hasSecret ? '••••••••••••••••' : ''}
                  type={editing.hasSecret ? 'password' : 'text'}
                  placeholder={editing.hasSecret ? 'Secret hidden' : 'Generate a secret to sign requests'}
                />
              </FormField>
              <FormField label="Events">
                <div className="scope-grid">
                  {(['push', 'issues', 'issue_comment', 'repository'] as const).map((ev) => (
                    <label key={ev} className="check scope" style={{ borderStyle: 'solid', borderWidth: '1px', borderColor: 'var(--border)', borderRadius: 'var(--r)', padding: '8px 12px' }}>
                      <input type="checkbox" name={ev} defaultChecked={editing.events?.includes(ev)} />
                      <span>
                        <b className="mono">{ev}</b>
                        <br />
                        <span className="small muted">{EVENT_DESCRIPTIONS[ev]}</span>
                      </span>
                    </label>
                  ))}
                </div>
              </FormField>
              {editing.id ? (
                <label className="check">
                  <input type="checkbox" name="active" defaultChecked={editing.active} />
                  <span>Active</span>
                </label>
              ) : null}
              <div className="row">
                <span className="sp" />
                <Button type="button" variant="ghost" onClick={() => setEditing(null)}>
                  Cancel
                </Button>
                <Button type="submit" variant="primary" disabled={saving}>
                  {editing.id ? 'Save webhook' : 'Create webhook'}
                </Button>
              </div>
            </form>
          </div>

          {/* Consegne recenti */}
          {editing.id && (
            <>
              <div className="card-h" style={{ borderTop: '1px solid var(--border)' }}>
                Recent deliveries
              </div>
              {deliveriesLoading[editing.id] ? (
                <p className="muted small" style={{ padding: '8px 16px' }}>Loading deliveries…</p>
              ) : (
                <ul className="list" style={{ borderTop: 'none', borderRadius: 0 }} aria-label="Deliveries">
                  {(deliveriesByWebhook[editing.id] ?? []).length === 0 ? (
                    <li className="list-row small muted" style={{ padding: '8px 16px' }}>
                      No deliveries yet.
                    </li>
                  ) : (
                    (deliveriesByWebhook[editing.id] ?? []).map((d) => (
                      <li key={d.id} className="list-row small">
                        {d.status === 'success' ? (
                          <Check size={16} style={{ color: 'var(--success)' }} aria-hidden="true" />
                        ) : (
                          <X size={16} style={{ color: 'var(--danger)' }} aria-hidden="true" />
                        )}
                        <span className="mono">{d.event}</span>
                        {d.action ? <span className="mono subtle">{d.action}</span> : null}
                        <span className="sp" />
                        <b style={{ color: d.status === 'success' ? 'var(--success)' : 'var(--danger)' }}>
                          {d.statusCode ?? d.status}
                        </b>
                        {d.durationMs ? <span className="muted">{d.durationMs} ms</span> : null}
                        <span className="subtle">{timeAgo(d.createdAt)}</span>
                        <Button size="sm" disabled={redelivering === d.id} onClick={() => handleRedeliver(editing.id, d.id)}>
                          {redelivering === d.id ? 'Queued' : 'Redeliver'}
                        </Button>
                      </li>
                    ))
                  )}
                </ul>
              )}
              <div className="card-b row" style={{ borderTop: '1px solid var(--border)' }}>
                <span className="sp" />
                <Button variant="danger" onClick={() => setToDelete(editing)}>
                  <Trash2 size={14} aria-hidden="true" />
                  Delete
                </Button>
              </div>
            </>
          )}
        </div>
      )}

      {/* Conferma eliminazione */}
      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(open) => {
          if (!open) setToDelete(null);
        }}
        title="Delete webhook?"
        description={`"${toDelete?.url ?? ''}" will be removed permanently.`}
        confirmLabel="Delete"
        destructive
        onConfirm={() => void handleDelete()}
      />

      {/* Dialog segreto dopo la creazione */}
      <Dialog.Root
        open={createdSecret !== null}
        onOpenChange={(open) => {
          if (!open) setCreatedSecret(null);
        }}
      >
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content className="dialog dialog-wide">
            <Dialog.Title className="dialog-title">
              <Check size={16} className="ok-icon" aria-hidden="true" /> Webhook created
            </Dialog.Title>
            <Dialog.Description className="dialog-desc">
              Copy the secret now: you will not be able to see it again.
            </Dialog.Description>
            <div className="newtoken">
              <div className="row nowrap">
                <input className="input mono" aria-label="New webhook secret" value={createdSecret ?? ''} readOnly />
                <CopyButton value={createdSecret ?? ''} />
              </div>
            </div>
            <div className="dialog-actions">
              <Dialog.Close asChild>
                <Button variant="primary">Done</Button>
              </Dialog.Close>
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}
