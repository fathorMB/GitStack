package domainevents

// Eventi dei domini propri e dei certificati delle app (v3-c, GIT-461). Sono avvisi per gli admin
// (dell'app e dell'istanza): nel contratto non hanno un webhook, quindi la consegna dei webhook li
// ignora. Ogni evento si scrive una volta sola per stato: lo store lo accoda solo nel passaggio a
// quello stato, nella stessa transazione che lo salva.
const (
	// AppDomainDNSFailed: il dominio non supera la verifica del DNS o ha smesso di puntare all'istanza
	// (parte il periodo di grazia, oppure la grazia è scaduta e il dominio è in errore).
	AppDomainDNSFailed = "app_domain.dns_failed"
	// AppCertificateRenewalFailed: Let's Encrypt non ha emesso o rinnovato il certificato del dominio.
	AppCertificateRenewalFailed = "app_certificate.renewal_failed"
	// AppCertificateExpiring: il certificato caricato dal cliente scade entro CertExpiryNoticeDays giorni.
	AppCertificateExpiring = "app_certificate.expiring"
)

// CertExpiryNoticeDays è l'anticipo dell'avviso di scadenza del certificato del cliente.
const CertExpiryNoticeDays = 14

// Domini degli eventi sopra (primo segmento del nome).
const (
	DomainAppDomain      = "app_domain"
	DomainAppCertificate = "app_certificate"
)

// AppDomainPayload è lo schema interno di app_domain.* e app_certificate.*. Reason è il motivo (DNS o
// emissione); ValidUntil (RFC 3339) è la scadenza del certificato, solo per app_certificate.expiring.
type AppDomainPayload struct {
	Repo       Repo   `json:"repo"`
	AppName    string `json:"appName"`
	Domain     string `json:"domain"`
	Reason     string `json:"reason,omitempty"`
	ValidUntil string `json:"validUntil,omitempty"`
}
