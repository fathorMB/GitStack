package sysinfo

import "fmt"

// Preflight di risorse dei database gestiti (v3-d, ADR 0003, GIT-557). Ogni database e' un Postgres a
// parte (StatefulSet con il suo volume) che consuma la stessa riserva delle app (DB3): sul profilo
// minimo di GitStack (N1) non resta spazio per ospitarne. Valori GB decimali, gli stessi di
// deploy/install.sh (DB_MIN_*/DB_REC_*): un controllo di deriva li confronta nei test.
const (
	// MinDatabasesMemoryBytes e' la RAM dell'host sotto la quale i database gestiti non si accendono.
	MinDatabasesMemoryBytes = 12 * gb
	// RecDatabasesMemoryBytes e' quella consigliata (solo avviso).
	RecDatabasesMemoryBytes = 16 * gb
	// MinDatabasesDiskFreeBytes e' lo spazio libero minimo: 20 GB del volume dei dump (databases.storage)
	// piu' margine per i volumi dei database.
	MinDatabasesDiskFreeBytes = 30 * gb
	// RecDatabasesDiskFreeBytes e' quello consigliato (solo avviso).
	RecDatabasesDiskFreeBytes = 100 * gb
)

// DatabasesPreflight confronta le misure dell'host con il profilo dei database gestiti. fails ferma
// l'accensione, warns la lascia passare. Una misura non letta (0) non blocca: non si inventa un guasto.
func DatabasesPreflight(p NodeProbe) (fails, warns []string) {
	if p.MemoryBytes > 0 {
		switch {
		case p.MemoryBytes < MinDatabasesMemoryBytes:
			fails = append(fails, fmt.Sprintf("RAM insufficiente per i database gestiti: %.1f GB, richiesti almeno %d GB (consigliati %d GB)",
				float64(p.MemoryBytes)/float64(gb), MinDatabasesMemoryBytes/gb, RecDatabasesMemoryBytes/gb))
		case p.MemoryBytes < RecDatabasesMemoryBytes:
			warns = append(warns, fmt.Sprintf("RAM sotto il profilo consigliato per i database gestiti: %.1f GB, consigliati %d GB",
				float64(p.MemoryBytes)/float64(gb), RecDatabasesMemoryBytes/gb))
		}
	}
	if p.DiskFreeBytes > 0 {
		path := p.DiskPath
		if path == "" {
			path = "/"
		}
		switch {
		case p.DiskFreeBytes < MinDatabasesDiskFreeBytes:
			fails = append(fails, fmt.Sprintf("disco libero insufficiente per i database gestiti su %s: %.1f GB, richiesti almeno %d GB",
				path, float64(p.DiskFreeBytes)/float64(gb), MinDatabasesDiskFreeBytes/gb))
		case p.DiskFreeBytes < RecDatabasesDiskFreeBytes:
			warns = append(warns, fmt.Sprintf("disco libero sotto il profilo consigliato per i database gestiti su %s: %.1f GB, consigliati %d GB",
				path, float64(p.DiskFreeBytes)/float64(gb), RecDatabasesDiskFreeBytes/gb))
		}
	}
	return fails, warns
}
