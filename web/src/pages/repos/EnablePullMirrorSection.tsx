import { useState } from 'react';
import type { FormEvent } from 'react';
import { Button, ErrorAlert, FormField, PasswordInput, Select, TextInput, useToast } from '../../components';
import { ApiError, describeError, fieldErrors } from '../../lib/http';
import { enablePullMirror } from '../../lib/pullMirrorsApi';
import type { PullMirror } from '../../lib/pullMirrorsApi';

const INTERVALS = [
  { value: '300', label: '5 minutes' },
  { value: '600', label: '10 minutes' },
  { value: '900', label: '15 minutes' },
  { value: '1800', label: '30 minutes' },
  { value: '3600', label: '1 hour' },
  { value: '7200', label: '2 hours' },
  { value: '14400', label: '4 hours' },
  { value: '28800', label: '8 hours' },
  { value: '43200', label: '12 hours' },
  { value: '86400', label: '24 hours' },
];

/**
 * Sezione per abilitare il mirror in pull su un repo esistente (P4, MP1, MP3).
 * Si mostra quando il repo non ha un mirror attivo.
 */
export function EnablePullMirrorSection({
  owner,
  repo,
  onSuccess,
}: {
  owner: string;
  repo: string;
  onSuccess: (m: PullMirror) => void;
}) {
  const { toast } = useToast();
  const [url, setUrl] = useState('');
  const [username, setUsername] = useState('');
  const [token, setToken] = useState('');
  const [interval, setInterval] = useState('600');
  const [busy, setBusy] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [general, setGeneral] = useState('');

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErrors({});
    setGeneral('');
    try {
      const input: { url: string; username?: string; token?: string; intervalSeconds?: number } = {
        url: url.trim(),
      };
      if (username.trim() !== '') input.username = username.trim();
      if (token !== '') input.token = token;
      input.intervalSeconds = Number(interval);
      const m = await enablePullMirror(owner, repo, input);
      toast(`Pull mirror enabled for ${repo}.`, 'success');
      onSuccess(m);
    } catch (err) {
      if (err instanceof ApiError && (err.status === 409 || err.status === 422 || err.status === 400)) {
        const fields = fieldErrors(err);
        if (Object.keys(fields).length > 0) setErrors(fields);
        else setGeneral(describeError(err));
      } else {
        setGeneral(describeError(err));
      }
    } finally {
      setBusy(false);
    }
  }

  const valid = url.trim() !== '';

  return (
    <section aria-label="Enable pull mirror">
      <div className="sec-h">
        <h2>Pull mirror</h2>
        <span className="badge">Off</span>
      </div>
      <div className="card card-b stack" style={{ gap: 16 }}>
        <p className="muted">
          Keep this repository as a copy of another Git server. While the mirror is on, <b>code is read-only</b> here and only the mirror updates branches and tags.
        </p>
        <form onSubmit={(e) => void submit(e)}>
          {general ? <ErrorAlert message={general} /> : null}
          <FormField label="Source URL" hint="HTTPS only, no credentials in the URL." error={errors.url}>
            <TextInput
              className="mono"
              value={url}
              placeholder="https://github.com/example/repo.git"
              onChange={(e) => setUrl(e.target.value)}
            />
          </FormField>
          <div className="row" style={{ gap: 10, flexWrap: 'nowrap' }}>
            <FormField label="Username (optional)" error={errors.username}>
              <TextInput value={username} onChange={(e) => setUsername(e.target.value)} />
            </FormField>
            <FormField label="Access token (optional)" error={errors.token}>
              <PasswordInput value={token} autoComplete="new-password" onChange={(e) => setToken(e.target.value)} />
            </FormField>
          </div>
          <FormField label="Update every" error={errors.interval}>
            <Select
              value={interval}
              onValueChange={(v) => setInterval(v)}
              options={INTERVALS}
              aria-label="Update interval"
            />
          </FormField>
          <div className="alert alert-info" style={{ marginTop: 8 }}>
            <svg className="icon" aria-hidden="true">
              <use href="#i-info" />
            </svg>
            <div>
              The mirror copies all branches and tags. Nothing is ever deleted or overwritten:
              rewritten refs are flagged as <i>diverged</i> and kept as they are.
            </div>
          </div>
          <div className="alert alert-warning" style={{ marginTop: 8 }}>
            <svg className="icon" aria-hidden="true">
              <use href="#i-warning" />
            </svg>
            <div>
              <b>This repository already has content.</b> The mirror keeps the local branches and tags that don&apos;t match the source.
              Refs that differ will be flagged as <i>diverged</i> and GitStack will keep its local version.
            </div>
          </div>
          <div className="row" style={{ borderTop: '1px solid var(--border)', paddingTop: 16 }}>
            <span className="sp" />
            <Button type="submit" variant="primary" disabled={busy || !valid}>
              Enable pull mirror
            </Button>
          </div>
        </form>
      </div>
    </section>
  );
}
