import { ShieldCheck } from 'lucide-react';
import { useMemo, useState } from 'react';
import type { FormEvent } from 'react';
import { Button, CopyButton, ErrorAlert, FormField, PasswordInput, TextInput, useToast } from '../../components';
import {
  confirmTotp,
  disableOwnTotp,
  fetchTotpStatus,
  startTotpEnrollment,
} from '../../lib/authApi';
import type { TotpEnrollment } from '../../lib/authApi';
import { describeError, fieldErrors } from '../../lib/http';
import { qrMatrix, qrSvgPath } from '../../lib/qr';
import { useLoad } from '../../lib/useLoad';
import { useAuth } from '../../routes/auth';
import { useSettingsUser } from './SettingsLayout';

// QR del segreto TOTP: disegnato in locale (lib/qr.ts), il segreto non lascia
// mai il browser. Se l'URI non entra in un QR si mostra solo il segreto.
function QrCode({ value }: { value: string }) {
  const drawing = useMemo(() => {
    try {
      const m = qrMatrix(value);
      return { size: m.length, path: qrSvgPath(m) };
    } catch {
      return null;
    }
  }, [value]);
  if (!drawing) return null;
  const q = 4; // quiet zone
  const total = drawing.size + 2 * q;
  return (
    <svg
      role="img"
      aria-label="QR code for your authenticator app"
      viewBox={`${-q} ${-q} ${total} ${total}`}
      width={192}
      height={192}
      shapeRendering="crispEdges"
      style={{ background: '#fff' }}
    >
      <path d={drawing.path} fill="#000" />
    </svg>
  );
}

