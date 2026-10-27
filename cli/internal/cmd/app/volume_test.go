package app_test

import (
	"strings"
	"testing"
)

// GIT-359 (H3, mockup H8): gs app volume list|add|remove contro un gateway finto.

const (
	volBase = "/repos/acme/web/apps/wiki/volumes"
	volData = `{"name":"data","mountPath":"/wiki/data","sizeBytes":10737418240,"version":3,"createdAt":"2026-10-26T10:00:00Z","includeInBackup":true,"looksLikeDatabase":false}`
	volDb   = `{"name":"db","mountPath":"/var/lib/postgresql/data","sizeBytes":5368709120,"version":4,"createdAt":"2026-10-26T11:00:00Z","includeInBackup":false,"looksLikeDatabase":true}`
	quotaJS = `{"owner":"acme","limits":{"cpuMillis":4000,"memoryBytes":8589934592,"diskBytes":53687091200},"used":{"cpuMillis":500,"memoryBytes":805306368,"diskBytes":16106127360},"warning":false}`
)

func TestVolumeList(t *testing.T) {
	fk := newFake(t, map[string]reply{"GET " + volBase: {200, `{"items":[` + volData + `,` + volDb + `],"quota":` + quotaJS + `}`}})
	r := run(t, fk, "app", "volume", "list", "wiki")
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.errOut)
	}
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "data\t/wiki/data\t10 GB\t3\t") || !strings.HasPrefix(lines[1], "db\t/var/lib/postgresql/data\t5 GB\t4\t") {
		t.Fatalf("righe: %q", r.out)
	}
	r = run(t, fk, "app", "volume", "list", "wiki", "--json", "name,mountPath,sizeBytes")
	if r.code != 0 || !strings.Contains(r.out, `"mountPath"`) || !strings.Contains(r.out, `10737418240`) || strings.Contains(r.out, `"version"`) {
		t.Fatalf("json: %+v", r)
	}
}

func TestVolumeBackup(t *testing.T) {
	fk := newFake(t, map[string]reply{"PATCH " + volBase + "/db": {200, volDb}})
	r := run(t, fk, "app", "volume", "backup", "wiki", "db", "--exclude")
	if r.code != 0 || !strings.Contains(r.out, "escluso dai backup") {
		t.Fatalf("exclude: %+v", r)
	}
	calls := fk.find("PATCH", volBase+"/db")
	if len(calls) != 1 || calls[0].Body["includeInBackup"] != false {
		t.Fatalf("corpo: %+v", calls)
	}
	r = run(t, fk, "app", "volume", "backup", "wiki", "db", "--include")
	if r.code != 0 || !strings.Contains(r.out, "è nei backup") {
		t.Fatalf("include: %+v", r)
	}
	if calls = fk.find("PATCH", volBase+"/db"); len(calls) != 2 || calls[1].Body["includeInBackup"] != true {
		t.Fatalf("corpo include: %+v", calls)
	}
	for _, args := range [][]string{
		{"app", "volume", "backup", "wiki", "db"},
		{"app", "volume", "backup", "wiki", "db", "--include", "--exclude"},
	} {
		if r := run(t, fk, args...); r.code != 2 {
			t.Errorf("%v: exit %d, atteso 2", args, r.code)
		}
	}
}

func TestVolumeAdd(t *testing.T) {
	fk := newFake(t, map[string]reply{"POST " + volBase: {202, `{"volume":` + volData + `,"version":{"number":3},"quota":` + quotaJS + `}`}})
	r := run(t, fk, "app", "volume", "add", "wiki", "data", "--mount", "/wiki/data", "--size", "10GB")
	if r.code != 0 || !strings.Contains(r.out, "aggiunto data") || !strings.Contains(r.out, "versione 3") {
		t.Fatalf("add: %+v", r)
	}
	calls := fk.find("POST", volBase)
	if len(calls) != 1 || calls[0].Body["name"] != "data" || calls[0].Body["mountPath"] != "/wiki/data" || calls[0].Body["sizeBytes"] != float64(10<<30) {
		t.Fatalf("corpo: %+v", calls)
	}
	r = run(t, fk, "app", "volume", "add", "wiki", "data", "--mount", "/wiki/data", "--size", "10GB", "--json", "name,sizeBytes")
	if r.code != 0 || !strings.Contains(r.out, `"name": "data"`) && !strings.Contains(r.out, `"name":"data"`) {
		t.Fatalf("json: %+v", r)
	}
}

func TestVolumeAdd_Usi(t *testing.T) {
	fk := newFake(t, nil)
	for _, args := range [][]string{
		{"app", "volume", "add", "wiki", "data", "--mount", "relativo", "--size", "1GB"},
		{"app", "volume", "add", "wiki", "data", "--mount", "/d", "--size", "10 pere"},
		{"app", "volume", "add", "wiki", "data", "--mount", "/d", "--size", "100KB"},
		{"app", "volume", "add", "wiki", "data", "--size", "1GB"},
	} {
		if r := run(t, fk, args...); r.code != 2 {
			t.Errorf("%v: exit %d, atteso 2 (%s)", args, r.code, r.errOut)
		}
	}
	if n := len(fk.find("POST", volBase)); n != 0 {
		t.Fatalf("un uso errato non deve creare volumi: %d", n)
	}
}

func TestVolumeAdd_QuotaPiena(t *testing.T) {
	fk := newFake(t, map[string]reply{"POST " + volBase: {409, `{"error":{"code":"insufficient_capacity","message":"Manca disco nella quota di acme."}}`}})
	r := run(t, fk, "app", "volume", "add", "wiki", "data", "--mount", "/d", "--size", "100GB")
	if r.code == 0 || !strings.Contains(r.errOut, "disco") {
		t.Fatalf("atteso errore di capacità: %+v", r)
	}
}

func TestVolumeRemove_ConfermaG8(t *testing.T) {
	fk := newFake(t, map[string]reply{"DELETE " + volBase + "/data": {202, `{"volume":` + volData + `,"version":{"number":5},"quota":` + quotaJS + `}`}})
	// Senza terminale e senza --yes: exit 2, nessuna chiamata.
	if r := run(t, fk, "app", "volume", "remove", "wiki", "data"); r.code != 2 {
		t.Fatalf("senza --yes: exit %d", r.code)
	}
	if len(fk.find("DELETE", volBase+"/data")) != 0 {
		t.Fatal("non doveva eliminare")
	}
	// Con terminale: nome sbagliato annulla, nome giusto elimina.
	if r := runWith(t, fk, true, "altro\n", "app", "volume", "rm", "wiki", "data"); r.code == 0 || len(fk.find("DELETE", volBase+"/data")) != 0 {
		t.Fatalf("nome sbagliato: %+v", r)
	}
	if r := runWith(t, fk, true, "data\n", "app", "volume", "rm", "wiki", "data"); r.code != 0 || !strings.Contains(r.out, "eliminato data") {
		t.Fatalf("conferma: %+v", r)
	}
	if r := run(t, fk, "app", "volume", "remove", "wiki", "data", "--yes"); r.code != 0 {
		t.Fatalf("--yes: %+v", r)
	}
	if n := len(fk.find("DELETE", volBase+"/data")); n != 2 {
		t.Fatalf("DELETE: %d", n)
	}
}

func TestVolume_SoloAdmin(t *testing.T) {
	fk := newFake(t, map[string]reply{"DELETE " + volBase + "/data": {403, forbidden}})
	if r := run(t, fk, "app", "volume", "remove", "wiki", "data", "--yes"); r.code != 5 {
		t.Fatalf("exit %d, atteso 5: %s", r.code, r.errOut)
	}
}
