package issue

// SetOpenURL sostituisce l'apertura del browser nei test; restituisce il
// ripristino.
func SetOpenURL(fn func(string) error) func() {
	old := openURL
	openURL = fn
	return func() { openURL = old }
}

// ParseCloseReason espone parseCloseReason ai test.
var ParseCloseReason = parseCloseReason