// Sicurezza dell'account (F11): secondo fattore TOTP per gli account locali.
// Attivazione in due tempi (segreto + QR, poi un codice che lo conferma),
// codici di recupero mostrati una sola volta, disattivazione con password e
// codice. Gli utenti OIDC prendono l'MFA dal loro provider.
export function SecurityPage() {
  const user = useSettingsUser();
  const { setMustEnrollTotp } = useAuth();
  const { toast } = useToast();
  const { data: status, setData: setStatus, loading, error } = useLoad(fetchTotpStatus);

  const [enrollment, setEnrollment] = useState<TotpEnrollment | null>(null);
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);

  const [password, setPassword] = useState('');
  const [disableCode, setDisableCode] = useState('');
  const [useRecovery, setUseRecovery] = useState(false);

  async function handleStart() {
    setBusy(true);
    setFormError(null);
    try {
      setEnrollment(await startTotpEnrollment());
      setCode('');
    } catch (err) {
      setFormError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  async function handleConfirm(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!/^[0-9]{6}$/.test(code.trim())) {
      setFormError('Enter the 6-digit code shown by your authenticator app.');
      return;
    }
    setBusy(true);
    setFormError(null);
    try {
      const codes = await confirmTotp(code.trim());
      setRecoveryCodes(codes);
      setEnrollment(null);
      setCode('');
      setStatus({
        enabled: true,
        available: true,
        requiredForAdmins: status?.requiredForAdmins ?? false,
        recoveryCodesRemaining: codes.length,
      });
      setMustEnrollTotp(false);
    } catch (err) {
      const fields = fieldErrors(err);
      setFormError(fields.code ? 'That code is not valid. Check the time on your device and try again.' : describeError(err));
    } finally {
      setBusy(false);
    }
  }

  async function handleDisable(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!password || !disableCode.trim()) {
      setFormError('Enter your password and a code to turn two-factor authentication off.');
      return;
    }
    setBusy(true);
    setFormError(null);
    try {
      await disableOwnTotp(password, useRecovery ? { recoveryCode: disableCode.trim() } : { totpCode: disableCode.replace(/\s+/g, '') });
      setStatus({ enabled: false, available: status?.available ?? true, requiredForAdmins: status?.requiredForAdmins ?? false, recoveryCodesRemaining: 0 });
      setPassword('');
      setDisableCode('');
      toast('Two-factor authentication turned off.', 'success');
    } catch (err) {
      setFormError(describeError(err));
    } finally {
      setBusy(false);
    }
  }

  const required = Boolean(status?.requiredForAdmins) && Boolean(user.isAdmin);
  const enabled = Boolean(status?.enabled);

  return (
    <div>
      <div className="sec-h">
        <h2>Security</h2>
      </div>

      {error ? <ErrorAlert message={error} /> : null}
      {loading && !status ? <p className="muted">Loading…</p> : null}

      {status ? (
        <section className="card section-gap" aria-label="Two-factor authentication">
          <div className="card-h">
            <ShieldCheck size={16} aria-hidden="true" /> Two-factor authentication
          </div>
          <div className="card-b stack form-stack">
            {required && !enabled ? (
              <ErrorAlert message="This installation requires administrators to use two-factor authentication. Set it up to continue." />
            ) : null}
            {formError ? <ErrorAlert message={formError} /> : null}

            {recoveryCodes ? (
              <div className="stack" role="region" aria-label="Recovery codes">
                <p>
                  <b>Two-factor authentication is on.</b> Save these recovery codes somewhere safe: each works once, and
                  you won&apos;t be able to see them again.
                </p>
                <ul className="mono" aria-label="Recovery codes list">
                  {recoveryCodes.map((c) => (
                    <li key={c}>{c}</li>
                  ))}
                </ul>
                <div className="row">
                  <CopyButton value={recoveryCodes.join('\n')} label="Copy codes" />
                  <span className="sp" />
                  <Button variant="primary" onClick={() => setRecoveryCodes(null)}>
                    I saved these codes
                  </Button>
                </div>
              </div>
            ) : enabled ? (
              <>
                <p>
                  Two-factor authentication is <b>on</b>. You have {status.recoveryCodesRemaining} unused recovery code
                  {status.recoveryCodesRemaining === 1 ? '' : 's'}.
                </p>
                {required ? (
                  <p className="muted">
                    This installation requires administrators to keep it on, so it can&apos;t be turned off here.
                  </p>
                ) : (
                  <form className="stack" onSubmit={(e) => void handleDisable(e)} noValidate aria-label="Turn off two-factor authentication">
                    <FormField label="Password">
                      <PasswordInput value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
                    </FormField>
                    <FormField label={useRecovery ? 'Recovery code' : 'Authentication code'}>
                      <TextInput
                        value={disableCode}
                        onChange={(e) => setDisableCode(e.target.value)}
                        autoComplete="one-time-code"
                        inputMode={useRecovery ? 'text' : 'numeric'}
                      />
                    </FormField>
                    <div className="row">
                      <Button
                        type="button"
                        onClick={() => {
                          setUseRecovery(!useRecovery);
                          setDisableCode('');
                        }}
                      >
                        {useRecovery ? 'Use an authenticator code' : 'Use a recovery code'}
                      </Button>
                      <span className="sp" />
                      <Button type="submit" variant="danger" disabled={busy}>
                        {busy ? 'Turning off…' : 'Turn off two-factor authentication'}
                      </Button>
                    </div>
                  </form>
                )}
              </>
            ) : enrollment ? (
              <form className="stack" onSubmit={(e) => void handleConfirm(e)} noValidate aria-label="Set up two-factor authentication">
                <p>
                  Scan the QR code with an authenticator app (or type the key by hand), then enter the 6-digit code it
                  shows.
                </p>
                <QrCode value={enrollment.otpauthUri} />
                <FormField label="Setup key" hint="Use this if you can't scan the QR code.">
                  <div className="row">
                    <span className="mono" aria-label="Setup key">
                      {enrollment.secret}
                    </span>
                    <CopyButton value={enrollment.secret} label="Copy key" />
                  </div>
                </FormField>
                <FormField label="Authentication code">
                  <TextInput
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                    autoComplete="one-time-code"
                    inputMode="numeric"
                    autoFocus
                  />
                </FormField>
                <div className="row">
                  <span className="sp" />
                  <Button type="button" onClick={() => setEnrollment(null)}>
                    Cancel
                  </Button>
                  <Button type="submit" variant="primary" disabled={busy}>
                    {busy ? 'Verifying…' : 'Verify and turn on'}
                  </Button>
                </div>
              </form>
            ) : status.available ? (
              <>
                <p className="muted">
                  Add a second step to your password sign-in with a time-based code from an authenticator app. Personal
                  access tokens are not affected. Accounts that sign in with OIDC use the provider&apos;s own MFA.
                </p>
                <div className="row">
                  <Button variant="primary" onClick={() => void handleStart()} disabled={busy}>
                    {busy ? 'Starting…' : 'Set up two-factor authentication'}
                  </Button>
                </div>
              </>
            ) : (
              <p className="muted">
                Two-factor authentication is not available: the administrator has not configured the encryption keys
                (GITSTACK_IDENTITY_TOTP_KEYS) yet.
              </p>
            )}
          </div>
        </section>
      ) : null}
    </div>
  );
}
