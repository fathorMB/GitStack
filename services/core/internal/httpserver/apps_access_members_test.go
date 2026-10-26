package httpserver

import (
	"strings"
	"testing"
)

// GIT-376 (H6): il testo dell'invito segue il mockup H13 (data-screen="testers") con la CA interna; con
// certificato del cliente o senza HTTPS non c'è niente da installare e il passo della guida non compare.
func TestInviteText(t *testing.T) {
	fp := "4F:9A:21:C7:8E:03:B2"
	got := inviteText("sito", "retail/retail-it", "https://git.example.com/help/ca?app=sito.apps.example.com", &fp, "https://sito.apps.example.com")
	want := "Hi, you can now test sito (retail/retail-it).\n\n" +
		"1. Open https://git.example.com/help/ca?app=sito.apps.example.com and follow the steps for your device.\n" +
		"   Your browser may warn you on that page: continue only if the page shows this fingerprint:\n" +
		"   SHA-256 4F:9A:21:C7:8E:03:B2\n" +
		"2. Open https://sito.apps.example.com and sign in with your GitStack account.\n" +
		"3. Found a problem? Use \"Report a problem\" inside the app."
	if got != want {
		t.Fatalf("testo:\n%s\n--- atteso:\n%s", got, want)
	}

	got = inviteText("sito", "retail/retail-it", "https://git.example.com/help/ca?app=sito.apps.example.com", nil, "https://sito.apps.example.com")
	if strings.Contains(got, "SHA-256") || strings.Contains(got, "help/ca") || !strings.HasPrefix(got, "Hi, you can now test sito (retail/retail-it).\n\n1. Open https://sito.apps.example.com and sign in") ||
		!strings.HasSuffix(got, "2. Found a problem? Use \"Report a problem\" inside the app.") {
		t.Fatalf("senza CA da installare: %s", got)
	}
}

func TestNormalizeTeamName(t *testing.T) {
	for in, want := range map[string]string{"Retail/Business": "retail/business", " acme/web ": "acme/web"} {
		if got, ok := normalizeTeamName(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "senza-barra", "/team", "org/", "a/b/c", "a b/c"} {
		if _, ok := normalizeTeamName(bad); ok {
			t.Errorf("%q deve essere rifiutato", bad)
		}
	}
}
