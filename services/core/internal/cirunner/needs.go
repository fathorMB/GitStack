package cirunner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"gopkg.in/yaml.v3"
)

// OutputsStepName è il nome del passo che il runner aggiunge in coda al job per leggere gli
// outputs dichiarati (jobs.<id>.outputs): act non li stampa e non li espone fuori dal processo.
// Il passo non compare nel log né fra i passi riportati.
const OutputsStepName = "GitStack: raccolta degli outputs"

// outputsDirEnv è la variabile con la cartella in cui il passo scrive un file per output.
const outputsDirEnv = "GITSTACK_OUTPUTS_DIR"

// need è una voce del contesto needs di un job: l'esito e gli outputs del job a monte.
type need struct {
	Result  string            `json:"result"`
	Outputs map[string]string `json:"outputs"`
}

// needsFromTask ricava il contesto needs dal task (campo needs di runner.v1).
func needsFromTask(t *runnerv1.Task) map[string]need {
	out := map[string]need{}
	for k, n := range t.GetNeeds() {
		o := n.GetOutputs()
		if o == nil {
			o = map[string]string{}
		}
		out[k] = need{Result: resultText(n.GetResult()), Outputs: o}
	}
	return out
}

func resultText(r runnerv1.Result) string {
	switch r {
	case runnerv1.Result_RESULT_FAILURE:
		return "failure"
	case runnerv1.Result_RESULT_CANCELLED:
		return "cancelled"
	case runnerv1.Result_RESULT_SKIPPED:
		return "skipped"
	}
	// Un gateway che non manda l'esito (o un esito sconosciuto): il server avvia il job solo
	// dopo la fine dei suoi needs, e di norma con successo.
	return "success"
}

// prepareJob prepara il workflow per `act -j <job>` (GIT-499, GIT-500, GIT-501):
//   - toglie `needs` dal job: act -j pianifica anche i job a monte e li riesegue dentro il pod
//     (il build girava due volte, con un altro digest). Qui ogni job e' un pod a se': i job a
//     monte sono gia' conclusi sul server, che avvia questo solo dopo;
//   - al posto del contesto needs (che senza `needs` in act e' vuoto) mette nelle espressioni
//     il contesto vero arrivato col task, fromJSON('{...}'): needs.<x>.result, needs['x'].result,
//     needs.<x>.outputs.<y>, needs.*.result e contains(needs.*.result, ...) hanno il valore
//     giusto. Si riscrivono solo le espressioni (`${{ }}` e il valore di `if:`), mai il testo
//     di un `run:`;
//   - se il job dichiara `outputs`, aggiunge in coda un passo (if: always()) che li valuta e li
//     scrive in file; il runner li legge a fine esecuzione e li riporta (outputNames: l'ordine
//     dei file).
//
// Un workflow illeggibile o senza il job resta com'e': act dara' il suo errore.
func prepareJob(workflow []byte, jobKey string, ctx map[string]need) (out []byte, outputNames []string, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(workflow, &doc); err != nil || len(doc.Content) == 0 {
		return workflow, nil, nil
	}
	job := mapValue(mapValue(doc.Content[0], "jobs"), jobKey)
	if job == nil || job.Kind != yaml.MappingNode {
		return workflow, nil, nil
	}
	changed := false
	var declared []string
	for i := 0; i+1 < len(job.Content); i += 2 {
		if job.Content[i].Value == "needs" {
			declared = needNames(job.Content[i+1])
			job.Content = append(job.Content[:i], job.Content[i+2:]...)
			changed = true
			break
		}
	}
	if changed {
		// Un needs dichiarato ma assente dal task (gateway che non li manda): e' concluso con
		// successo, come nel comportamento di GIT-500.
		full := map[string]need{}
		for k, v := range ctx {
			full[k] = v
		}
		for _, k := range declared {
			if _, ok := full[k]; !ok {
				full[k] = need{Result: "success", Outputs: map[string]string{}}
			}
		}
		lit, err := newNeedsLit(full)
		if err != nil {
			return nil, nil, err
		}
		rewriteNeeds(job, lit, true)
	}
	if names, step := outputsStep(job); step != nil {
		steps := mapValue(job, "steps")
		if steps != nil && steps.Kind == yaml.SequenceNode {
			steps.Content = append(steps.Content, step)
			outputNames = names
			changed = true
		}
	}
	if !changed {
		return workflow, nil, nil
	}
	b, err := yaml.Marshal(&doc)
	return b, outputNames, err
}

// needNames sono i job elencati da `needs:` (una stringa o una lista).
func needNames(n *yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			if c.Kind == yaml.ScalarNode {
				out = append(out, c.Value)
			}
		}
		return out
	}
	return nil
}

// outputsStep compone il passo di raccolta: per ogni output `jobs.<id>.outputs.<nome>` una
// variabile d'ambiente con l'espressione originale (la valuta act, nel contesto dei passi) e un
// file in $GITSTACK_OUTPUTS_DIR con il valore. I nomi stanno nel Go, non nello script: niente da
// citare nella shell.
func outputsStep(job *yaml.Node) ([]string, *yaml.Node) {
	outs := mapValue(job, "outputs")
	if outs == nil || outs.Kind != yaml.MappingNode || len(outs.Content) == 0 {
		return nil, nil
	}
	str := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
	env := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	var names, script []string
	for i := 0; i+1 < len(outs.Content); i += 2 {
		if outs.Content[i+1].Kind != yaml.ScalarNode {
			continue
		}
		idx := strconv.Itoa(len(names))
		names = append(names, outs.Content[i].Value)
		env.Content = append(env.Content, str("GITSTACK_OUT_"+idx), str(outs.Content[i+1].Value))
		script = append(script, `printf '%s' "$GITSTACK_OUT_`+idx+`" > "$`+outputsDirEnv+`/`+idx+`"`)
	}
	if len(names) == 0 {
		return nil, nil
	}
	return names, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		str("name"), str(OutputsStepName),
		str("if"), str("always()"),
		str("continue-on-error"), {Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
		str("shell"), str("bash"),
		str("env"), env,
		str("run"), str(strings.Join(script, "\n")),
	}}
}

