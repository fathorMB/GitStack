package languages

// Language è una riga della tabella: nome mostrato, colore per la UI,
// estensioni (minuscole, con il punto) e nomi di file noti (esatti).
type Language struct {
	Name       string
	Color      string
	Extensions []string
	Filenames  []string
}

// Table è la tabella delle lingue riconosciute. Il README del pacchetto la
// riporta con i colori per la UI (TestREADMEDocumentsTable la tiene allineata).
var Table = []Language{
	{Name: "Go", Color: "#00ADD8", Extensions: []string{".go"}},
	{Name: "TypeScript", Color: "#3178C6", Extensions: []string{".ts", ".tsx", ".mts", ".cts"}},
	{Name: "JavaScript", Color: "#F1E05A", Extensions: []string{".js", ".jsx", ".mjs", ".cjs"}},
	{Name: "Python", Color: "#3572A5", Extensions: []string{".py", ".pyi"}},
	{Name: "Java", Color: "#B07219", Extensions: []string{".java"}},
	{Name: "Kotlin", Color: "#A97BFF", Extensions: []string{".kt", ".kts"}},
	{Name: "C", Color: "#555555", Extensions: []string{".c", ".h"}},
	{Name: "C++", Color: "#F34B7D", Extensions: []string{".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx"}},
	{Name: "C#", Color: "#178600", Extensions: []string{".cs"}},
	{Name: "Rust", Color: "#DEA584", Extensions: []string{".rs"}},
	{Name: "Ruby", Color: "#701516", Extensions: []string{".rb", ".rake"}, Filenames: []string{"Rakefile", "Gemfile"}},
	{Name: "PHP", Color: "#4F5D95", Extensions: []string{".php"}},
	{Name: "Swift", Color: "#F05138", Extensions: []string{".swift"}},
	{Name: "Dart", Color: "#00B4AB", Extensions: []string{".dart"}},
	{Name: "Scala", Color: "#C22D40", Extensions: []string{".scala", ".sc"}},
	{Name: "Elixir", Color: "#6E4A7E", Extensions: []string{".ex", ".exs"}},
	{Name: "Haskell", Color: "#5E5086", Extensions: []string{".hs"}},
	{Name: "Lua", Color: "#000080", Extensions: []string{".lua"}},
	{Name: "Perl", Color: "#0298C3", Extensions: []string{".pl", ".pm"}},
	{Name: "R", Color: "#198CE7", Extensions: []string{".r"}},
	{Name: "Shell", Color: "#89E051", Extensions: []string{".sh", ".bash", ".zsh"}},
	{Name: "PowerShell", Color: "#012456", Extensions: []string{".ps1", ".psm1"}},
	{Name: "Makefile", Color: "#427819", Extensions: []string{".mk"}, Filenames: []string{"Makefile", "GNUmakefile"}},
	{Name: "Dockerfile", Color: "#384D54", Extensions: []string{".dockerfile"}, Filenames: []string{"Dockerfile"}},
	{Name: "HTML", Color: "#E34C26", Extensions: []string{".html", ".htm"}},
	{Name: "CSS", Color: "#563D7C", Extensions: []string{".css"}},
	{Name: "SCSS", Color: "#C6538C", Extensions: []string{".scss"}},
	{Name: "Less", Color: "#1D365D", Extensions: []string{".less"}},
	{Name: "Vue", Color: "#41B883", Extensions: []string{".vue"}},
	{Name: "Svelte", Color: "#FF3E00", Extensions: []string{".svelte"}},
	{Name: "SQL", Color: "#E38C00", Extensions: []string{".sql"}},
	{Name: "HCL", Color: "#844FBA", Extensions: []string{".tf", ".tfvars", ".hcl"}},
	{Name: "Objective-C", Color: "#438EFF", Extensions: []string{".m", ".mm"}},
	{Name: "Groovy", Color: "#4298B8", Extensions: []string{".groovy", ".gradle"}},
	{Name: "Zig", Color: "#EC915C", Extensions: []string{".zig"}},
	{Name: "Protocol Buffer", Color: "#6F8FA0", Extensions: []string{".proto"}},
	{Name: "TeX", Color: "#3D6117", Extensions: []string{".tex"}},
	{Name: "Batchfile", Color: "#C1F12E", Extensions: []string{".bat", ".cmd"}},
}
