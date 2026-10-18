import { passwordStrength } from '../lib/passwordStrength';

// Misuratore di robustezza (ASVS V2.1.8) sotto il campo "nuova password".
// Non blocca niente: il server applica la politica.
export function PasswordStrengthMeter({ password }: { password: string }) {
  if (!password) return null;
  const s = passwordStrength(password);
  return (
    <div className="pw-meter" data-score={s.score}>
      <div
        className="pw-meter-bar"
        role="meter"
        aria-label="Password strength"
        aria-valuemin={0}
        aria-valuemax={4}
        aria-valuenow={s.score}
        aria-valuetext={s.label}
      >
        <span style={{ width: `${(s.score + 1) * 20}%` }} />
      </div>
      <div className="small muted" aria-live="polite">
        Strength: <b>{s.label}</b>
        {s.hint ? ` — ${s.hint}` : ''}
      </div>
    </div>
  );
}
