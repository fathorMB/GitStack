package main

// ciExternalListener decide se la porta esterna del ci-gateway (GIT-329) deve partire.
// Parte solo con la CI accesa, l'indirizzo impostato e la porta interna presente: con la CI
// spenta il ci-gateway puo' essere in modalita' AppPullOnly e la porta esterna non deve mai
// esporre il gateway completo (runner.v1, archivio, artefatti). warn e' true quando
// l'indirizzo e' impostato ma viene ignorato perche' la CI e' spenta.
func ciExternalListener(ciEnabled bool, externalAddr string, internalUp bool) (start, warn bool) {
	if externalAddr == "" {
		return false, false
	}
	if !ciEnabled {
		return false, true
	}
	return internalUp, false
}
