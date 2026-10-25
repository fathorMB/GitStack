import { AlertTriangle, Info, Rocket, ShieldCheck, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Button, ErrorAlert } from '../../../components';
import { createApp } from '../../../lib/appsApi';
import { formatCpu } from '../../../lib/appsView';
import { describeError } from '../../../lib/http';
import type { PackageList } from '../../../lib/packagesApi';
import { fetchPackages } from '../../../lib/packagesApi';
import type { Repository } from '../../../lib/reposApi';
import { useAppPermissions } from '../../../lib/useAppPermissions';
import { appsPath } from './appMode';

type Source = 'repo' | 'external';

interface ImageChoice {
  reference: string;
  signed: boolean;
  tag: string;
}

/** Immagini del registry del repo, un'opzione per tag. Il riferimento usa l'host da cui si serve la UI. */
function imageChoices(list: PackageList | null): ImageChoice[] {
  if (!list) return [];
  const host = window.location.host;
  const out: ImageChoice[] = [];
  for (const p of list.items) {
    if (p.kind === 'helm') continue;
    const tags = p.tagDetails ?? p.tags.map((name) => ({ name, signature: p.signature }));
    for (const t of tags) out.push({ reference: `${host}/${p.name}:${t.name}`, signed: t.signature === 'verified', tag: t.name });
  }
  return out;
}

