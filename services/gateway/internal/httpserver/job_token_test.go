package httpserver

import (
	"net/http"
	"sort"
	"testing"

	"github.com/fathorMB/GitStack/services/gateway/internal/security"
	"github.com/fathorMB/GitStack/services/gateway/internal/trust"
)

const jobRel = "00000000-0000-0000-0000-0000000000aa"

// GIT-372: il token del job (gsj_) è ammesso solo dalle operazioni con
// x-job-token, cioè createRelease e uploadReleaseAsset, e da nient'altro.
func TestJobToken_SoloCreateEUploadDelleRelease(t *testing.T) {
	var got []string
	for _, r := range security.Routes {
		if r.JobToken {
			got = append(got, r.OperationID)
			if r.Public {
				t.Errorf("%s: una rotta pubblica non ha token del job", r.OperationID)
			}
			if r.Method != http.MethodPost {
				t.Errorf("%s: il token del job vale solo per POST, non %s", r.OperationID, r.Method)
			}
		}
	}
	sort.Strings(got)
	if len(got) != 2 || got[0] != "createRelease" || got[1] != "uploadReleaseAsset" {
		t.Fatalf("rotte con x-job-token = %v, volute createRelease e uploadReleaseAsset", got)
	}
}

func TestJobToken_InoltratoACoreComeIdentitaDelJob(t *testing.T) {
	for _, tc := range []struct{ name, method, target string }{
		{"createRelease", "POST", "/v1/repos/alice/app/releases"},
		{"uploadReleaseAsset", "POST", "/v1/repos/alice/app/releases/" + jobRel + "/assets?name=build.zip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &stubVerifier{}
			e := newEnv(t, v)
			req := bearerReq(tc.method, tc.target, "gsj_token-del-job")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(trust.HeaderJobToken, "gsj_finto-del-client")
			if rec := e.do(req); rec.Code != 204 {
				t.Fatalf("status %d, voluto 204 (inoltrato a core)", rec.Code)
			}
			if v.calls != 0 {
				t.Errorf("il gateway non chiede a identity il token del job: %d chiamate", v.calls)
			}
			g := e.core.last()
			if g == nil {
				t.Fatal("core non ha ricevuto la richiesta")
			}
			if g.Header.Get(trust.HeaderJobToken) != "gsj_token-del-job" {
				t.Errorf("token del job a core = %q", g.Header.Get(trust.HeaderJobToken))
			}
			if g.Header.Get(trust.HeaderUsername) != trust.JobUsername || g.Header.Get(trust.HeaderUserID) != "00000000-0000-0000-0000-000000000000" ||
				g.Header.Get(trust.HeaderScopes) != trust.JobScope {
				t.Errorf("identità a core: user=%q id=%q scopes=%q", g.Header.Get(trust.HeaderUsername), g.Header.Get(trust.HeaderUserID), g.Header.Get(trust.HeaderScopes))
			}
			if g.Header.Get("Authorization") != "" {
				t.Error("Authorization è arrivato a core")
			}
		})
	}
}

// «Nient'altro»: ogni altra rotta (anche di release, anche di sola lettura) rifiuta il token del job
// con 403 e non arriva né a core né a identity.
func TestJobToken_AltreRotteRifiutate(t *testing.T) {
	for _, tc := range []struct{ method, target string }{
		{"PATCH", "/v1/repos/alice/app"},
		{"DELETE", "/v1/repos/alice/app"},
		{"POST", "/v1/repos/alice/app/issues"},
		{"GET", "/v1/repos/alice/app/issues"},
		{"GET", "/v1/repos/alice/app/releases"},
		{"GET", "/v1/repos/alice/app/releases/" + jobRel},
		{"PATCH", "/v1/repos/alice/app/releases/" + jobRel},
		{"DELETE", "/v1/repos/alice/app/releases/" + jobRel},
		{"GET", "/v1/repos/alice/app/releases/" + jobRel + "/assets/" + jobRel + "/download"},
		{"POST", "/v1/repos"},
		{"POST", "/v1/user/tokens"},
		{"GET", "/v1/auth/session"},
	} {
		v := &stubVerifier{}
		e := newEnv(t, v)
		req := bearerReq(tc.method, tc.target, "gsj_token-del-job")
		rec := e.do(req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s con token del job = %d, voluto 403", tc.method, tc.target, rec.Code)
		}
		if e.core.count() != 0 || e.identity.count() != 0 || v.calls != 0 {
			t.Errorf("%s %s: la richiesta non doveva uscire dal gateway (core %d, identity %d, verifier %d)", tc.method, tc.target, e.core.count(), e.identity.count(), v.calls)
		}
	}
}

// L'header del token del job non si può passare da fuori: con un token personale non arriva a core.
func TestJobToken_HeaderDelClientNonPassa(t *testing.T) {
	e := newEnv(t, allowAll())
	req := bearerReq("POST", "/v1/repos/alice/app/releases", "gst_tok")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(trust.HeaderJobToken, "gsj_finto")
	if rec := e.do(req); rec.Code != 204 {
		t.Fatalf("status %d", rec.Code)
	}
	if h := e.core.last().Header.Get(trust.HeaderJobToken); h != "" {
		t.Errorf("il token del job del client è arrivato a core: %q", h)
	}
	if u := e.core.last().Header.Get(trust.HeaderUsername); u == trust.JobUsername {
		t.Errorf("un token personale non diventa un job: %q", u)
	}
}
