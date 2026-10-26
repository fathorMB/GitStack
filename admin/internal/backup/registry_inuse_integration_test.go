//go:build integration

package backup

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/fathorMB/GitStack/admin/internal/pgtest"
)

// TestInUseCmd_SuPostgresReale esegue inUseCmd (la stessa costante del backup, via sh -c nel
// container client come PGExec) su un Postgres vero con le migrazioni di core (GIT-389, GIT-351):
// le versioni «failed» restano fuori, la versione attiva è marcata t e la precedente f.
func TestInUseCmd_SuPostgresReale(t *testing.T) {
	s := pgtest.Start(t, 15)
	s.WithClient(t, 15)
	gsPw := seedExternal(t, s)

	const (
		repoID = "11111111-2222-4333-8444-555555555555"
		ref    = "registry.test/alice/web"
	)
	sql := `
INSERT INTO core.resources (id, type, name) VALUES ('` + repoID + `', 'repo', 'alice/web');
INSERT INTO core.repositories (resource_id, owner_type, owner_id, name, owner_name)
  VALUES ('` + repoID + `', 'user', '99999999-2222-4333-8444-555555555555', 'web', 'alice');
INSERT INTO core.apps (id, repo_id, name, active_version, next_version, guaranteed_cpu_millis, guaranteed_memory_bytes,
                       limit_cpu_millis, limit_memory_bytes, created_at, updated_at) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', '` + repoID + `', 'a-failed', 3, 4, 100, 1000, 100, 1000, now(), now()),
  ('aaaaaaaa-0000-4000-8000-000000000002', '` + repoID + `', 'b-normale', 2, 3, 100, 1000, 100, 1000, now(), now()),
  ('aaaaaaaa-0000-4000-8000-000000000003', '` + repoID + `', 'c-senza-versioni', 1, 2, 100, 1000, 100, 1000, now(), now()),
  ('aaaaaaaa-0000-4000-8000-000000000004', '` + repoID + `', 'd-attiva-assente', 7, 8, 100, 1000, 100, 1000, now(), now());
INSERT INTO core.app_versions (app_id, number, trigger, state, image_reference, image_source, config, created_at) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', 1, 'create', 'superseded', '` + ref + `:a1', 'registry', '{}', now()),
  ('aaaaaaaa-0000-4000-8000-000000000001', 2, 'manual', 'failed', '` + ref + `:a2', 'registry', '{}', now()),
  ('aaaaaaaa-0000-4000-8000-000000000001', 3, 'manual', 'serving', '` + ref + `:a3', 'registry', '{}', now()),
  ('aaaaaaaa-0000-4000-8000-000000000002', 1, 'create', 'superseded', '` + ref + `:b1', 'registry', '{}', now()),
  ('aaaaaaaa-0000-4000-8000-000000000002', 2, 'manual', 'serving', '` + ref + `:b2', 'registry', '{}', now()),
  ('aaaaaaaa-0000-4000-8000-000000000004', 1, 'create', 'superseded', '` + ref + `:d1', 'registry', '{}', now());
`
	s.SQL(t, "gitstack", gsPw, "gitstack", nil, sql)

	var out bytes.Buffer
	if err := s.Exec(context.Background(), pgtest.ClientEnv("gitstack", gsPw, "gitstack"), nil, &out, "sh", "-c", inUseCmd); err != nil {
		t.Fatalf("inUseCmd: %v", err)
	}
	prefix := repoID + "|alice|web|"
	want := []string{
		prefix + "a-failed|" + ref + ":a3||t",  // v2 failed esclusa, v1 fuori finestra
		prefix + "b-normale|" + ref + ":b1||f", // precedente
		prefix + "b-normale|" + ref + ":b2||t", // attiva
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("righe di inUseCmd:\n%s\nattese:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Le righe devono restare leggibili dal parser del backup.
	for _, line := range got {
		if _, err := parseInUseRow(line); err != nil {
			t.Errorf("parseInUseRow(%q): %v", line, err)
		}
	}
}
