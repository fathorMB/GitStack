package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

type issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	State  string   `json:"state"`
	Labels []string `json:"labels"`
}

var sample = []issue{
	{1, "Prima", "open", []string{"bug"}},
	{2, "Seconda", "closed", nil},
}

// run costruisce un comando con --json/--jq che stampa sample, lo esegue e
// restituisce stdout e l'errore.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var jo JSONOptions
	var out bytes.Buffer
	cmd := &cobra.Command{
		Use:           "list",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, _ []string) error {
			if !jo.Enabled() {
				return errors.New("non in modalità JSON")
			}
			return jo.Write(&out, sample, false)
		},
	}
	AddJSONFlags(cmd, &jo, []string{"title", "number", "state", "labels"})
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestJSONSelezioneCampi(t *testing.T) {
	out, err := run(t, "--json", "number,title")
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if len(got) != 2 || len(got[0]) != 2 || got[0]["title"] != "Prima" || got[0]["number"] != float64(1) {
		t.Errorf("campi non selezionati: %v", got)
	}
	if strings.Contains(out, "state") {
		t.Errorf("campo non richiesto presente: %s", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("senza TTY il JSON è una riga: %q", out)
	}
}

func TestJSONCampoSeparatoDaSpazio(t *testing.T) {
	out, err := run(t, "--json", "state")
	if err != nil || !strings.Contains(out, `"state":"open"`) {
		t.Fatalf("%q %v", out, err)
	}
}

func TestJSONSenzaCampiElencaDisponibili(t *testing.T) {
	out, err := run(t, "--json")
	if !errors.Is(err, cmdutil.ErrSilent) || cmdutil.ExitCode(err) != 0 {
		t.Fatalf("atteso exit 0 (ErrSilent), ho %v", err)
	}
	for _, f := range []string{"labels", "number", "state", "title"} {
		if !strings.Contains(out, "  "+f+"\n") {
			t.Errorf("campo %q non elencato: %q", f, out)
		}
	}
	if strings.Index(out, "labels") > strings.Index(out, "title") {
		t.Errorf("campi non in ordine alfabetico: %q", out)
	}
}

func TestJSONCampoSconosciuto(t *testing.T) {
	_, err := run(t, "--json", "nope")
	if cmdutil.ExitCode(err) != cmdutil.ExitUsage || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("atteso uso errato, ho %v", err)
	}
}

func TestJQ(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--json", "number,title", "--jq", ".[].title"}, "Prima\nSeconda\n"},
		{[]string{"--jq", "map(select(.state==\"open\")) | length"}, "1\n"},
		{[]string{"--jq", ".[0]"}, `{"labels":["bug"],"number":1,"state":"open","title":"Prima"}` + "\n"},
		{[]string{"--json", "number", "-q", "map(.number) | add"}, "3\n"},
		{[]string{"--jq", ".[1].labels"}, "null\n"},
	}
	for _, c := range cases {
		out, err := run(t, c.args...)
		if err != nil {
			t.Errorf("%v: %v", c.args, err)
			continue
		}
		if out != c.want {
			t.Errorf("%v: %q, atteso %q", c.args, out, c.want)
		}
	}
}

func TestJQErrori(t *testing.T) {
	if _, err := run(t, "--jq", ".[[["); cmdutil.ExitCode(err) != cmdutil.ExitUsage {
		t.Errorf("espressione non valida deve essere uso errato: %v", err)
	}
	if _, err := run(t, "--jq", ".[0] | keys | .[0] | tonumber"); err == nil || cmdutil.ExitCode(err) != cmdutil.ExitGeneric {
		t.Errorf("errore a runtime: %v", err)
	}
}

func TestJSONIndentatoSuTTY(t *testing.T) {
	var jo JSONOptions
	var out bytes.Buffer
	if err := jo.Write(&out, map[string]int{"a": 1}, true); err != nil {
		t.Fatal(err)
	}
	if out.String() != "{\n  \"a\": 1\n}\n" {
		t.Errorf("%q", out.String())
	}
}

func TestInteriGrandiNonPerdonoPrecisione(t *testing.T) {
	var jo JSONOptions
	var out bytes.Buffer
	if err := jo.Write(&out, map[string]any{"n": json.Number("12345678901234567890")}, false); err != nil {
		t.Fatal(err)
	}
	if out.String() != "{\"n\":12345678901234567890}\n" {
		t.Errorf("%q", out.String())
	}
}

func TestErroreJSONSuStderr(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{&cmdutil.APIError{Status: 403, Code: "insufficient_scope", Message: "serve lo scope repo:write"}, "insufficient_scope"},
		{&cmdutil.APIError{Status: 404, Message: "repo non trovato"}, "not_found"},
		{cmdutil.UsageErrorf("flag"), "usage_error"},
		{errors.New("boom"), "error"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		WriteError(&buf, c.err)
		var got struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("%v: %q", err, buf.String())
		}
		if got.Error.Code != c.code || got.Error.Message == "" {
			t.Errorf("%v: %+v", c.err, got)
		}
		if strings.Count(buf.String(), "\n") != 1 {
			t.Errorf("una riga attesa: %q", buf.String())
		}
	}
}

func TestTabellaNonTTY(t *testing.T) {
	tb := NewTable(false, 0, "N", "TITOLO")
	tb.AddRow("1", "Una\ttitolo\nlungo")
	tb.AddRow("22")
	var out bytes.Buffer
	if err := tb.Render(&out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "1\tUna titolo lungo\n22\n" {
		t.Errorf("%q", out.String())
	}
}

func TestTabellaTTYAllineaETroncaSoloLUltima(t *testing.T) {
	tb := NewTable(true, 30, "N", "TITOLO")
	tb.AddRow("1", strings.Repeat("x", 80))
	tb.AddRow("22", "breve")
	var out bytes.Buffer
	if err := tb.Render(&out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "N   TITOLO") {
		t.Fatalf("intestazione/allineamento: %q", out.String())
	}
	if !strings.HasSuffix(lines[1], "…") || len([]rune(lines[1])) > 30 {
		t.Errorf("riga non troncata a 30: %q (%d)", lines[1], len([]rune(lines[1])))
	}
	if !strings.HasSuffix(lines[2], "breve") {
		t.Errorf("%q", lines[2])
	}
	// Senza limite di larghezza non si tronca.
	tb2 := NewTable(true, 0, "N", "T")
	tb2.AddRow("1", strings.Repeat("y", 200))
	out.Reset()
	_ = tb2.Render(&out)
	if !strings.Contains(out.String(), strings.Repeat("y", 200)) {
		t.Error("troncato senza larghezza")
	}
}
