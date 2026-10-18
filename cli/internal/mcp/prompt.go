package mcp

import (
	"fmt"
	"strings"
)

//go:generate go run ../../cmd/promptgen -skills ../../../skills -out prompts_gen.go -pkg mcp

// PromptArgument è un argomento di un prompt.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// Prompt è un prompt MCP (M4), generato da una skill di skills/ (prompts_gen.go).
type Prompt struct {
	Name        string
	Title       string
	Skill       string // cartella della skill di origine
	Description string
	Arguments   []PromptArgument
	Body        string // corpo di SKILL.md, senza frontmatter
}

// promptPreamble introduce il corpo della skill: i comandi `gs` sono quelli della
// skill, e con questo server gli stessi passi hanno uno strumento dedicato.
const promptPreamble = "Segui il flusso qui sotto. Gli esempi usano la CLI `gs`; con il server MCP di GitStack " +
	"puoi fare le stesse chiamate con gli strumenti dedicati (issue_*, pr_*, review_*, code_*) o con api_request. " +
	"Il server non chiede conferme: le azioni distruttive chiedile a una persona."

// render compone il testo del prompt con gli argomenti ricevuti.
func (p Prompt) render(args map[string]string) (string, error) {
	var ctx []string
	for _, a := range p.Arguments {
		v := strings.TrimSpace(args[a.Name])
		if v == "" {
			if a.Required {
				return "", fmt.Errorf("manca l'argomento %q", a.Name)
			}
			continue
		}
		ctx = append(ctx, fmt.Sprintf("- %s: %s", a.Name, v))
	}
	known := map[string]bool{}
	for _, a := range p.Arguments {
		known[a.Name] = true
	}
	for k := range args {
		if !known[k] {
			return "", fmt.Errorf("argomento sconosciuto %q", k)
		}
	}
	var b strings.Builder
	b.WriteString(promptPreamble)
	if len(ctx) > 0 {
		b.WriteString("\n\nContesto:\n" + strings.Join(ctx, "\n"))
	}
	b.WriteString("\n\n" + p.Body + "\n")
	return b.String(), nil
}
