// Package output è il livello comune di uscita di gs (G3): tabelle di testo,
// --json con selezione dei campi, --jq ed errori JSON su stderr.
package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"

	"github.com/itchyny/gojq"
	"github.com/spf13/cobra"

	"github.com/fathorMB/GitStack/cli/internal/cmdutil"
)

// JSONOptions sono i valori di --json e --jq.
type JSONOptions struct {
	// Fields sono i campi richiesti con --json (in ordine).
	Fields []string
	// JQ è l'espressione di --jq.
	JQ string

	available []string
	flagSet   bool
}

// Enabled è vero se l'output va in JSON (--json o --jq).
func (o *JSONOptions) Enabled() bool { return o.flagSet || o.JQ != "" }

// Available sono i campi ammessi per --json.
func (o *JSONOptions) Available() []string { return o.available }

// AddJSONFlags aggiunge --json e --jq a cmd.
//
//   - `--json a,b` stampa solo i campi richiesti; un campo ignoto è un uso errato (exit 2).
//   - `--json` senza campi elenca i campi disponibili su stdout ed esce 0, come gh.
//   - `--jq expr` filtra l'output JSON con gojq; implica --json con tutti i campi.
func AddJSONFlags(cmd *cobra.Command, opts *JSONOptions, available []string) {
	opts.available = append([]string(nil), available...)
	sort.Strings(opts.available)
	cmd.Flags().StringSliceVar(&opts.Fields, "json", nil, "Output JSON con i campi indicati (senza campi: elenca quelli disponibili)")
	cmd.Flags().StringVarP(&opts.JQ, "jq", "q", "", "Filtra l'output JSON con un'espressione jq")

	prevPre := cmd.PreRunE
	cmd.PreRunE = func(c *cobra.Command, args []string) error {
		opts.flagSet = c.Flags().Changed("json")
		if opts.flagSet && len(opts.Fields) == 0 {
			return opts.listFields(c.OutOrStdout())
		}
		if opts.JQ != "" && !opts.flagSet {
			// --jq da solo: tutti i campi.
			opts.Fields = nil
		}
		if err := opts.validate(); err != nil {
			return err
		}
		if prevPre != nil {
			return prevPre(c, args)
		}
		return nil
	}

	// `--json` come ultimo argomento: pflag protesta che manca il valore.
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		if err != nil && strings.Contains(err.Error(), "flag needs an argument: --json") {
			opts.flagSet = true
			return opts.listFields(c.OutOrStdout())
		}
		return &cmdutil.UsageError{Msg: err.Error()}
	})
}

func (o *JSONOptions) listFields(w io.Writer) error {
	_, _ = fmt.Fprintln(w, "Campi disponibili per --json:")
	for _, f := range o.available {
		_, _ = fmt.Fprintf(w, "  %s\n", f)
	}
	return cmdutil.ErrSilent
}

func (o *JSONOptions) validate() error {
	if len(o.available) == 0 {
		return nil
	}
	ok := map[string]bool{}
	for _, f := range o.available {
		ok[f] = true
	}
	for _, f := range o.Fields {
		if !ok[f] {
			return cmdutil.UsageErrorf("campo JSON sconosciuto %q; disponibili: %s", f, strings.Join(o.available, ", "))
		}
	}
	return nil
}

// Write stampa data (qualunque valore serializzabile in JSON) secondo le
// opzioni: selezione dei campi, poi --jq. Con TTY il JSON è indentato, senza
// è compatto, una riga per documento.
func (o *JSONOptions) Write(w io.Writer, data any, tty bool) error {
	v, err := normalize(data)
	if err != nil {
		return err
	}
	if len(o.Fields) > 0 {
		v = pick(v, o.Fields)
	}
	if o.JQ == "" {
		return writeJSON(w, v, tty)
	}
	q, err := gojq.Parse(o.JQ)
	if err != nil {
		return cmdutil.UsageErrorf("espressione --jq non valida: %v", err)
	}
	code, err := gojq.Compile(q)
	if err != nil {
		return cmdutil.UsageErrorf("espressione --jq non valida: %v", err)
	}
	iter := code.Run(v)
	for {
		r, ok := iter.Next()
		if !ok {
			return nil
		}
		if err, isErr := r.(error); isErr {
			var halt *gojq.HaltError
			if errors.As(err, &halt) && halt.Value() == nil {
				return nil
			}
			return fmt.Errorf("jq: %w", err)
		}
		if s, isStr := r.(string); isStr {
			if _, err := fmt.Fprintln(w, s); err != nil {
				return err
			}
			continue
		}
		if err := writeJSON(w, r, tty); err != nil {
			return err
		}
	}
}

func writeJSON(w io.Writer, v any, tty bool) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if tty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// normalize porta qualunque valore al modello di gojq: map[string]any, []any,
// string, bool, nil, int o float64.
func normalize(data any) (any, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return fixNumbers(v), nil
}

func fixNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i)
		}
		if bi, ok := new(big.Int).SetString(x.String(), 10); ok {
			return bi
		}
		f, _ := x.Float64()
		return f
	case []any:
		for i := range x {
			x[i] = fixNumbers(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = fixNumbers(x[k])
		}
		return x
	}
	return v
}

// pick tiene solo i campi richiesti: su un oggetto, su ogni elemento di una lista.
func pick(v any, fields []string) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = pick(x[i], fields)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(fields))
		for _, f := range fields {
			if val, ok := x[f]; ok {
				out[f] = val
			} else {
				out[f] = nil
			}
		}
		return out
	}
	return v
}

// jsonError è il corpo dell'errore JSON su stderr: la stessa forma del
// formato errore unico dell'API.
type jsonError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// WriteError scrive err su w come {"error":{"code","message"}} (una riga).
func WriteError(w io.Writer, err error) {
	var je jsonError
	je.Error.Code = cmdutil.ErrorCode(err)
	je.Error.Message = err.Error()
	b, _ := json.Marshal(je)
	_, _ = fmt.Fprintf(w, "%s\n", b)
}
