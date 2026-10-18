import { ShieldCheck } from 'lucide-react';
import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { Button, ErrorAlert, FormField, Logo, PasswordInput, TextInput } from '../components';
import { SECOND_FACTOR_REQUIRED, fetchOidcProviders, loginWithPassword, oidcStartUrl } from '../lib/authApi';
import type { OidcProvider } from '../lib/authApi';
import { ApiError, describeError } from '../lib/http';
import { useAuth } from '../routes/auth';

// Login (mockup 01, design/mockups-v1 data-screen="login"): pannello di
// brand a sinistra, a destra i pulsanti OIDC (solo se GET
// /auth/oidc/providers dà una lista non vuota) e il form con password.
// Il cookie di sessione e' HttpOnly: la UI non lo legge, dopo il 200 si va
// dove si voleva andare (stato di rotta `from`).
export function LoginPage() {
  const { clearUnauthorized, setMustChangePassword, setMustEnrollTotp } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from ?? '/';

  const [providers, setProviders] = useState<OidcProvider[]>([]);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Secondo passo (F11): la password era giusta e l'utente ha il TOTP attivo.
  const [secondStep, setSecondStep] = useState(false);
  const [useRecovery, setUseRecovery] = useState(false);
  const [code, setCode] = useState('');

  useEffect(() => {
    let cancelled = false;
    void fetchOidcProviders().then((list) => {
      if (!cancelled) setProviders(list);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!username.trim() || !password) {
      setError('Enter your username or email and your password.');
      return;
    }
    if (secondStep && !code.trim()) {
      setError(useRecovery ? 'Enter one of your recovery codes.' : 'Enter the 6-digit code from your authenticator app.');
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const session = secondStep
        ? await loginWithPassword(
            username.trim(),
            password,
            useRecovery ? { recoveryCode: code.trim() } : { totpCode: code.replace(/\s+/g, '') },
          )
        : await loginWithPassword(username.trim(), password);
      clearUnauthorized();
      setMustEnrollTotp(session?.mustEnrollTotp === true);
      if (session?.mustEnrollTotp) {
        navigate('/settings/security', { replace: true });
      } else if (session?.mustChangePassword) {
        setMustChangePassword(true);
        navigate('/change-password', { replace: true, state: { from } });
      } else {
        setMustChangePassword(false);
        navigate(from, { replace: true });
      }
    } catch (err) {
      if (err instanceof ApiError && err.code === SECOND_FACTOR_REQUIRED) {
        setSecondStep(true);
        setCode('');
        setError(null);
      } else if (secondStep && err instanceof ApiError && err.code === 'invalid_credentials') {
        setError(
          useRecovery
            ? 'That recovery code is not valid or was already used.'
            : 'That code is not valid. Check the time on your device and try again.',
        );
      } else {
        setError(describeError(err));
      }
      setSubmitting(false);
    }
  }

  function backToPassword() {
    setSecondStep(false);
    setUseRecovery(false);
    setCode('');
    setError(null);
    setPassword('');
  }

  return (
    <main className="login">
      <div className="login-l">
        <div className="row login-brand">
          <Logo size={28} className="login-logo" />
          GitStack
        </div>
        <div>
          <h2>Your code, your issues, your servers.</h2>
          <p>Self-hosted Git and issue tracking for teams and their coding agents.</p>
        </div>
        <div className="small login-host">{window.location.host}</div>
        <Logo size={420} className="big" />
      </div>
      <div className="login-r">
        <div className="login-card">
          <h1>Sign in to GitStack</h1>
          <p className="muted login-lead">Use your company account or a local GitStack account.</p>
          {providers.map((p) => (
            <a key={p.slug} className="btn btn-block" href={oidcStartUrl(p.slug, from)}>
              <ShieldCheck size={16} aria-hidden="true" />
              Continue with {p.displayName}
            </a>
          ))}
          {providers.length > 0 ? <div className="or">or</div> : null}
          <form className="stack login-form" onSubmit={(e) => void handleSubmit(e)} noValidate>
            {error ? <ErrorAlert message={error} /> : null}
            {secondStep ? (
              <>
                <p className="muted">
                  {useRecovery
                    ? 'Enter one of your single-use recovery codes.'
                    : 'Enter the 6-digit code from your authenticator app.'}
                </p>
                <FormField label={useRecovery ? 'Recovery code' : 'Authentication code'}>
                  <TextInput
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                    autoComplete="one-time-code"
                    inputMode={useRecovery ? 'text' : 'numeric'}
                    autoFocus
                  />
                </FormField>
                <Button type="submit" variant="primary" className="btn-block" disabled={submitting}>
                  {submitting ? 'Verifying…' : 'Verify'}
                </Button>
                <div className="row">
                  <Button
                    type="button"
                    onClick={() => {
                      setUseRecovery(!useRecovery);
                      setCode('');
                      setError(null);
                    }}
                  >
                    {useRecovery ? 'Use an authenticator code' : 'Use a recovery code'}
                  </Button>
                  <span className="sp" />
                  <Button type="button" onClick={backToPassword}>
                    Back
                  </Button>
                </div>
              </>
            ) : (
              <>
                <FormField label="Username or email">
                  <TextInput
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    autoComplete="username"
                    autoFocus
                  />
                </FormField>
                <FormField label="Password">
                  <PasswordInput
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    autoComplete="current-password"
                  />
                </FormField>
                <Button type="submit" variant="primary" className="btn-block" disabled={submitting}>
                  {submitting ? 'Signing in…' : 'Sign in'}
                </Button>
              </>
            )}
          </form>
          <p className="small muted login-foot">
            Coding agent or CLI? Create an <Link to="/settings/tokens">access token</Link> and run{' '}
            <span className="mono">gs auth login</span>.
          </p>
        </div>
      </div>
    </main>
  );
}
