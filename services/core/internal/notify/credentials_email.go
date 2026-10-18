package notify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/mailer"
)

// composeCredential scrive l'email di un avviso sulle credenziali (F13). Una
// per evento, con il nome del token o della chiave e cosa fare se non è stato
// l'utente. Nessun segreto: il token non compare mai, solo il suffisso.
func composeCredential(to, base string, group []emailRow) mailer.Message {
	var b strings.Builder
	subject := "GitStack: avviso di sicurezza sul tuo account"
	for _, r := range group {
		var data map[string]any
		_ = json.Unmarshal(r.data, &data)
		str := func(k string) string { v, _ := data[k].(string); return v }
		switch r.eventName {
		case "credential.password_changed":
			subject = "GitStack: la password del tuo account è stata cambiata"
			b.WriteString("La password del tuo account GitStack è stata cambiata.\n")
		case "credential.password_reset":
			subject = "GitStack: la password del tuo account è stata reimpostata"
			by := str("by")
			if by == "" {
				by = "un amministratore"
			}
			fmt.Fprintf(&b, "La password del tuo account GitStack è stata reimpostata da %s.\n", by)
		case "credential.token_created":
			subject = "GitStack: è stato creato un token personale"
			fmt.Fprintf(&b, "È stato creato il token personale «%s» (termina con %s).\n", str("name"), str("hint"))
			if sc, ok := data["scopes"].([]any); ok && len(sc) > 0 {
				names := make([]string, 0, len(sc))
				for _, x := range sc {
					if s, ok := x.(string); ok {
						names = append(names, s)
					}
				}
				b.WriteString("Ambiti: " + strings.Join(names, ", ") + ".\n")
			}
			if e := str("expiresAt"); e != "" {
				b.WriteString("Scade il " + e + ".\n")
			}
		case "credential.token_expiring":
			subject = "GitStack: un tuo token personale sta per scadere"
			fmt.Fprintf(&b, "Il token personale «%s» (termina con %s) scade il %s.\nCreane uno nuovo prima della scadenza, o gli strumenti che lo usano smetteranno di funzionare.\n", str("name"), str("hint"), str("expiresAt"))
		case "credential.ssh_key_added":
			subject = "GitStack: è stata aggiunta una chiave SSH"
			fmt.Fprintf(&b, "È stata aggiunta al tuo account la chiave SSH «%s» (%s).\n", str("title"), str("fingerprint"))
		default:
			b.WriteString("C'è un avviso di sicurezza sul tuo account GitStack.\n")
		}
	}
	if len(group) > 1 {
		subject = "GitStack: avvisi di sicurezza sul tuo account"
	}
	b.WriteString("\nSe non sei stato tu, cambia subito la password, revoca i token e le chiavi che non riconosci dalle impostazioni del profilo e avvisa l'amministratore.\n")
	b.WriteString("Impostazioni: " + base + "/settings\n")
	b.WriteString("\n--\nQuesta è una notifica automatica: non rispondere a questa email, nessuna risposta viene letta.\n")
	return mailer.Message{To: to, Subject: subject, Body: b.String()}
}
