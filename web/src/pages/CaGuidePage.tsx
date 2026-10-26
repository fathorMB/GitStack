import { AlertTriangle, CheckCircle2, Download, Info, Link2, Lock, Play, ShieldCheck } from 'lucide-react';
import { useState } from 'react';
import type { ReactNode } from 'react';
import { useLocation } from 'react-router-dom';
import { ErrorAlert, Logo } from '../components';
import { CA_DEVICES, detectDevice, fetchInstanceCa, parseAppParams, probeTrust } from '../lib/caApi';
import type { CaDevice, TrustResult } from '../lib/caApi';
import { useLoad } from '../lib/useLoad';

const DEVICE_TIME: Record<CaDevice, string> = {
  win: 'about 1 minute',
  mac: 'about 1 minute',
  ios: 'about 2 minutes',
  and: 'about 1 minute',
};

function Step({ n, children }: { n: number; children: ReactNode }) {
  return (
    <div className="guide-step">
      <span className="guide-n">{n}</span>
      <div>{children}</div>
    </div>
  );
}

function Steps({ device }: { device: CaDevice }) {
  switch (device) {
    case 'win':
      return (
        <>
          <Step n={1}>Open the downloaded file and choose <b>Install certificate</b>.</Step>
          <Step n={2}>
            Choose <b>Current user</b>, then <b>Place all certificates in the following store</b> →{' '}
            <b>Trusted Root Certification Authorities</b>.
          </Step>
          <Step n={3}>Confirm with <b>Yes</b> and restart the browser.</Step>
        </>
      );
    case 'mac':
      return (
        <>
          <Step n={1}>Open the downloaded file: <b>Keychain Access</b> adds it to <b>login</b>.</Step>
          <Step n={2}>
            Double-click <b>GitStack CA</b>, open <b>Trust</b> and set <b>When using this certificate</b> to{' '}
            <b>Always Trust</b>.
          </Step>
          <Step n={3}>Close the window and enter your Mac password.</Step>
        </>
      );
    case 'ios':
      return (
        <>
          <Step n={1}>Open this page in <b>Safari</b>, tap <b>Download certificate</b> and <b>Allow</b>.</Step>
          <Step n={2}><b>Settings</b> → <b>Profile Downloaded</b> → <b>Install</b>.</Step>
          <Step n={3}>
            <b>Settings</b> → <b>General</b> → <b>About</b> → <b>Certificate Trust Settings</b> → turn on <b>GitStack CA</b>.
          </Step>
        </>
      );
    case 'and':
      return (
        <>
          <Step n={1}>Tap <b>Download certificate</b>.</Step>
          <Step n={2}>
            <b>Settings</b> → <b>Security</b> → <b>More security settings</b> → <b>Install from device storage</b> →{' '}
            <b>CA certificate</b>, then pick <span className="mono">gitstack-ca.crt</span>.
          </Step>
          <Step n={3}>Confirm the warning about trusted certificates. Menu names can vary by brand.</Step>
        </>
      );
  }
}

