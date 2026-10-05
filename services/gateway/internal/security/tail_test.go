package security

import "testing"

// getRepositoryRawByPath ha `x-path-tail`: ref (anche con `/`) e percorso
// occupano più segmenti, e la dichiarazione di sicurezza resta quella.
func TestLookup_CodaMultiSegmento(t *testing.T) {
	table, err := NewTable(Routes)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/repos/alice/app/raw/main",
		"/repos/alice/app/raw/main/README.md",
		"/repos/alice/app/raw/feat/x/dir/sub/file.txt",
	} {
		m, ok := table.Lookup("GET", p)
		if !ok || m.Route.OperationID != "getRepositoryRawByPath" || m.Route.Public {
			t.Fatalf("Lookup(%s) = %+v %v", p, m, ok)
		}
		if m.Params["owner"] != "alice" || m.Params["repo"] != "app" || m.Params["refAndPath"] == "" {
			t.Errorf("Lookup(%s) params = %v", p, m.Params)
		}
	}
	if m, _ := table.Lookup("GET", "/repos/alice/app/raw/feat/x/a.txt"); m.Params["refAndPath"] != "feat/x/a.txt" {
		t.Errorf("coda = %q", m.Params["refAndPath"])
	}
	// La forma con query e le altre rotte restano al loro posto, e senza coda
	// o con un altro metodo non c'è dichiarazione.
	if m, ok := table.Lookup("GET", "/repos/alice/app/raw"); !ok || m.Route.OperationID != "getRepositoryRaw" {
		t.Errorf("raw con query = %+v %v", m, ok)
	}
	for _, c := range [][2]string{{"GET", "/repos/alice/app/raw/"}, {"POST", "/repos/alice/app/raw/main/a"}, {"GET", "/repos/alice/app/tree/x/y"}} {
		if _, ok := table.Lookup(c[0], c[1]); ok {
			t.Errorf("Lookup(%s %s) ha trovato una dichiarazione", c[0], c[1])
		}
	}
}
