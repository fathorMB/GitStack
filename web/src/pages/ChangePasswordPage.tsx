import { useEffect, useState } from 'react';
import type { FormEvent } from 'react';
import { Navigate, useLocation, useNavigate } from 'react-router-dom';
import { Button, ErrorAlert, FormField, Logo, PasswordInput } from '../components';
import { changeOwnPassword, fetchSession, signOut } from '../lib/authApi';
import { describeError, fieldErrors } from '../lib/http';
import { useAuth } from '../routes/auth';

// Cambio password obbligatorio (stile della schermata login, data-screen="login":
// non c'e' un mockup dedicato). Fuori da AppShell: finche' la password iniziale
// non e' cambiata la UI non mostra altro. Consentiti dal contratto: questa
// chiamata, GET /auth/session e il logout.
export function ChangePasswordPage() {
  const { unauthorized, clearUnauthorized, setMustChangePassword } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from ?? '/';

  const [username, setUsername] = useState<string | null>(null);
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<string, string>>({});

  useEffect(() => {
    let cancelled = false;
    fetchSession().then(
      (s) => {
        if (!cancelled) setUsername(s.user.username);
      },
      (err: unknown) => {
        if (!cancelled) setError(describeError(err));
      },
    );
    return () => {
      cancelled = true;
    };
  }, []);

  if (unauthorized) {
    return <Navigate to="/login" replace state={{ from }} />;
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!username) return;
    setError(null);
    setFields({});
    if (!current || !next) {
      setError('Enter your current password and a new one.');
      return;
    }
    if (next !== confirm) {
      setFields({ confirm: 'The two passwords do not match.' });
      return;
    }
    setSubmitting(true);
    try {
      await changeOwnPassword(username, current, next);
      // Aggiorna la sessione: da qui in poi le chiamate non rispondono piu' 403.
      await fetchSession();
      setMustChangePassword(false);
      navigate(from, { replace: true });
    } catch (err) {
      const byField = fieldErrors(err);
      setFields(byField);
      setError(Object.keys(byField).length > 0 ? null : describeError(err));
      setSubmitting(false);
    }
  }

  async function handleLogout() {
    try {
      await signOut();
    } catch {
      // comunque si torna al login: se il cookie e' ancora valido lo si vedra' li'.
    }
    setMustChangePassword(false);
    clearUnauthorized();
    navigate('/login', { replace: true });
  }

  return (
    <main className="login">
      <div className="login-l">
        <div className="row login-brand">
          <Logo size={28} className="login-logo" />
          GitStack
        </div>
        <div>
          <h2>Choose a new password.</h2>
          <p>Your account was created with a temporary password. Set your own to continue.</p>
        </div>
        <div className="small login-host">{window.location.host}</div>
        <Logo size={420} className="big" />
      </div>
      <div className="login-r">
        <div className="login-card">
          <h1>Change your password</h1>
          <p className="muted login-lead">You must change it before using GitStack.</p>
          <form className="stack login-form" onSubmit={(e) => void handleSubmit(e)} noValidate>
            {error ? <ErrorAlert message={error} /> : null}
            <FormField label="Current password" error={fields.currentPassword}>
              <PasswordInput
                value={current}
                onChange={(e) => setCurrent(e.target.value)}
                autoComplete="current-password"
                autoFocus
              />
            </FormField>
            <FormField label="New password" hint="At least 12 characters." error={fields.newPassword}>
              <PasswordInput value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" />
            </FormField>
            <FormField label="Confirm new password" error={fields.confirm}>
              <PasswordInput
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                autoComplete="new-password"
              />
            </FormField>
            <Button type="submit" variant="primary" className="btn-block" disabled={submitting || !username}>
              {submitting ? 'Saving…' : 'Change password'}
            </Button>
          </form>
          <p className="small muted login-foot">
            <button type="button" className="btn btn-ghost" onClick={() => void handleLogout()}>
              Sign out
            </button>
          </p>
        </div>
      </div>
    </main>
  );
}
