import { ShieldAlert } from 'lucide-react';
import { isInsecureHttp } from '../lib/insecureHttp';

// Avviso visibile (GIT-143, N5): la connessione non e' cifrata, password e token
// viaggiano in chiaro. Ruolo "status" e non "alert": non e' un errore dell'API.
export function InsecureHttpBanner() {
  if (!isInsecureHttp()) return null;
  return (
    <div className="alert alert-warning insecure-http-banner" role="status" data-testid="insecure-http-banner">
      <ShieldAlert size={16} aria-hidden="true" />
      <span>
        Connessione non protetta (HTTP): password e token viaggiano in chiaro. Usa questa modalità solo per prove locali;
        reinstalla senza <code>--insecure-http</code> per attivare HTTPS.
      </span>
    </div>
  );
}
