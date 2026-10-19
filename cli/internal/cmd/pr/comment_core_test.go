package pr_test

import (
	"strings"
	"testing"
)

// GIT-300: core non risponde più 501 ai commenti generali delle PR. Le
// risposte qui sotto sono quelle vere di core (corpo catturato dal test
// d'integrazione TestPullGeneralComments_CreaELeggi): un commento generale non
// ha threadId, path, side, line né commitSha.
const coreGeneralComment = `{"author":{"id":"cccccccc-0000-0000-0000-000000000003","kind":"human","username":"carol"},"body":"una domanda generale","createdAt":"2026-10-19T14:34:17.641417+02:00","deleted":false,"edited":false,"id":"e4c52535-3d97-4552-b3c7-b53d5233520e","outdated":false,"updatedAt":"2026-10-19T14:34:17.641417+02:00"}`

func TestCommentSuRispostaRealeDiCore(t *testing.T) {
	routes := map[string]handler{"POST " + pr7 + "/comments": fixed(201, coreGeneralComment)}
	r := run(t, routes, nil, "pr", "comment", "7", "--body", "una domanda generale")
	if r.code != 0 || !strings.HasSuffix(strings.TrimSpace(r.out), "/acme/web/pulls/7") || len(r.calls) != 1 {
		t.Fatalf("comment: %+v", r)
	}
	// corpo inviato: solo il testo, nessun path/inReplyTo (commento generale)
	b := bodyOf(t, r.calls[0])
	if len(b) != 1 || b["body"] != "una domanda generale" {
		t.Errorf("corpo inviato: %v", b)
	}
	j := run(t, routes, nil, "pr", "comment", "7", "-b", "x", "--json", "id,body,author,createdAt")
	if j.code != 0 || !strings.Contains(j.out, `"id":"e4c52535-3d97-4552-b3c7-b53d5233520e"`) || !strings.Contains(j.out, `"body":"una domanda generale"`) {
		t.Errorf("json: %+v", j)
	}
	// i vecchi 501 non sono più la risposta attesa: se il server li desse, il comando fallisce senza stampare l'indirizzo
	old := map[string]handler{"POST " + pr7 + "/comments": fixed(501, `{"error":{"code":"not_implemented","message":"non ancora"}}`)}
	if x := run(t, old, nil, "pr", "comment", "7", "-b", "x"); x.code == 0 || strings.Contains(x.out, "pulls/7") {
		t.Errorf("501: %+v", x)
	}
}

func TestCloseConCommentoSuRispostaRealeDiCore(t *testing.T) {
	routes := map[string]handler{
		"POST " + pr7 + "/close":    fixed(200, prJSON(7, "x", "closed", false)),
		"POST " + pr7 + "/comments": fixed(201, coreGeneralComment),
	}
	r := run(t, routes, nil, "pr", "close", "7", "--yes", "--comment", "Sostituita dalla #9", "--json", "state")
	if r.code != 0 || len(r.calls) != 2 || r.calls[0].path != pr7+"/close" || r.calls[1].path != pr7+"/comments" {
		t.Fatalf("close --comment: %+v", r)
	}
	if b := bodyOf(t, r.calls[1]); len(b) != 1 || b["body"] != "Sostituita dalla #9" {
		t.Errorf("corpo del commento: %v", b)
	}
	if !strings.Contains(r.out, `"state":"closed"`) {
		t.Errorf("json: %s", r.out)
	}
	// repo archiviato fra chiusura e commento: la PR è chiusa, il commento no, e il messaggio lo dice
	arch := map[string]handler{
		"POST " + pr7 + "/close":    fixed(200, prJSON(7, "x", "closed", false)),
		"POST " + pr7 + "/comments": fixed(409, `{"error":{"code":"archived","message":"repo archiviato"}}`),
	}
	x := run(t, arch, nil, "pr", "close", "7", "-y", "-c", "ciao")
	if x.code == 0 || !strings.Contains(x.errOut, "chiusa, ma il commento non è stato scritto") {
		t.Errorf("commento rifiutato: %+v", x)
	}
}
