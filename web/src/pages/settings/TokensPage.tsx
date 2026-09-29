import * as Dialog from '@radix-ui/react-dialog';
import { Check, Key, Plus } from 'lucide-react';
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
import { TOKEN_SCOPES, createPersonalToken, fetchTokens, revokePersonalToken } from '../../lib/accessApi';
import type { CreatedToken, Token, TokenScope } from '../../lib/accessApi';
import { describeError } from '../../lib/http';
import { daysFromNow, formatDate, usedText } from '../../lib/format';
import { useLoad } from '../../lib/useLoad';

// La scadenza e' obbligatoria nel contratto (max 365 giorni di default).
const EXPIRATIONS = [
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' },
  { value: '365', label: '365 days' },
];

// Token personali (mockup 14 "Access tokens"): elenco, creazione con scope e
// scadenza, valore mostrato una sola volta in un dialog (vive solo nello
// stato del componente: chiuso il dialog non e' piu' recuperabile), revoca.
export function TokensPage() {
  const { data, setData, loading, error } = useLoad(fetchTokens);
  const { toast } = useToast();
  const tokens = data ?? [];

  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [days, setDays] = useState('90');
  const [scopes, setScopes] = useState<TokenScope[]>([]);
  const [formError, setFormError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [created, setCreated] = useState<CreatedToken | null>(null);

  const [toRevoke, setToRevoke] = useState<Token | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  function resetForm() {
    setCreating(false);
    setName('');
    setDays('90');
    setScopes([]);
    setFormError(null);
  }

  function toggleScope(scope: TokenScope, on: boolean) {
    setScopes((cur) => (on ? [...cur, scope] : cur.filter((s) => s !== scope)));
  }

  async function handleCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!name.trim()) {
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
      const token = await createPersonalToken({
        name: name.trim(),
        scopes: TOKEN_SCOPES.map((s) => s.value).filter((s) => scopes.includes(s)),
        expiresAt: daysFromNow(Number(days)),
      });
      // Nell'elenco va la parte senza valore.
      const { token: _value, ...listed } = token;
      void _value;
      setData([listed, ...tokens]);
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
      await revokePersonalToken(token.id);
      setData(tokens.filter((t) => t.id !== token.id));
      toast(`Token "${token.name}" revoked.`, 'success');
    } catch (err) {
      setActionError(describeError(err));
    }
  }

  return (
    <div>
      <div className="sec-h">
        <h2>Access tokens</h2>
        <span className="sp" />
        {!creating ? (
          <Button variant="primary" onClick={() => setCreating(true)}>
            <Plus size={16} aria-hidden="true" />
            Generate token
          </Button>
        ) : null}
      </div>
      <p className="muted section-lead">
        Tokens let the <span className="mono">gs</span> CLI, scripts and coding agents act on your behalf. Give each one
        only the scopes it needs.
      </p>

      {error ? <ErrorAlert message={error} /> : null}
      {actionError ? <ErrorAlert message={actionError} /> : null}

      {creating ? (
        <form className="card section-gap" onSubmit={(e) => void handleCreate(e)} noValidate aria-label="New token">
          <div className="card-h">New token</div>
          <div className="card-b stack form-stack">
            {formError ? <ErrorAlert message={formError} /> : null}
            <div className="row form-row">
              <div className="grow">
                <FormField label="Name">
                  <TextInput value={name} onChange={(e) => setName(e.target.value)} placeholder="claude-code" />
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
                      onCheckedChange={(on) => toggleScope(s.value, on === true)}
                    />
                  </div>
                ))}
              </div>
            </fieldset>
            <div className="row">
              <span className="sp" />
              <Button onClick={resetForm}>Cancel</Button>
              <Button type="submit" variant="primary" disabled={submitting}>
                {submitting ? 'Generating…' : 'Generate token'}
              </Button>
            </div>
          </div>
        </form>
      ) : null}

      {loading && data === null ? (
        <p className="muted">Loading tokens…</p>
      ) : tokens.length === 0 ? (
        error ? null : (
          <EmptyState icon={<Key size={32} />} title="No access tokens" description="Generate a token to use the CLI or a coding agent." />
        )
      ) : (
        <ul className="list" aria-label="Access tokens">
          {tokens.map((t) => (
            <li key={t.id} className="list-row">
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
            </li>
          ))}
        </ul>
      )}

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

      <Dialog.Root
        open={created !== null}
        onOpenChange={(open) => {
          if (!open) setCreated(null);
        }}
      >
        <Dialog.Portal>
          <Dialog.Overlay className="dialog-overlay" />
          <Dialog.Content className="dialog dialog-wide">
            <Dialog.Title className="dialog-title">
              <Check size={16} className="ok-icon" aria-hidden="true" /> Token &quot;{created?.name}&quot; created
            </Dialog.Title>
            <Dialog.Description className="dialog-desc">
              Copy it now: you won&apos;t be able to see it again.
            </Dialog.Description>
            <div className="newtoken">
              <div className="row nowrap">
                <input className="input mono" aria-label="New token value" value={created?.token ?? ''} readOnly />
                <CopyButton value={created?.token ?? ''} />
              </div>
              <div className="cmdblock">
                <span className="p">$ </span>gs auth login --host {window.location.host} --with-token
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
    </div>
  );
}
