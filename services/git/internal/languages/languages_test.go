package languages

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestCompute(t *testing.T) {
	cases := []struct {
		name  string
		files []File
		total int64
		want  []Share
	}{
		{"repo solo Go", []File{{Path: "main.go", Size: 100}, {Path: "a/b.go", Size: 50}, {Path: "README.md", Size: 999}},
			150, []Share{{"Go", 150, 100}}},
		{"repo misto", []File{
			{Path: "a.go", Size: 860}, {Path: "build.sh", Size: 90}, {Path: "Makefile", Size: 50}, {Path: "logo.png", Size: 5000},
		}, 1000, []Share{{"Go", 860, 86}, {"Shell", 90, 9}, {"Makefile", 50, 5}}},
		{"vendor, node_modules e generati esclusi", []File{
			{Path: "main.go", Size: 10},
			{Path: "vendor/x/y.go", Size: 1000}, {Path: "web/node_modules/p/i.js", Size: 1000},
			{Path: "api/api.gen.go", Size: 1000}, {Path: "web/app.min.js", Size: 1000}, {Path: "p/x.pb.go", Size: 1000},
			{Path: "vendorish/z.go", Size: 5},
		}, 15, []Share{{"Go", 15, 100}}},
		{"symlink, sottomoduli, vuoti ed estensioni ignote", []File{
			{Path: "link.go", Mode: "120000", Size: 10}, {Path: "sub", Mode: "160000", Type: "commit", Size: -1},
			{Path: "empty.go", Size: 0}, {Path: "data.json", Size: 10},
		}, 0, []Share{}},
		{"nomi noti e maiuscole", []File{
			{Path: "Dockerfile", Size: 1}, {Path: "svc/Dockerfile", Size: 1}, {Path: "x/MAIN.GO", Size: 2},
		}, 4, []Share{{"Dockerfile", 2, 50}, {"Go", 2, 50}}},
		{"repo vuoto", nil, 0, []Share{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Compute(c.files)
			if got.TotalBytes != c.total || got.Languages == nil || fmt.Sprint(got.Languages) != fmt.Sprint(c.want) {
				t.Fatalf("got %+v, voglio %d %v", got, c.total, c.want)
			}
		})
	}
}

func TestComputePercentSumTo100(t *testing.T) {
	for _, sizes := range [][]int64{{1, 1, 1}, {1, 1, 1, 1, 1, 1, 1}, {7, 13, 29, 31, 1}, {1000003, 3, 2}, {5, 5}} {
		var files []File
		for i, s := range sizes {
			files = append(files, File{Path: fmt.Sprintf("f%d%s", i, Table[i].Extensions[0]), Size: s})
		}
		res := Compute(files)
		var tenths int64
		var prev int64 = 1 << 62
		for _, l := range res.Languages {
			tenths += int64(l.Percent*10 + 0.5)
			if l.Bytes > prev {
				t.Fatalf("ordine non decrescente: %+v", res)
			}
			prev = l.Bytes
		}
		if tenths != 1000 {
			t.Fatalf("%v: somma %d decimi, voglio 1000: %+v", sizes, tenths, res)
		}
	}
	// 3 lingue pari: 33,4 + 33,3 + 33,3 (il decimo extra va al primo per nome).
	res := Compute([]File{{Path: "a.go", Size: 1}, {Path: "b.py", Size: 1}, {Path: "c.rs", Size: 1}})
	if res.Languages[0].Percent != 33.4 || res.Languages[1].Percent != 33.3 || res.Languages[2].Percent != 33.3 {
		t.Fatalf("pari: %+v", res)
	}
}

func TestTable(t *testing.T) {
	if len(Table) < 30 {
		t.Fatalf("lingue in tabella: %d, voglio almeno 30", len(Table))
	}
	names, exts := map[string]bool{}, map[string]string{}
	for _, l := range Table {
		if names[l.Name] || len(l.Color) != 7 || l.Color[0] != '#' {
			t.Errorf("riga non valida: %+v", l)
		}
		names[l.Name] = true
		for _, e := range l.Extensions {
			if e != strings.ToLower(e) || e[0] != '.' || exts[e] != "" {
				t.Errorf("estensione %q (%s) non valida o doppia (%s)", e, l.Name, exts[e])
			}
			exts[e] = l.Name
		}
	}
	if Color("Go") != "#00ADD8" || Color("Boh") != "" {
		t.Fatal("Color")
	}
}

// Il README deve riportare ogni lingua con il suo colore e le sue regole.
func TestREADMEDocumentsTable(t *testing.T) {
	b, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		_ = line
	}
	doc := string(b)
	for _, l := range Table {
		row := "| " + l.Name + " | `" + l.Color + "` |"
		if !strings.Contains(doc, row) {
			t.Errorf("README senza la riga %q", row)
		}
		for _, e := range l.Extensions {
			if !strings.Contains(doc, "`"+e+"`") {
				t.Errorf("README senza l'estensione %s di %s", e, l.Name)
			}
		}
		for _, n := range l.Filenames {
			if !strings.Contains(doc, "`"+n+"`") {
				t.Errorf("README senza il nome %s di %s", n, l.Name)
			}
		}
	}
}

func TestCache(t *testing.T) {
	c := NewCache()
	if _, ok := c.Get("a"); ok {
		t.Fatal("cache non vuota")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < CacheMax*2; j++ {
				sha := fmt.Sprint(j)
				c.Put(sha, Result{TotalBytes: int64(j)})
				c.Get(sha)
			}
		}()
	}
	wg.Wait()
	if len(c.m) > CacheMax || len(c.m) != len(c.order) {
		t.Fatalf("cache: %d voci, %d in ordine", len(c.m), len(c.order))
	}
	c.Put("x", Result{TotalBytes: 7})
	if r, ok := c.Get("x"); !ok || r.TotalBytes != 7 {
		t.Fatal("get dopo put")
	}
}
