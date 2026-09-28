import { useLocation, useNavigate } from 'react-router-dom';
import { Button } from '../components';
import { useAuth } from '../routes/auth';

// Segnaposto: il login vero (mockup 01) e' GIT-40. Qui solo il punto di
// arrivo dei 401; "Continue" azzera lo stato e torna dove si era.
export function LoginPage() {
  const { clearUnauthorized } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from ?? '/';
  return (
    <main className="login-wrap">
      <div className="card login-card">
        <h1>Sign in to GitStack</h1>
        <p className="muted">Your session is missing or has expired. (Placeholder: the sign-in form arrives with the login screen.)</p>
        <Button
          variant="primary"
          onClick={() => {
            clearUnauthorized();
            navigate(from, { replace: true });
          }}
        >
          Continue
        </Button>
      </div>
    </main>
  );
}