/** New app / Deploy (H2, mockup data-screen="new"): immagine del repo firmata, esterna non verificata, server pieno. */
export function NewAppPage({ repo }: { repo: Repository }) {
  const owner = repo.owner.name;
  const name = repo.name;
  const navigate = useNavigate();
  const perms = useAppPermissions(repo);

  const [appName, setAppName] = useState('');
  const [source, setSource] = useState<Source>('repo');
  const [packages, setPackages] = useState<PackageList | null>(null);
  const [repoImage, setRepoImage] = useState('');
  const [externalImage, setExternalImage] = useState('');
  const [follow, setFollow] = useState(true);
  const [port, setPort] = useState('8080');
  const [cpuG, setCpuG] = useState('0.5');
  const [memG, setMemG] = useState('512');
  const [cpuL, setCpuL] = useState('1');
  const [memL, setMemL] = useState('1024');
  const [copies, setCopies] = useState(2);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [full, setFull] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    fetchPackages(owner, name).then(
      (l) => live && setPackages(l),
      () => live && setPackages(null),
    );
    return () => {
      live = false;
    };
  }, [owner, name]);

  const choices = imageChoices(packages);
  const selected = choices.find((c) => c.reference === repoImage) ?? choices[0] ?? null;
  const image = source === 'repo' ? (selected?.reference ?? '') : externalImage.trim();
  const canFollow = source === 'repo' && Boolean(selected?.signed);
  const followBranch = canFollow && follow ? repo.defaultBranch || 'main' : null;

  if (perms.ready && !perms.canCreate) {
    return <ErrorAlert message="Creating an app requires admin on the repository." />;
  }

  // Ogni modifica toglie il rifiuto «server pieno»: si puo' riprovare.
  const edit = <T,>(set: (v: T) => void) => (v: T) => {
    setFull(null);
    set(v);
  };

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setFull(null);
    try {
      const created = await createApp(owner, name, {
        name: appName.trim(),
        image,
        port: Number(port) || undefined,
        followBranch,
        resources: {
          guaranteed: { cpuMillis: Math.round(Number(cpuG) * 1000), memoryBytes: Math.round(Number(memG)) * 1024 * 1024 },
          limit: { cpuMillis: Math.round(Number(cpuL) * 1000), memoryBytes: Math.round(Number(memL)) * 1024 * 1024 },
        },
        copies,
      });
      void navigate(appsPath(owner, name, created.app.name));
    } catch (err) {
      if ((err as { code?: string }).code === 'insufficient_capacity') setFull((err as Error).message);
      else setError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  const guaranteedTotal = formatCpu(Math.round(Number(cpuG) * 1000) * copies);

  return (
    <form className="page-narrow" onSubmit={(e) => void submit(e)} aria-label="New app">
      <h1 style={{ fontSize: 22, fontWeight: 600, marginBottom: 4 }}>New app</h1>
      <p className="muted" style={{ marginBottom: 20 }}>
        in{' '}
        <b>
          {owner}/{name}
        </b>{' '}
        · requires admin on the repository
      </p>
      <div className="stack" style={{ gap: 18 }}>
        <div className="field">
          <label htmlFor="app-name">Name</label>
          <input id="app-name" className="input" value={appName} required onChange={(e) => edit(setAppName)(e.target.value)} />
          <span className="hint">Address assigned automatically · internal, certificate from the GitStack CA</span>
        </div>

        <div className="field">
          <label id="app-image-label">Image</label>
          <div className="seg" role="group" aria-labelledby="app-image-label" style={{ alignSelf: 'flex-start' }}>
            <button type="button" className={source === 'repo' ? 'active' : undefined} aria-pressed={source === 'repo'} onClick={() => edit(setSource)('repo')}>
              From this repository
            </button>
            <button type="button" className={source === 'external' ? 'active' : undefined} aria-pressed={source === 'external'} onClick={() => edit(setSource)('external')}>
              External registry
            </button>
          </div>
          {source === 'repo' ? (
            <div className="stack" style={{ gap: 8 }}>
              {choices.length === 0 ? (
                <p className="muted">This repository has no published images yet.</p>
              ) : (
                <select className="input mono" aria-label="Repository image" value={selected?.reference ?? ''} onChange={(e) => edit(setRepoImage)(e.target.value)}>
                  {choices.map((c) => (
                    <option key={c.reference} value={c.reference}>
                      {c.reference}
                    </option>
                  ))}
                </select>
              )}
              {selected?.signed ? (
                <div className="alert alert-ok">
                  <ShieldCheck size={16} aria-hidden="true" />
                  <div>
                    <b>Signed by the CI of {owner}/{name}</b> · <span className="mono">{selected.tag}</span>. Required for automatic deploys.
                  </div>
                </div>
              ) : selected ? (
                <div className="alert alert-warning">
                  <AlertTriangle size={16} aria-hidden="true" />
                  <div>
                    <b>Not verified.</b> This image has no signature from a trusted key. Automatic deploys are only possible with images signed by this repository&apos;s CI.
                  </div>
                </div>
              ) : null}
            </div>
          ) : (
            <div className="stack" style={{ gap: 8 }}>
              <input
                className="input mono"
                aria-label="External image"
                placeholder="docker.io/requarks/wiki:2.5"
                value={externalImage}
                onChange={(e) => edit(setExternalImage)(e.target.value)}
              />
              <div className="alert alert-warning">
                <AlertTriangle size={16} aria-hidden="true" />
                <div>
                  <b>Not verified.</b> This image has no signature from a trusted key. Manual deploys are allowed and the app will show a <i>not verified</i> label. Automatic deploys are only possible with images signed by this repository&apos;s CI.
                </div>
              </div>
              <div className="alert alert-info">
                <Info size={16} aria-hidden="true" />
                <div>Whether the image runs as root is checked when it is deployed; the app is isolated from the server and from other apps either way.</div>
              </div>
            </div>
          )}
        </div>

        <fieldset className="field" style={{ border: 0, padding: 0 }}>
          <legend>Deploys</legend>
          <label className={`opt${canFollow && follow ? ' on' : canFollow ? '' : ' off'}`}>
            <input type="radio" name="dm" checked={canFollow && follow} disabled={!canFollow} onChange={() => edit(setFollow)(true)} />
            <div>
              <b>
                Follow branch <span className="mono">{repo.defaultBranch || 'main'}</span>
              </b>
              <div className="hint">
                {canFollow
                  ? 'Each image the CI publishes for this branch goes live. If the new version fails its health check, the previous one keeps running.'
                  : 'Not available for external or unsigned images.'}
              </div>
            </div>
          </label>
          <label className={`opt${canFollow && follow ? '' : ' on'}`}>
            <input type="radio" name="dm" checked={!(canFollow && follow)} onChange={() => edit(setFollow)(false)} />
            <div>
              <b>Manual only</b>
              <div className="hint">
                Deploy from here, <span className="mono">gs app deploy</span> or a workflow step.
              </div>
            </div>
          </label>
        </fieldset>

        <div className="field">
          <label htmlFor="app-port">Port</label>
          <input id="app-port" className="input" inputMode="numeric" value={port} style={{ maxWidth: 140 }} onChange={(e) => edit(setPort)(e.target.value)} />
        </div>

        <div className="field">
          <label id="app-res-label">Resources</label>
          <div className="grid3" role="group" aria-labelledby="app-res-label">
            <div className="card card-b small stack">
              <b>Guaranteed</b>
              <label className="row">
                <input className="input" aria-label="Guaranteed vCPU" value={cpuG} onChange={(e) => edit(setCpuG)(e.target.value)} /> vCPU
              </label>
              <label className="row">
                <input className="input" aria-label="Guaranteed memory MB" value={memG} onChange={(e) => edit(setMemG)(e.target.value)} /> MB
              </label>
              <span className="hint">Always reserved</span>
            </div>
            <div className="card card-b small stack">
              <b>Limit</b>
              <label className="row">
                <input className="input" aria-label="Limit vCPU" value={cpuL} onChange={(e) => edit(setCpuL)(e.target.value)} /> vCPU
              </label>
              <label className="row">
                <input className="input" aria-label="Limit memory MB" value={memL} onChange={(e) => edit(setMemL)(e.target.value)} /> MB
              </label>
              <span className="hint">Used when the server has room</span>
            </div>
            <div className="card card-b small">
              <b>Copies</b>
              <div className="stepper" style={{ marginTop: 4 }}>
                <button type="button" aria-label="Fewer copies" disabled={copies <= 1} onClick={() => edit(setCopies)(copies - 1)}>
                  −
                </button>
                <span aria-live="polite">{copies}</span>
                <button type="button" aria-label="More copies" onClick={() => edit(setCopies)(copies + 1)}>
                  +
                </button>
              </div>
              <span className="hint">{guaranteedTotal} vCPU guaranteed in total</span>
            </div>
          </div>
        </div>

        {full ? (
          <div className="alert alert-danger" role="alert">
            <X size={16} aria-hidden="true" />
            <div>
              <b>Not enough room on the server.</b> {full} Lower the guaranteed share or copies, stop another app, or ask an administrator to enlarge the apps share.
            </div>
          </div>
        ) : null}
        {error ? <ErrorAlert message={error} /> : null}

        <div className="row">
          <span className="sp" />
          <Link className="btn" to={appsPath(owner, name)}>
            Cancel
          </Link>
          <Button type="submit" variant="primary" disabled={busy || full !== null || !appName.trim() || !image}>
            <Rocket size={16} aria-hidden="true" />
            Create and deploy
          </Button>
        </div>
      </div>
    </form>
  );
}