// Pagina pubblica /help/ca (mockup H12, data-screen="ca"): guida all'installazione della CA
// interna per i tester (H2, N5). Sta fuori da RequireAuth e da AppShell: il tester ci arriva
// prima di fidarsi della CA e non vede il resto di GitStack. Gli indirizzi da provare
// arrivano dal link dell'invito (`?app=<indirizzo>`, ripetibile).
export function CaGuidePage() {
  const { data, loading, error } = useLoad(fetchInstanceCa);
  const { search } = useLocation();
  const apps = parseAppParams(search);
  const [device, setDevice] = useState<CaDevice>(() =>
    typeof navigator === 'undefined' ? 'win' : detectDevice(navigator.userAgent, navigator.maxTouchPoints),
  );
  const [picking, setPicking] = useState(false);
  const [results, setResults] = useState<Record<string, TrustResult> | null>(null);
  const [testing, setTesting] = useState(false);

  async function runTest() {
    setTesting(true);
    try {
      const entries = await Promise.all(apps.map(async (a) => [a, await probeTrust(a)] as const));
      setResults(Object.fromEntries(entries));
    } finally {
      setTesting(false);
    }
  }

  const allTrusted = results !== null && apps.every((a) => results[a] === 'trusted');
  const internal = data?.mode === 'internal';

  return (
    <div className="public-page">
      <main className="guide">
        <div className="row guide-brand">
          <Logo size={28} />
          <b>GitStack</b>
          <span className="sp" />
          <span className="small muted">{window.location.host}</span>
        </div>

        {error ? <ErrorAlert message={`${error} The certificate guide is not available right now.`} /> : null}
        {loading && !data ? <p className="muted">Loading…</p> : null}

        {data && !internal ? (
          <>
            <h1>Nothing to install</h1>
            <div className="alert alert-ok" role="status">
              <CheckCircle2 size={16} aria-hidden="true" />
              <div>
                <b>This GitStack does not use its own certificate authority.</b>{' '}
                {data.mode === 'custom'
                  ? 'Its certificate is issued by a public authority or by your company: your browser already trusts it, and so do the test apps.'
                  : 'It is not served over HTTPS, so there is no certificate to install.'}{' '}
                If your browser still shows a warning, ask the person who invited you.
              </div>
            </div>
          </>
        ) : null}

        {data && internal ? (
          <>
            <h1>Open the test apps without warnings</h1>
            <p className="muted guide-lead">
              Your company&apos;s GitStack signs its own certificates. Install its certificate once on this device and the
              browser will trust GitStack and every test app.
            </p>

            <div className="alert alert-info">
              <Info size={16} aria-hidden="true" />
              <div>
                The browser may have warned you when opening this page: that&apos;s the problem this guide solves. Check that
                the fingerprint below matches the one in the invitation you received.
                <div className="mono small guide-fp" aria-label="SHA-256 fingerprint">
                  SHA-256 {data.fingerprintSha256}
                </div>
              </div>
            </div>

            <div className="card">
              <div className="card-h">
                <ShieldCheck size={16} aria-hidden="true" />
                <span>
                  {CA_DEVICES.find((d) => d.id === device)?.label} · {DEVICE_TIME[device]}
                </span>
                <span className="sp" />
                <button type="button" className="btn btn-ghost btn-sm" onClick={() => setPicking((p) => !p)} aria-expanded={picking}>
                  Other device
                </button>
              </div>
              <div className="card-b">
                {picking ? (
                  <div className="seg guide-seg" role="group" aria-label="Device">
                    {CA_DEVICES.map((d) => (
                      <button
                        key={d.id}
                        type="button"
                        className={device === d.id ? 'active' : ''}
                        aria-pressed={device === d.id}
                        onClick={() => setDevice(d.id)}
                      >
                        {d.label}
                      </button>
                    ))}
                  </div>
                ) : null}
                <a className="btn btn-primary guide-download" href={data.downloadUrl ?? '/downloads/ca.crt'} download="gitstack-ca.crt">
                  <Download size={16} aria-hidden="true" />
                  Download certificate <span className="mono small">gitstack-ca.crt</span>
                </a>
                <Steps device={device} />
              </div>
            </div>

            {apps.length > 0 ? (
              <div className="row guide-try">
                <button type="button" className="btn btn-primary" onClick={() => void runTest()} disabled={testing}>
                  <Play size={16} aria-hidden="true" />
                  {testing ? 'Trying…' : `Try: open ${apps[0]?.split('.')[0] ?? 'the app'}`}
                </button>
                <span className="hint">
                  Checks a secure connection to <span className="mono">{apps.join(', ')}</span>.
                </span>
              </div>
            ) : null}

            {results ? (
              <div className="guide-result" role="status">
                {allTrusted ? (
                  <div className="alert alert-ok">
                    <CheckCircle2 size={16} aria-hidden="true" />
                    <div>
                      <b>Done: this device trusts GitStack.</b> The test apps opened over a secure connection without
                      warnings. The same certificate covers every other test app.
                    </div>
                  </div>
                ) : (
                  <div className="alert alert-warning">
                    <AlertTriangle size={16} aria-hidden="true" />
                    <div>
                      <b>Not trusted yet.</b> The browser still rejects the connection. Finish the steps above, restart the
                      browser and try again. If the app is switched off the test fails in the same way.
                    </div>
                  </div>
                )}
                <div className="list guide-list">
                  {apps.map((a) => (
                    <div className="list-row" key={a}>
                      <Link2 size={16} className="muted" aria-hidden="true" />
                      <span className="mono grow">{a}</span>
                      {results[a] === 'trusted' ? (
                        <span className="badge badge-ok">
                          <Lock size={12} aria-hidden="true" />
                          secure
                        </span>
                      ) : (
                        <span className="badge badge-danger">not trusted</span>
                      )}
                      <a className="btn btn-sm" href={`https://${a}/`}>
                        Open
                      </a>
                    </div>
                  ))}
                </div>
                {allTrusted ? (
                  <p className="hint">
                    Found a problem in the app? Use <b>Report a problem</b> inside it: it opens an issue for the team with
                    your screenshot and your name.
                  </p>
                ) : null}
              </div>
            ) : null}

            <p className="hint guide-foot">
              On a company-managed computer you may not be allowed to install certificates: send this page to your IT team.
              Only install a certificate if the fingerprint matches.
            </p>
          </>
        ) : null}
      </main>
    </div>
  );
}
