package gitread

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fathorMB/GitStack/services/git/internal/gitref"
)

func searchRepo(t *testing.T) *fixture {
	f := newFixture(t)
	f.write("README.md", []byte("# Demo\nUna riga con Ciao Mondo\n"))
	f.write("src/main.go", []byte("package main\n\nfunc main() { println(\"hello\") }\n"))
	f.write("src/util/x.txt", []byte("--help e -e sono testo\na.*b non è una regex\n100% [x] (y) $HOME \\d\n"))
	f.write("bin.dat", []byte("hello\x00binario\n"))
	f.write("big.txt", append([]byte("hello grande\n"), bytes.Repeat([]byte("z"), SearchFileMaxBytes)...))
	f.write("latin1.txt", []byte{'c', 'a', 'f', 0xe9, ' ', 'h', 'e', 'l', 'l', 'o', '\n'})
	f.write("sp ace/nl;x.txt", []byte("hello in un percorso strano\n"))
	f.commit(alice, "Primo")
	return f
}

func hitsOf(r *CodeSearchResult) []string {
	var out []string
	for _, h := range r.Results {
		out = append(out, fmt.Sprintf("%s:%d", h.Path, h.Line))
	}
	return out
}

func TestFiles(t *testing.T) {
	f := searchRepo(t)
	ctx := context.Background()
	res, err := f.svc.Files(ctx, "r1", "main")
	if err != nil {
		t.Fatal(err)
	}
	want := "README.md,big.txt,bin.dat,latin1.txt,sp ace/nl;x.txt,src/main.go,src/util/x.txt"
	if strings.Join(res.Paths, ",") != want || res.Truncated || res.Ref != "main" || len(res.CommitSHA) != 40 {
		t.Fatalf("files: %+v", res)
	}
	if _, err := f.svc.Files(ctx, "r1", "nope"); !errors.Is(err, gitref.ErrRefNotFound) {
		t.Fatalf("ref inesistente: %v", err)
	}
	if _, err := f.svc.Files(ctx, "r1", "a..b"); !errors.Is(err, gitref.ErrInvalidRef) {
		t.Fatalf("ref non valido: %v", err)
	}
}

func TestFiles_EmptyRepo(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.Files(context.Background(), "r1", "main")
	if err != nil || res.Paths == nil || len(res.Paths) != 0 || res.Truncated {
		t.Fatalf("repo vuoto: %+v %v", res, err)
	}
	sr, err := f.svc.SearchCode(context.Background(), "r1", "main", "hello")
	if err != nil || sr.Results == nil || len(sr.Results) != 0 {
		t.Fatalf("ricerca in repo vuoto: %+v %v", sr, err)
	}
}

func TestSearchCode_FoundAndNot(t *testing.T) {
	f := searchRepo(t)
	ctx := context.Background()
	res, err := f.svc.SearchCode(ctx, "r1", "main", "HELLO")
	if err != nil {
		t.Fatal(err)
	}
	// Binari e file oltre 1 MB esclusi; maiuscole ignorate; Latin-1 ok.
	got := strings.Join(hitsOf(res), ",")
	if got != "latin1.txt:1,sp ace/nl;x.txt:1,src/main.go:3" || res.LimitReached || res.TimedOut {
		t.Fatalf("risultati: %s %+v", got, res)
	}
	if res.Results[2].Fragment != `func main() { println("hello") }` || res.Query != "HELLO" || res.Ref != "main" {
		t.Fatalf("frammento: %+v", res)
	}
	if !strings.HasPrefix(res.Results[0].Fragment, "caf") {
		t.Fatalf("latin1: %q", res.Results[0].Fragment)
	}

	none, err := f.svc.SearchCode(ctx, "r1", "main", "inesistente")
	if err != nil || none.Results == nil || len(none.Results) != 0 || none.LimitReached || none.TimedOut {
		t.Fatalf("nessun risultato: %+v %v", none, err)
	}
}

