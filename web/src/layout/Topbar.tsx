// Barra superiore (52px), stessa altezza e stile di
// design/mockups-v1/index.html (.topbar): breadcrumb a sinistra, resto
// vuoto per ora (nessuna ricerca/utente finti: identity/auth arrivano in
// una milestone successiva, vedi M-01 scope).
export function Topbar({ crumb }: { crumb: string }) {
  return (
    <header className="topbar">
      <div className="crumb">
        <b>{crumb}</b>
      </div>
      <span className="sp" />
    </header>
  );
}
