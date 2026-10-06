// Host raggiunti in locale: i browser li trattano come contesto sicuro anche
// su http, e li usano le prove (k3d in CI, sviluppo). Non fanno scattare l'avviso.
function isLoopback(hostname: string): boolean {
  return hostname === 'localhost' || hostname === '[::1]' || hostname === '::1' || /^127\./.test(hostname);
}

// true se la pagina e' servita in chiaro su un host non locale: capita solo con
// `install.sh --insecure-http` (di default Traefik reindirizza 80 a 443, N5).
export function isInsecureHttp(loc: Pick<Location, 'protocol' | 'hostname'> = window.location): boolean {
  return loc.protocol === 'http:' && !isLoopback(loc.hostname);
}
