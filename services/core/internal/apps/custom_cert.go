package apps

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Certificato del cliente per un dominio proprio (v3-c, GIT-460, ADR 0002). Qui sta la sola validazione:
// i messaggi d'errore dicono cosa non va e non citano mai la chiave privata (nemmeno i suoi byte).

// ErrCertificateInvalid: il certificato o la chiave non passano il controllo; il messaggio è per l'utente.
var ErrCertificateInvalid = errors.New("certificato non valido")

// CertCheckOptions sono i punti d'ingresso dei test.
type CertCheckOptions struct {
	// Roots: radici di fiducia per chiudere la catena; nil usa quelle di sistema. Una radice
	// autofirmata presente nella catena caricata chiude comunque la catena.
	Roots *x509.CertPool
	Now   time.Time
}

// CustomCertInfo è quanto serve sapere di un certificato valido.
type CustomCertInfo struct {
	ChainPEM   string // catena normalizzata, foglia per prima
	KeyPEM     string // chiave normalizzata (PKCS#8)
	ValidFrom  time.Time
	ValidUntil time.Time
	Issuer     string
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrCertificateInvalid, fmt.Sprintf(format, a...))
}

// ValidateCustomCertificate controlla il certificato caricato per il dominio: catena in PEM, copertura
// del nome (esatto o wildcard di un livello), validità temporale di ogni certificato, chiave
// corrispondente alla foglia, catena completa fino a una radice nota o autofirmata.
func ValidateCustomCertificate(domain, chainPEM, keyPEM string, opt CertCheckOptions) (CustomCertInfo, error) {
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	certs, err := parseCertChain(chainPEM)
	if err != nil {
		return CustomCertInfo{}, err
	}
	leaf := certs[0]
	if err := leaf.VerifyHostname(domain); err != nil {
		return CustomCertInfo{}, invalid("il certificato non copre %s (nomi: %s)", domain, certNames(leaf))
	}
	for i, c := range certs {
		switch {
		case now.After(c.NotAfter):
			return CustomCertInfo{}, invalid("%s è scaduto il %s", certLabel(i), c.NotAfter.UTC().Format("2006-01-02"))
		case now.Before(c.NotBefore):
			return CustomCertInfo{}, invalid("%s non è ancora valido (dal %s)", certLabel(i), c.NotBefore.UTC().Format("2006-01-02"))
		}
	}
	keyDER, err := parsePrivateKey(keyPEM)
	if err != nil {
		return CustomCertInfo{}, err
	}
	// tls.X509KeyPair confronta la chiave pubblica della foglia con quella derivata dalla privata.
	var chainBlocks []byte
	for _, c := range certs {
		chainBlocks = append(chainBlocks, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	keyBlock := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if _, err := tls.X509KeyPair(chainBlocks, keyBlock); err != nil {
		return CustomCertInfo{}, invalid("la chiave privata non corrisponde al certificato")
	}
	if err := verifyChain(certs, opt.Roots, now); err != nil {
		return CustomCertInfo{}, err
	}
	issuer := leaf.Issuer.CommonName
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}
	return CustomCertInfo{
		ChainPEM: string(chainBlocks), KeyPEM: string(keyBlock),
		ValidFrom: leaf.NotBefore.UTC(), ValidUntil: leaf.NotAfter.UTC(), Issuer: issuer,
	}, nil
}

func certLabel(i int) string {
	if i == 0 {
		return "il certificato"
	}
	return fmt.Sprintf("il certificato intermedio %d della catena", i)
}

func certNames(c *x509.Certificate) string {
	names := append([]string(nil), c.DNSNames...)
	if len(names) == 0 && c.Subject.CommonName != "" {
		names = append(names, c.Subject.CommonName)
	}
	if len(names) == 0 {
		return "nessuno"
	}
	if len(names) > 5 {
		names = append(names[:5], "…")
	}
	return strings.Join(names, ", ")
}

func parseCertChain(in string) ([]*x509.Certificate, error) {
	rest := []byte(in)
	var certs []*x509.Certificate
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		if b.Type != "CERTIFICATE" {
			return nil, invalid("il campo certificate deve contenere solo certificati PEM: la chiave privata va in privateKey")
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return nil, invalid("un certificato della catena non si legge")
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return nil, invalid("nessun certificato PEM nel campo certificate")
	}
	if len(strings.TrimSpace(string(rest))) > 0 {
		return nil, invalid("il campo certificate ha dati che non sono PEM")
	}
	return certs, nil
}

// parsePrivateKey accetta PKCS#8, PKCS#1 ed EC, senza passphrase; ritorna il DER PKCS#8.
func parsePrivateKey(in string) ([]byte, error) {
	b, _ := pem.Decode([]byte(in))
	if b == nil {
		return nil, invalid("la chiave privata non è in formato PEM")
	}
	if strings.Contains(b.Type, "ENCRYPTED") || strings.Contains(b.Headers["Proc-Type"], "ENCRYPTED") {
		return nil, invalid("la chiave privata ha una passphrase: caricala senza")
	}
	var key any
	var err error
	switch b.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(b.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(b.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(b.Bytes)
	default:
		return nil, invalid("tipo di chiave non supportato (%q): servono PKCS#8, PKCS#1 o EC", b.Type)
	}
	if err != nil {
		return nil, invalid("la chiave privata non si legge")
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, invalid("la chiave privata non si legge")
	}
	return der, nil
}

// verifyChain controlla che la catena arrivi a una radice: i certificati dopo la foglia sono gli
// intermedi (e, se c'è, la radice autofirmata); senza intermedi mancanti Verify trova un percorso.
func verifyChain(certs []*x509.Certificate, roots *x509.CertPool, now time.Time) error {
	pool := roots
	if pool == nil {
		sys, err := x509.SystemCertPool()
		if err != nil || sys == nil {
			sys = x509.NewCertPool()
		}
		pool = sys
	} else {
		pool = pool.Clone()
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		if isSelfSigned(c) {
			pool.AddCert(c)
		} else {
			inter.AddCert(c)
		}
	}
	if isSelfSigned(certs[0]) && len(certs) == 1 {
		return invalid("il certificato è autofirmato: il cliente deve caricare un certificato emesso da una CA, con la sua catena")
	}
	_, err := certs[0].Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	if err == nil {
		return nil
	}
	var unk x509.UnknownAuthorityError
	if errors.As(err, &unk) {
		return invalid("la catena è incompleta: manca un certificato intermedio o la radice non è riconosciuta; carica la foglia seguita dagli intermedi")
	}
	return invalid("la catena non è valida: %v", sanitizeVerifyErr(err))
}

func isSelfSigned(c *x509.Certificate) bool {
	return string(c.RawIssuer) == string(c.RawSubject) && c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
}

func sanitizeVerifyErr(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