func TestSearchCode_LiteralText(t *testing.T) {
	f := searchRepo(t)
	ctx := context.Background()
	for q, want := range map[string]string{
		"--help":    "src/util/x.txt:1",
		"-e sono":   "src/util/x.txt:1",
		"a.*b":      "src/util/x.txt:2",
		"100% [x]":  "src/util/x.txt:3",
		"(y) $HOME": "src/util/x.txt:3",
		`\d`:        "src/util/x.txt:3",
		"--":        "src/util/x.txt:1", // due trattini, come testo
	} {
		res, err := f.svc.SearchCode(ctx, "r1", "main", q)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if got := strings.Join(hitsOf(res), ","); got != want {
			t.Errorf("%q: %s, atteso %s", q, got, want)
		}
	}
	// Come regex "a.*b" troverebbe anche altro: qui solo la riga con il testo letterale.
	// Caratteri che sarebbero regex non valide non fanno errore.
	for _, q := range []string{"[", "(", "*x", `\`} {
		if _, err := f.svc.SearchCode(ctx, "r1", "main", q+"q"); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
}

func TestSearchCode_InvalidQuery(t *testing.T) {
	f := searchRepo(t)
	long := strings.Repeat("a", 257)
	for _, q := range []string{"", "a", "ab\ncd", "ab\x00cd", long, "\xff\xfe"} {
		if _, err := f.svc.SearchCode(context.Background(), "r1", "main", q); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%q: %v", q, err)
		}
	}
	if _, err := f.svc.SearchCode(context.Background(), "r1", "main", strings.Repeat("a", 256)); err != nil {
		t.Errorf("256 caratteri: %v", err)
	}
	if _, err := f.svc.SearchCode(context.Background(), "r1", "nope", "hello"); !errors.Is(err, gitref.ErrRefNotFound) {
		t.Errorf("ref inesistente: %v", err)
	}
}

func TestSearchCode_LimitReached(t *testing.T) {
	f := newFixture(t)
	f.write("a.txt", bytes.Repeat([]byte("needle\n"), SearchMaxResults))
	f.commit(alice, "cento")
	res, err := f.svc.SearchCode(context.Background(), "r1", "main", "needle")
	if err != nil || len(res.Results) != SearchMaxResults || res.LimitReached {
		t.Fatalf("esattamente 100: %d %v %+v", len(res.Results), err, res.LimitReached)
	}
	f.write("b.txt", []byte("needle\n"))
	f.commit(alice, "centouno")
	res, err = f.svc.SearchCode(context.Background(), "r1", "main", "needle")
	if err != nil || len(res.Results) != SearchMaxResults || !res.LimitReached || res.TimedOut {
		t.Fatalf("oltre 100: %d %v %+v", len(res.Results), err, res.LimitReached)
	}
}

func TestSearchCode_FragmentTrimmed(t *testing.T) {
	f := newFixture(t)
	line := strings.Repeat("x", 1000) + "TROVATO" + strings.Repeat("y", 1000) + "\n"
	f.write("long.txt", []byte(line))
	f.commit(alice, "lunga")
	res, err := f.svc.SearchCode(context.Background(), "r1", "main", "trovato")
	if err != nil || len(res.Results) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	fr := res.Results[0].Fragment
	if len([]rune(fr)) != SearchFragmentMaxChars || !strings.Contains(fr, "TROVATO") {
		t.Fatalf("frammento: %d %q", len(fr), fr)
	}
}

func TestSearchCode_TimeoutInjected(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 300; i++ {
		f.write(fmt.Sprintf("d%d/f%d.txt", i%20, i), bytes.Repeat([]byte("riga di testo\n"), 50))
	}
	f.commit(alice, "molti file")
	f.svc.SetSearchTimeout(time.Nanosecond)
	start := time.Now()
	res, err := f.svc.SearchCode(context.Background(), "r1", "main", "inesistente")
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.Results == nil {
		t.Fatalf("atteso timedOut: %+v", res)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("il timeout non ha interrotto la ricerca: %v", d)
	}
	// Con il tempo normale la stessa ricerca finisce senza timeout.
	f.svc.SetSearchTimeout(0)
	res, err = f.svc.SearchCode(context.Background(), "r1", "main", "riga di")
	if err != nil || res.TimedOut || !res.LimitReached {
		t.Fatalf("senza timeout: %+v %v", res, err)
	}
}

func TestSearchCode_CallerCancel(t *testing.T) {
	f := searchRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.svc.SearchCode(ctx, "r1", "main", "hello"); err == nil {
		t.Fatal("un contesto annullato dal chiamante deve dare errore, non timedOut")
	}
}
