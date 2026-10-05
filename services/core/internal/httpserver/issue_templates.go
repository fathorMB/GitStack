package httpserver

import (
	"bytes"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/fathorMB/GitStack/services/core/internal/openapi"
	"gopkg.in/yaml.v3"
)

// issueTemplateFrontMatter è la struttura del front matter YAML nei file
// .gitstack/ISSUE_TEMPLATE/*.md. Le chiavi sono facoltative: title, about,
// labels.
type issueTemplateFrontMatter struct {
	Title  *string  `yaml:"title"`
	About  *string  `yaml:"about"`
	Labels []string `yaml:"labels"`
}

// parseIssueTemplate analizza un file markdown di modello issue. Il contenuto
// deve iniziare con "---", seguito da YAML, seguito da "---". Se il file non
// ha front matter, viene interpretato come un modello puro (body = tutto il
// file). Il nome viene estratto dal path del file nel repo.
func parseIssueTemplate(name, _ string, body string) (openapi.IssueTemplate, error) {
	name = strings.TrimSuffix(name, ".md")

	b := []byte(body)

	// Il front matter richiede "---" iniziale.
	if !bytes.HasPrefix(b, []byte("---\n")) && !bytes.HasPrefix(b, []byte("---\r\n")) {
		// Nessun front matter: il file intero è il body.
		return openapi.IssueTemplate{
			Name: name,
			Body: strings.TrimSuffix(body, "\r\n"),
		}, nil
	}

	// Trova il delimitatore di chiusura.
	closeIdx := findFrontMatterClose(b)
	if closeIdx < 0 {
		return openapi.IssueTemplate{}, fmt.Errorf("front matter non chiuso")
	}

	raw := bytes.TrimSpace(b[3:closeIdx])
	var fm issueTemplateFrontMatter
	if err := yaml.Unmarshal(raw, &fm); err != nil {
		return openapi.IssueTemplate{}, fmt.Errorf("yaml non valido: %w", err)
	}

	// yaml.v3 non mappa mai []any in []string, quindi se labels è []any
	// lo convertiamo. yaml.v3 mappa titoli e about correttamente come *string.
	if fm.Labels == nil {
		var rawLabels any
		if err := yaml.Unmarshal(raw, &rawLabels); err == nil {
			switch rt := rawLabels.(type) {
			case map[string]any:
				rawLabelsVal, ok := rt["labels"]
				if !ok {
					// labels non presente nel YAML, nessun problema.
					fm.Labels = nil
				} else if rawLabelsVal == nil {
					// labels: (nessun valore) → nil.
					fm.Labels = nil
				} else if _, ok := rawLabelsVal.(map[string]any); ok {
					// yaml.v3 unmarshala "labels:" vuoto come map[string]any{}:
					// è un errore semantico.
					return openapi.IssueTemplate{}, fmt.Errorf("labels: tipo errato map[string]interface{}, attesa lista di stringhe")
				} else {
					// qualcos'altro di inaspettato → errore.
					return openapi.IssueTemplate{}, fmt.Errorf("labels: tipo errato %T, attesa lista di stringhe", rawLabelsVal)
				}
			case []any:
				// yaml.v3 a volte restituisce []any per liste.
				labels := make([]string, 0, len(rt))
				for _, item := range rt {
					s, ok := item.(string)
					if !ok {
						return openapi.IssueTemplate{}, fmt.Errorf("labels: elemento non stringa %T", item)
					}
					labels = append(labels, s)
				}
				fm.Labels = labels
			}
		}
	}

	tmpl := openapi.IssueTemplate{
		Name:  name,
		Title: fm.Title,
		About: fm.About,
		Body:  strings.TrimSpace(body[closeIdx+3:]),
	}
	if fm.Labels != nil {
		tmpl.Labels = &fm.Labels
	}
	return tmpl, nil
}

// findFrontMatterClose cerca la seconda sequenza "---" sulla propria riga.
// Ritorna l'indice del primo carattere dopo "---\n" o -1 se non trovata.
func findFrontMatterClose(data []byte) int {
	rest := data[3:] // dopo il "---" iniziale
	idx := bytes.Index(rest, []byte("---\n"))
	if idx < 0 {
		idx = bytes.Index(rest, []byte("---\r\n"))
		if idx < 0 {
			return -1
		}
		return 3 + idx + 4 // "---\r\n"
	}
	return 3 + idx + 3 // "---\n"
}

// listIssueTemplatesFromTree elabora un tree response del servizio git e
// restituisce i modelli issue trovati. Per ogni file .md legge il contenuto,
// lo analizza e salta con un avviso quelli malformati. I modelli restanti
// sono ordinati per nome.
func listIssueTemplatesFromTree(templatesDir string, entries []openapi.TreeEntry, readContents func(path string) (string, error)) []openapi.IssueTemplate {
	var result []openapi.IssueTemplate
	for _, entry := range entries {
		if entry.Type != "file" {
			continue
		}
		if !strings.HasSuffix(entry.Name, ".md") {
			continue
		}
		content, err := readContents(entry.Path)
		if err != nil {
			slog.Warn("lettura del modello non riuscita", "repo", templatesDir, "file", entry.Name, "err", err)
			continue
		}
		tmpl, err := parseIssueTemplate(entry.Name, entry.Path, content)
		if err != nil {
			slog.Warn("modello saltato (formato non valido)", "repo", templatesDir, "file", entry.Name, "err", err)
			continue
		}
		result = append(result, tmpl)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}
