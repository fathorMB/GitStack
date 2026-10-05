// Package languages calcola la ripartizione per lingua di un ref (pannello
// About, mockup 07) dall'elenco dei file di `git ls-tree -r -l`.
//
// Un file conta solo se la sua lingua è nella Table, riconosciuta dal nome
// esatto (Makefile, Dockerfile...) o dall'estensione. I binari non hanno
// lingua (immagini, archivi, .dat...) e quindi non entrano mai; dati e prosa
// (JSON, YAML, Markdown, testo) non sono in tabella per scelta. Sono esclusi
// anche symlink e sottomoduli, le cartelle vendor e node_modules a qualsiasi
// profondità e i file generati riconoscibili (IsGenerated).
package languages

import (
	"path"
	"sort"
	"strings"
	"sync"
)

// File è una riga di `git ls-tree -r -l`: percorso, modo, tipo e dimensione.
// Type vuoto vale blob.
type File struct {
	Path string
	Mode string
	Type string
	Size int64
}

// Share è la quota di una lingua. Percent ha un decimale.
type Share struct {
	Name    string  `json:"name"`
	Bytes   int64   `json:"bytes"`
	Percent float64 `json:"percent"`
}

// Result è la ripartizione: lingue per byte decrescenti (a parità, per nome)
// e totale dei byte contati. Languages non è mai nil.
type Result struct {
	Languages  []Share `json:"languages"`
	TotalBytes int64   `json:"totalBytes"`
}

var (
	byExt  = map[string]string{}
	byName = map[string]string{}
	colors = map[string]string{}
)

func init() {
	for _, l := range Table {
		colors[l.Name] = l.Color
		for _, e := range l.Extensions {
			byExt[e] = l.Name
		}
		for _, n := range l.Filenames {
			byName[n] = l.Name
		}
	}
}

// Color è il colore della lingua, "" se non è in tabella.
func Color(name string) string { return colors[name] }

// Detect torna la lingua di un percorso, "" se non ne ha.
func Detect(p string) string {
	base := path.Base(p)
	if n, ok := byName[base]; ok {
		return n
	}
	return byExt[strings.ToLower(path.Ext(base))]
}

var generatedSuffixes = []string{
	".gen.go", ".pb.go", ".pb.gw.go", "_generated.go", ".min.js", ".min.css", ".bundle.js", ".d.ts",
}

// IsGenerated dice se il file è generato in modo riconoscibile dal nome.
func IsGenerated(p string) bool {
	b := strings.ToLower(path.Base(p))
	for _, s := range generatedSuffixes {
		if strings.HasSuffix(b, s) {
			return true
		}
	}
	return false
}

func isVendored(p string) bool {
	dirs := strings.Split(p, "/")
	for _, d := range dirs[:len(dirs)-1] {
		if d == "vendor" || d == "node_modules" {
			return true
		}
	}
	return false
}

// Compute somma i byte per lingua e calcola le percentuali.
//
// Arrotondamento: le percentuali sono in decimi di punto col metodo del resto
// più grande: si parte dalla parte intera di bytes*1000/total e i decimi che
// mancano a 1000 vanno, uno ciascuno, alle lingue col resto più alto (a
// parità, quella con più byte). La somma è quindi sempre 100,0.
func Compute(files []File) Result {
	sums := map[string]int64{}
	var total int64
	for _, f := range files {
		if (f.Type != "" && f.Type != "blob") || f.Mode == "120000" || f.Size <= 0 {
			continue
		}
		if isVendored(f.Path) || IsGenerated(f.Path) {
			continue
		}
		name := Detect(f.Path)
		if name == "" {
			continue
		}
		sums[name] += f.Size
		total += f.Size
	}
	res := Result{Languages: []Share{}, TotalBytes: total}
	if total == 0 {
		return res
	}
	type row struct {
		Share
		tenths, rem int64
	}
	rows := make([]row, 0, len(sums))
	var assigned int64
	for n, b := range sums {
		t := b * 1000 / total
		rows = append(rows, row{Share: Share{Name: n, Bytes: b}, tenths: t, rem: b * 1000 % total})
		assigned += t
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Bytes != rows[j].Bytes {
			return rows[i].Bytes > rows[j].Bytes
		}
		return rows[i].Name < rows[j].Name
	})
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return rows[order[a]].rem > rows[order[b]].rem })
	for k := int64(0); k < 1000-assigned; k++ {
		rows[order[k]].tenths++
	}
	for _, r := range rows {
		r.Percent = float64(r.tenths) / 10
		res.Languages = append(res.Languages, r.Share)
	}
	return res
}

// CacheMax è il numero massimo di commit in cache.
const CacheMax = 256

// Cache tiene i risultati per sha del commit, che non cambia mai. Sicura per
// uso concorrente; a CacheMax voci scarta la più vecchia.
type Cache struct {
	mu    sync.Mutex
	m     map[string]Result
	order []string
}

// NewCache crea una cache vuota.
func NewCache() *Cache { return &Cache{m: map[string]Result{}} }

// Get torna il risultato di sha, se c'è.
func (c *Cache) Get(sha string) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.m[sha]
	return r, ok
}

// Put memorizza il risultato di sha.
func (c *Cache) Put(sha string, r Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[sha]; !ok {
		if len(c.order) >= CacheMax {
			delete(c.m, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, sha)
	}
	c.m[sha] = r
}
