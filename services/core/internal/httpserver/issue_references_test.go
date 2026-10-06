package httpserver

import (
	"testing"
)

func TestExtractReferences(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []issueRef
	}{
		{
			name:  "#n stesso repo",
			input: "Fix #12 nel codice",
			want:  []issueRef{{repoKey: "", number: 12}},
		},
		{
			name:  "owner/repo#n altro repo",
			input: "Vedi acme/api#3 per i dettagli",
			want:  []issueRef{{repoKey: "acme/api", number: 3}},
		},
		{
			name:  "#0 ignorato",
			input: "Problema con #0 e #5",
			want:  []issueRef{{repoKey: "", number: 5}},
		},
		{
			name:  "stesso riferimento due volte una sola volta",
			input: "Riferimenti #1 e anche #1",
			want:  []issueRef{{repoKey: "", number: 1}},
		},
		{
			name:  "codice inline ignorato",
			input: "Vedi `#12` nel codice e #13 nel testo",
			want:  []issueRef{{repoKey: "", number: 13}},
		},
		{
			name:  "blocco di codice fence ignorato",
			input: "```\n#99\n```\nMa #10 è nel testo",
			want:  []issueRef{{repoKey: "", number: 10}},
		},
		{
			name:  "mix di stessi e diversi repo",
			input: "#5 e acme/api#3 e acme/web#1",
			want: []issueRef{
				{repoKey: "", number: 5},
				{repoKey: "acme/api", number: 3},
				{repoKey: "acme/web", number: 1},
			},
		},
		{
			name:  "nessun riferimento",
			input: "Solo testo senza numeri",
			want:  nil,
		},
		{
			name:  "fence senza chiusura",
			input: "#1 e ```senza fine #99",
			want:  []issueRef{{repoKey: "", number: 1}},
		},
		{
			name:  "fence con inline dentro",
			input: "```go\n`#5`\n```\n#6 nel testo",
			want:  []issueRef{{repoKey: "", number: 6}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractReferences(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("extractReferences(%q) = %d references, voluto %d: %+v", tt.input, len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i].repoKey != tt.want[i].repoKey || got[i].number != tt.want[i].number {
					t.Errorf("extractReferences(%q)[%d] = %+v, voluto %+v", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestRemoveCodeBlocks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "fence rimosso",
			input: "prima ```\ncodice\n``` dopo",
			want:  "prima  dopo",
		},
		{
			name:  "inline rimosso",
			input: "prima `codice` dopo",
			want:  "prima  dopo",
		},
		{
			name:  "multipli fence",
			input: "```a``` e ```b```",
			want:  " e ",
		},
		{
			name:  "senza codice",
			input: "solo testo",
			want:  "solo testo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removeCodeBlocks(tt.input)
			if got != tt.want {
				t.Errorf("removeCodeBlocks(%q) = %q, voluto %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseInt64(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"123", 123},
		{"0", 0},
		{"abc", 0},
		{"-1", 0},
		{"999999999999999999", 999999999999999999},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseInt64(tt.input)
			if got != tt.want {
				t.Errorf("parseInt64(%q) = %d, voluto %d", tt.input, got, tt.want)
			}
		})
	}
}
