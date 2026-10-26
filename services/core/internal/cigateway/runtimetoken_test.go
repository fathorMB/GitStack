package cigateway

import (
	"testing"

	"github.com/fathorMB/GitStack/services/core/internal/cirunner"
)

// Il ci-gateway estrae il token del job dal JWT di ACTIONS_RUNTIME_TOKEN (GIT-393); un token
// semplice o un JWT senza gsj passano com'erano e si verificano come sempre.
func TestRuntimeJobToken(t *testing.T) {
	if got := runtimeJobToken(cirunner.RuntimeToken("gsj_abc")); got != "gsj_abc" {
		t.Errorf("avvolto: %q", got)
	}
	for _, in := range []string{"", "gsj_abc", "a.b.c", "eyJ.@@@.x", "eyJhbGciOiJub25lIn0.e30.x"} {
		if got := runtimeJobToken(in); got != in {
			t.Errorf("runtimeJobToken(%q) = %q", in, got)
		}
	}
}
