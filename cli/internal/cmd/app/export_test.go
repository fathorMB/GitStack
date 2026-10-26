package app

import "time"

// SetPollInterval accorcia l'attesa di `--wait` nei test e restituisce il ripristino.
func SetPollInterval(d time.Duration) func() {
	old := pollInterval
	pollInterval = d
	return func() { pollInterval = old }
}
