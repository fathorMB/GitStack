package output

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode/utf8"
)

// Table è una tabella di testo a colonne allineate (tabwriter).
type Table struct {
	headers []string
	rows    [][]string
	tty     bool
	width   int
}

// NewTable crea una tabella. Su TTY le celle troppo lunghe si troncano per
// stare nella larghezza (width, 0 = nessun limite); senza TTY mai: l'output
// per macchine non perde dati.
func NewTable(tty bool, width int, headers ...string) *Table {
	return &Table{headers: headers, tty: tty, width: width}
}

// AddRow aggiunge una riga; le celle in meno restano vuote.
func (t *Table) AddRow(cells ...string) {
	t.rows = append(t.rows, cells)
}

// Render scrive la tabella. Su TTY l'intestazione è inclusa, senza TTY no
// (come gh: output per script, una riga per record, colonne separate da tab).
func (t *Table) Render(w io.Writer) error {
	if !t.tty {
		for _, r := range t.rows {
			if _, err := fmt.Fprintln(w, strings.Join(clean(r), "\t")); err != nil {
				return err
			}
		}
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	all := append([][]string{t.headers}, t.rows...)
	ncol := len(t.headers)
	max := t.maxLast(all, ncol)
	for _, r := range all {
		cells := clean(r)
		for len(cells) < ncol {
			cells = append(cells, "")
		}
		if max > 0 && ncol > 0 {
			cells[ncol-1] = truncate(cells[ncol-1], max)
		}
		if _, err := fmt.Fprintln(tw, strings.TrimRight(strings.Join(cells, "\t"), "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// maxLast è la larghezza disponibile per l'ultima colonna: width meno lo
// spazio delle altre (la più larga di ciascuna + 2 di spaziatura).
func (t *Table) maxLast(all [][]string, ncol int) int {
	if t.width <= 0 || ncol == 0 {
		return 0
	}
	used := 0
	for c := 0; c < ncol-1; c++ {
		w := 0
		for _, r := range all {
			if c < len(r) {
				if n := utf8.RuneCountInString(oneLine(r[c])); n > w {
					w = n
				}
			}
		}
		used += w + 2
	}
	if m := t.width - used; m >= 10 {
		return m
	}
	return 10
}

func clean(r []string) []string {
	out := make([]string, len(r))
	for i, c := range r {
		out[i] = oneLine(c)
	}
	return out
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.ReplaceAll(s, "\n", " ")
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}
