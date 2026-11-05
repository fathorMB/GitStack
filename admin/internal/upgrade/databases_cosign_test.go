package upgrade

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// GIT-551 (DB5): core verifica con cosign KEYLESS le immagini PostgreSQL con identita' e issuer che il chart
// gli passa (databases.cosign in values.yaml). La fonte unica e' V5Config: se i default del chart divergono
// da quelli di `gitstack upgrade` (IdentityRegexp e issuer) questo test fallisce, cosi' le due verifiche
// accettano sempre la stessa firma e nessuna regex e' ricopiata a mano.
func TestDatabasesCosignDelChartCoincideConLaVerificaDiUpgrade(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "gitstack", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Databases struct {
			Cosign struct {
				IdentityRegexp string `yaml:"identityRegexp"`
				Issuer         string `yaml:"issuer"`
			} `yaml:"cosign"`
		} `yaml:"databases"`
	}
	if err := yaml.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	src := V5Config{}
	if got, want := v.Databases.Cosign.IdentityRegexp, src.IdentityRegexp(); got != want {
		t.Errorf("databases.cosign.identityRegexp = %q, V5Config.IdentityRegexp() = %q", got, want)
	}
	if got, want := v.Databases.Cosign.Issuer, src.issuer(); got != want {
		t.Errorf("databases.cosign.issuer = %q, V5Config.issuer() = %q", got, want)
	}
}