// readOutputs legge i file scritti dal passo di raccolta. Un file mancante (il job e' caduto
// prima del passo) lascia fuori quell'output.
func readOutputs(dir string, names []string) map[string]string {
	out := map[string]string{}
	for i, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(i)))
		if err != nil {
			continue
		}
		out[name] = string(b)
	}
	return out
}

// needsLit sono le due forme del contesto needs come letterale di un'espressione. act non sa
// filtrare un oggetto con `.*` (needs.*.result darebbe sempre falso) ma sa farlo su un array: dove
// dopo `needs` c'e' `.*` si usa l'array dei valori (in ordine di nome), altrove l'oggetto.
type needsLit struct{ obj, arr string }

func newNeedsLit(ctx map[string]need) (needsLit, error) {
	obj, err := json.Marshal(ctx)
	if err != nil {
		return needsLit{}, err
	}
	keys := make([]string, 0, len(ctx))
	for k := range ctx {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]need, 0, len(keys))
	for _, k := range keys {
		vals = append(vals, ctx[k])
	}
	arr, err := json.Marshal(vals)
	if err != nil {
		return needsLit{}, err
	}
	q := func(b []byte) string { return "fromJSON('" + strings.ReplaceAll(string(b), "'", "''") + "')" }
	return needsLit{obj: q(obj), arr: q(arr)}, nil
}

// followedByWildcard: dopo la posizione i (senza spazi) c'e' `.*`.
func followedByWildcard(e string, i int) bool {
	rest := strings.TrimLeft(e[i:], " 	")
	if !strings.HasPrefix(rest, ".") {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(rest[1:], " 	"), "*")
}

// rewriteNeeds sostituisce l'identificatore `needs` con lit nelle espressioni degli scalari di n.
// ifOK: il nodo e' un job o un passo, dove `if:` e' un'espressione anche senza `${{ }}`.
func rewriteNeeds(n *yaml.Node, lit needsLit, ifOK bool) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			switch {
			case v.Kind == yaml.ScalarNode && ifOK && k.Value == "if" && !strings.Contains(v.Value, "${{"):
				v.Value = rewriteExpr(v.Value, lit)
			case v.Kind == yaml.ScalarNode:
				v.Value = rewriteTemplate(v.Value, lit)
			case k.Value == "steps" && v.Kind == yaml.SequenceNode:
				for _, st := range v.Content {
					rewriteNeeds(st, lit, true)
				}
			default:
				rewriteNeeds(v, lit, false)
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			rewriteNeeds(c, lit, false)
		}
	case yaml.ScalarNode:
		n.Value = rewriteTemplate(n.Value, lit)
	}
}

// rewriteTemplate riscrive le sole espressioni `${{ ... }}` di un testo; il resto (il corpo di
// un run:) resta com'e'. `}}` dentro una stringa 'literal' dell'espressione non la chiude.
func rewriteTemplate(s string, lit needsLit) string {
	if !strings.Contains(s, "${{") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, "${{")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i+3])
		s = s[i+3:]
		end := exprEnd(s)
		if end < 0 { // espressione non chiusa: la lascia act
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(rewriteExpr(s[:end], lit))
		s = s[end:]
	}
}

// exprEnd e' la posizione di `}}` fuori dalle stringhe, -1 se manca.
func exprEnd(s string) int {
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\'':
			i = skipString(s, i) - 1
		case s[i] == '}' && i+1 < len(s) && s[i+1] == '}':
			return i
		}
	}
	return -1
}

// skipString ritorna la posizione dopo la stringa 'literal' che inizia in i (due apici di fila sono un apice).
func skipString(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		if s[j] != '\'' {
			continue
		}
		if j+1 < len(s) && s[j+1] == '\'' {
			j++
			continue
		}
		return j + 1
	}
	return len(s)
}

func isIdent(c byte) bool {
	return c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// rewriteExpr sostituisce con lit ogni identificatore `needs` di un'espressione che non sia una
// proprieta' (github.needs) ne' dentro una stringa. Il resto (.x.result, ['x'].outputs['y'],
// .*.result) lo risolve act sul valore di fromJSON.
func rewriteExpr(e string, lit needsLit) string {
	var b strings.Builder
	for i := 0; i < len(e); {
		c := e[i]
		if c == '\'' {
			j := skipString(e, i)
			b.WriteString(e[i:j])
			i = j
			continue
		}
		if !isIdent(c) {
			b.WriteByte(c)
			i++
			continue
		}
		j := i
		for j < len(e) && isIdent(e[j]) {
			j++
		}
		word := e[i:j]
		if strings.EqualFold(word, "needs") && !afterDot(e, i) {
			if followedByWildcard(e, j) {
				b.WriteString(lit.arr)
			} else {
				b.WriteString(lit.obj)
			}
		} else {
			b.WriteString(word)
		}
		i = j
	}
	return b.String()
}

// afterDot: il carattere non bianco prima di i e' un punto (e' una proprieta').
func afterDot(e string, i int) bool {
	for i--; i >= 0; i-- {
		if e[i] == ' ' || e[i] == '\t' || e[i] == '\n' || e[i] == '\r' {
			continue
		}
		return e[i] == '.'
	}
	return false
}
