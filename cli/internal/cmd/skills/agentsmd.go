package skills

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Delimitatori della sezione gestita da gs in AGENTS.md.
const (
	MarkerStart = "<!-- gitstack-skills:start -->"
	MarkerEnd   = "<!-- gitstack-skills:end -->"
)

// agentsMDSection è il testo della sezione, con EOL "\n", delimitatori compresi.
func agentsMDSection(names []string) string {
	var sb strings.Builder
	sb.WriteString(MarkerStart + "\n")
	sb.WriteString("## GitStack\n\n")
	sb.WriteString("Questo progetto è su GitStack. Per issue, repo e notifiche usa la CLI `gs` (`gs --help`), non l'API a mano.\n")
	sb.WriteString("Le skills in `.claude/skills/` e `.agents/skills/` descrivono i flussi:")
	for i, n := range names {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(" `" + n + "`")
	}
	sb.WriteString(".\n")
	sb.WriteString("Un commit che chiude una issue contiene `fixes #<numero>`. Non usare `--yes` su eliminazioni e archiviazioni senza un'istruzione esplicita di una persona.\n")
	sb.WriteString("Sezione gestita da `gs skills`: non modificarla a mano, `gs skills update --agents-md` la riscrive.\n")
	sb.WriteString(MarkerEnd)
	return sb.String()
}

// eolOf è il fine riga del file: CRLF se la prima interruzione di riga è
// "\r\n", altrimenti LF.
func eolOf(b []byte) string {
	i := bytes.IndexByte(b, '\n')
	if i > 0 && b[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// UpdateAgentsMD aggiunge la sezione delimitata in fondo al file, o sostituisce
// quella che c'è. Quello che sta fuori dai delimitatori resta identico byte per
// byte; il fine riga della sezione è quello del file (LF per un file vuoto).
func UpdateAgentsMD(existing []byte, names []string) ([]byte, error) {
	eol := eolOf(existing)
	section := agentsMDSection(names)
	if eol != "\n" {
		section = strings.ReplaceAll(section, "\n", eol)
	}
	start := bytes.Index(existing, []byte(MarkerStart))
	end := bytes.Index(existing, []byte(MarkerEnd))
	switch {
	case start < 0 && end < 0:
		var out bytes.Buffer
		out.Write(existing)
		if len(existing) > 0 {
			if !bytes.HasSuffix(existing, []byte("\n")) {
				out.WriteString(eol)
			}
			if !bytes.HasSuffix(existing, []byte(eol+eol)) {
				out.WriteString(eol)
			}
		}
		out.WriteString(section)
		out.WriteString(eol)
		return out.Bytes(), nil
	case start < 0 || end < 0 || end < start:
		return nil, errors.New("AGENTS.md ha i delimitatori della sezione di gs incompleti o invertiti: sistemali a mano e rilancia")
	case bytes.Count(existing, []byte(MarkerStart)) > 1 || bytes.Count(existing, []byte(MarkerEnd)) > 1:
		return nil, errors.New("AGENTS.md ha più di una sezione di gs: lasciane una sola e rilancia")
	}
	var out bytes.Buffer
	out.Write(existing[:start])
	out.WriteString(section)
	out.Write(existing[end+len(MarkerEnd):])
	return out.Bytes(), nil
}

// writeAgentsMD aggiorna <root>/AGENTS.md; dà false se il file è già a posto.
func writeAgentsMD(root string, names []string) (string, bool, error) {
	p := filepath.Join(root, "AGENTS.md")
	old, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return p, false, err
	}
	next, err := UpdateAgentsMD(old, names)
	if err != nil {
		return p, false, err
	}
	if bytes.Equal(old, next) {
		return p, false, nil
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	}
	return p, true, os.WriteFile(p, next, mode)
}
