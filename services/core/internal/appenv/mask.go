// Package appenv: regole delle variabili e dei segreti delle app (v3-a, GIT-348, H8) che non stanno
// nello store né nelle API. Qui solo il mascheramento dei log: i valori dei segreti dell'app
// (decifrati da store.AppSecretValues) diventano «***» in ogni riga di log, con lo stesso masker dei
// segreti della CI. Le variabili non si mascherano: le legge chi ha write.
package appenv

import "github.com/fathorMB/GitStack/services/core/internal/ci"

// Masker costruisce il masker dei valori dei segreti di un'app.
func Masker(secrets map[string]string) *ci.Masker {
	values := make([]string, 0, len(secrets))
	for _, v := range secrets {
		values = append(values, v)
	}
	return ci.NewMasker(values...)
}
