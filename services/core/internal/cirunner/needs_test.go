package cirunner

import (
	"strings"
	"testing"
)

func testNeeds() map[string]need {
	return map[string]need{
		"build": {Result: "success", Outputs: map[string]string{"digest": "sha256:abc"}},
		"lint":  {Result: "failure", Outputs: map[string]string{}},
	}
}

// TestPrepareJob_SoloLeEspressioni (GIT-501, limiti di GIT-500): il testo letterale needs.build.result
// in un run: fuori da ${{ }} resta com'e'; dentro ${{ }} (anche con la sintassi a indice e con
// needs.*.result) diventa il contesto vero.
func TestPrepareJob_SoloLeEspressioni(t *testing.T) {
	wf := `name: w
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - run: echo build
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: echo lint
  deploy:
    needs: [build, lint]
    if: needs.build.result == 'success'
    runs-on: ubuntu-latest
    steps:
      - run: echo "needs.build.result e needs['build'].result restano testo"
      - if: ${{ contains(needs.*.result, 'failure') }}
        run: echo "fallito ${{ needs['build'].outputs['digest'] }} e ${{ github.needs }} 'needs.x'"
`
	out, names, err := prepareJob([]byte(wf), "deploy", testNeeds())
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Errorf("nomi degli outputs inattesi: %v", names)
	}
	got := string(out)
	for _, want := range []string{
		`echo "needs.build.result e needs['build'].result restano testo"`, // run: invariato
		`if: ${{ contains(fromJSON(`,                                      // contains(needs.*.result, ..)
		`).*.result, 'failure') }}`,
		`${{ fromJSON(`,
		`)['build'].outputs['digest'] }}`,
		`github.needs }}`, // proprieta' di un altro contesto, non il contesto needs
		`'needs.x'"`,      // stringa 'literal' dentro l'espressione... fuori da ${{ }} qui e' testo
		`"digest":"sha256:abc"`,
		`"lint":{"result":"failure","outputs":{}}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("manca %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "needs:") {
		t.Errorf("needs doveva essere tolto:\n%s", got)
	}
	if !strings.Contains(got, "if: fromJSON(") {
		t.Errorf("if: senza ${{ }} doveva essere riscritto:\n%s", got)
	}
}

// TestPrepareJob_StringaNellEspressione: needs dentro una stringa 'literal' o dopo un punto non si tocca.
func TestPrepareJob_StringaNellEspressione(t *testing.T) {
	wf := "jobs:\n  a:\n    runs-on: x\n    steps:\n      - run: echo\n  b:\n    needs: a\n    runs-on: x\n    steps:\n      - run: echo ${{ format('needs {0}', github.needs) }} ${{ 'a}}b' }}\n"
	out, _, err := prepareJob([]byte(wf), "b", map[string]need{"a": {Result: "success"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "format('needs {0}', github.needs) }} ${{ 'a}}b' }}") {
		t.Errorf("riscritto troppo:\n%s", out)
	}
}

// TestPrepareJob_NeedsAssenteDalTask: un needs dichiarato che il gateway non manda vale 'success'.
func TestPrepareJob_NeedsAssenteDalTask(t *testing.T) {
	wf := "jobs:\n  a:\n    runs-on: x\n    steps:\n      - run: echo\n  b:\n    needs: a\n    if: needs.a.result == 'success'\n    runs-on: x\n    steps:\n      - run: echo\n"
	out, _, err := prepareJob([]byte(wf), "b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"a":{"result":"success","outputs":{}}`) {
		t.Errorf("manca il needs di default:\n%s", out)
	}
}

// TestPrepareJob_PassoDegliOutputs: il job con outputs riceve il passo di raccolta in coda; un job
// senza outputs e senza needs resta com'e'.
func TestPrepareJob_PassoDegliOutputs(t *testing.T) {
	wf := "jobs:\n  a:\n    runs-on: x\n    outputs:\n      d: ${{ steps.s.outputs.d }}\n      e: fisso\n    steps:\n      - id: s\n        run: echo\n  b:\n    runs-on: x\n    steps:\n      - run: echo\n"
	out, names, err := prepareJob([]byte(wf), "a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "d" || names[1] != "e" {
		t.Errorf("nomi: %v", names)
	}
	got := string(out)
	for _, want := range []string{OutputsStepName, "if: always()", "continue-on-error: true", "GITSTACK_OUT_0: ${{ steps.s.outputs.d }}", "GITSTACK_OUT_1: fisso"} {
		if !strings.Contains(got, want) {
			t.Errorf("manca %q in:\n%s", want, got)
		}
	}
	same, names, err := prepareJob([]byte(wf), "b", nil)
	if err != nil || names != nil || string(same) != wf {
		t.Errorf("il job senza outputs doveva restare invariato: %v %v\n%s", err, names, same)
	}
}
