package coreevents_test

import (
	"errors"
	"testing"

	"github.com/fathorMB/GitStack/pkg/events"
	"github.com/fathorMB/GitStack/pkg/events/coreevents"
)

const repoJSON = `"repo":{"id":"r1","fullName":"alice/app","defaultBranch":"main","visibility":"private","archived":false}`
const issueJSON = `"issue":{"id":"i1","number":3,"title":"t","state":"open","authorId":"u1","assigneeIds":[],"hidden":false,"locked":false}`

func TestRegisterDecodificaTuttiGliEventi(t *testing.T) {
	reg := events.NewRegistry()
	coreevents.Register(reg)
	payloads := map[string]string{
		"issue":         `{` + repoJSON + `,"actor":{"id":"u1","username":"alice","type":"human"},` + issueJSON + `}`,
		"issue_comment": `{` + repoJSON + `,"actor":null,` + issueJSON + `,"comment":{"id":"c1","authorId":"u1"}}`,
		"repository":    `{` + repoJSON + `,"actor":null}`,
	}
	groups := map[string][]string{
		"issue": coreevents.IssueNames, "issue_comment": coreevents.IssueCommentNames, "repository": coreevents.RepositoryNames,
	}
	for domain, names := range groups {
		for _, n := range names {
			if events.Domain(n) != domain {
				t.Errorf("%s: dominio %q, voluto %q", n, events.Domain(n), domain)
			}
			if _, err := reg.Decode(events.Envelope{Name: n, Version: coreevents.Version, Payload: []byte(payloads[domain])}); err != nil {
				t.Errorf("%s: %v", n, err)
			}
		}
	}
}

func TestDecodeRifiutaCampiSconosciutiEMancanti(t *testing.T) {
	reg := events.NewRegistry()
	coreevents.Register(reg)
	for _, raw := range []string{`{` + repoJSON + `,"actor":null,"extra":1}`, `{"actor":null}`} {
		if _, err := reg.Decode(events.Envelope{Name: coreevents.RepositoryCreated, Version: 1, Payload: []byte(raw)}); err == nil {
			t.Errorf("payload %s accettato", raw)
		}
	}
	_, err := reg.Decode(events.Envelope{Name: coreevents.IssueCreated, Version: 2, Payload: []byte(`{}`)})
	var ue *events.UnknownSchemaError
	if !errors.As(err, &ue) {
		t.Fatalf("atteso UnknownSchemaError, ottenuto %v", err)
	}
}
