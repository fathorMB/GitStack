//go:build integration

package httpserver_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
)

// GIT-571: un allegato di release da 5 MiB (oltre il tetto JSON da 1 MiB) passa
// da limitBody e arriva all'handler, sia con l'utente sia con il token del job.
func TestReleases_AssetOltre1MiB(t *testing.T) {
	e := newReleaseEnv(t)
	e.jobFixture("app", "gsj_grande", `{"contents":"write","packages":"read","actions":"read"}`, time.Now().Add(time.Hour))

	rec := e.release(http.MethodPost, "/repos/alice/app/releases", "alice", `{"tagName":"v1.0.0","name":"Grande"}`)
	e.want(rec, http.StatusCreated)
	var rel openapi.Release
	if err := json.Unmarshal(rec.Body.Bytes(), &rel); err != nil {
		t.Fatal(err)
	}

	data := make([]byte, 5<<20)
	for i := range data {
		data[i] = byte(i * 7)
	}
	sum := sha256.Sum256(data)
	want := hex.EncodeToString(sum[:])

	path := func(name string) string {
		return "/repos/alice/app/releases/" + rel.Id.String() + "/assets?name=" + url.QueryEscape(name)
	}
	for name, rec := range map[string]interface{ Result() *http.Response }{
		"utente": e.upload(rel.Id, "utente.bin", data),
		"job":    e.asJob(http.MethodPost, path("job.bin"), "gsj_grande", "", data),
	} {
		res := rec.Result()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("%s: status %d", name, res.StatusCode)
		}
		var a openapi.ReleaseAsset
		if err := json.NewDecoder(res.Body).Decode(&a); err != nil || a.Sha256 != want || a.SizeBytes != int64(len(data)) {
			t.Fatalf("%s: asset %+v err %v (sha atteso %s)", name, a, err, want)
		}
	}

	// POST /releases in JSON resta a 1 MiB.
	big := `{"tagName":"v1.1.0","name":"` + string(make([]byte, 0)) + `","body":"` + string(repeat('a', 1<<20+10)) + `"}`
	e.want(e.release(http.MethodPost, "/repos/alice/app/releases", "alice", big), http.StatusRequestEntityTooLarge)
}

func repeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
