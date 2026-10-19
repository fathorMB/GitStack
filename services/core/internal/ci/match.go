package ci

import (
	"regexp"
	"strings"
	"sync"
)

// Tipi di evento (come nel contratto, WorkflowRun.event).
const (
	EventPush     = "push"
	EventPull     = "pull_request"
	EventDispatch = "workflow_dispatch"
	EventSchedule = "schedule"
)

// Azioni di pull_request che avviano un workflow se `types` non è indicato.
var defaultPullTypes = []string{"opened", "synchronize", "reopened"}

// Event è ciò che è successo, già ridotto ai dati utili ai filtri.
type Event struct {
	Kind string
	// Ref è il nome completo: refs/heads/main, refs/tags/v1. Per una PR è il
	// branch di origine (refs/heads/...).
	Ref string
	SHA string
	// Deleted: il push ha eliminato il ref. Non avvia niente.
	Deleted bool
	// Message è il messaggio del commit di testa, per [skip ci].
	Message string
	// Changed sono i file cambiati; nil = sconosciuti (i filtri paths non
	// possono escludere niente e il workflow parte).
	Changed []string
	// Per le PR: numero, azione (opened, synchronize, reopened) e branch di destinazione.
	PullNumber int64
	PullAction string
	BaseBranch string
}

// Branch e Tag estraggono il nome breve dal ref.
func (e Event) Branch() (string, bool) { return strings.CutPrefix(e.Ref, "refs/heads/") }
func (e Event) Tag() (string, bool)    { return strings.CutPrefix(e.Ref, "refs/tags/") }

// Matches dice se il workflow parte per l'evento ev (solo push e
// pull_request: dispatch e schedule non hanno filtri, hanno altre regole).
// Non guarda [skip ci] né lo stato del repo: lo fa la policy.
func (wf *Workflow) Matches(ev Event) bool {
	switch ev.Kind {
	case EventPush:
		return wf.Push != nil && !ev.Deleted && matchPush(wf.Push, ev)
	case EventPull:
		return wf.PullRequest != nil && matchPull(wf.PullRequest, ev)
	case EventDispatch:
		return wf.Dispatch
	}
	return false
}

func matchPush(f *Filter, ev Event) bool {
	if br, ok := ev.Branch(); ok {
		// Con soli filtri sui tag un branch non parte, e viceversa.
		if (len(f.Tags) > 0 || len(f.TagsIgnore) > 0) && len(f.Branches) == 0 && len(f.BranchesIgnore) == 0 {
			return false
		}
		if !matchRefs(f.Branches, f.BranchesIgnore, br) {
			return false
		}
		return matchPaths(f, ev.Changed)
	}
	tag, ok := ev.Tag()
	if !ok {
		return false
	}
	if (len(f.Branches) > 0 || len(f.BranchesIgnore) > 0) && len(f.Tags) == 0 && len(f.TagsIgnore) == 0 {
		return false
	}
	// Un tag non ha file cambiati da confrontare: un workflow che filtra solo per
	// paths non parte su un tag. I filtri paths non si valutano sui tag.
	if len(f.Tags) == 0 && len(f.TagsIgnore) == 0 && (len(f.Paths) > 0 || len(f.PathsIgnore) > 0) {
		return false
	}
	return matchRefs(f.Tags, f.TagsIgnore, tag)
}

func matchPull(f *Filter, ev Event) bool {
	types := f.Types
	if len(types) == 0 {
		types = defaultPullTypes
	}
	found := false
	for _, t := range types {
		if t == ev.PullAction {
			found = true
		}
	}
	if !found {
		return false
	}
	// branches/branches-ignore valgono per il branch di destinazione.
	if !matchRefs(f.Branches, f.BranchesIgnore, ev.BaseBranch) {
		return false
	}
	return matchPaths(f, ev.Changed)
}

// matchRefs applica i filtri positivi (con eventuali «!») e *-ignore.
func matchRefs(include, ignore []string, name string) bool {
	if len(include) > 0 && !matchList(include, name) {
		return false
	}
	if len(ignore) > 0 && matchList(ignore, name) {
		return false
	}
	return true
}

// matchPaths: con `paths` basta un file che corrisponde; con `paths-ignore` il
// workflow non parte se tutti i file sono ignorati. File sconosciuti: parte.
func matchPaths(f *Filter, changed []string) bool {
	if len(f.Paths) == 0 && len(f.PathsIgnore) == 0 {
		return true
	}
	if changed == nil {
		return true
	}
	if len(f.Paths) > 0 {
		ok := false
		for _, c := range changed {
			if matchList(f.Paths, c) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(f.PathsIgnore) > 0 && len(changed) > 0 {
		all := true
		for _, c := range changed {
			if !matchList(f.PathsIgnore, c) {
				all = false
				break
			}
		}
		if all {
			return false
		}
	}
	return true
}

// matchList valuta i pattern in ordine: uno positivo che corrisponde accende,
// uno col «!» che corrisponde spegne (come GitHub).
func matchList(patterns []string, name string) bool {
	matched := false
	for _, p := range patterns {
		if neg, ok := strings.CutPrefix(p, "!"); ok {
			if Glob(neg, name) {
				matched = false
			}
			continue
		}
		if Glob(p, name) {
			matched = true
		}
	}
	return matched
}

var (
	globMu    sync.Mutex
	globCache = map[string]*regexp.Regexp{}
)

// Glob confronta name con un pattern in stile GitHub: `*` qualsiasi sequenza
// senza «/», `**` qualsiasi sequenza anche con «/», `?` un carattere diverso da
// «/». `**/` iniziale o intermedio può anche essere vuoto.
func Glob(pattern, name string) bool {
	globMu.Lock()
	re, ok := globCache[pattern]
	if !ok {
		re = compileGlob(pattern)
		if len(globCache) < 4096 {
			globCache[pattern] = re
		}
	}
	globMu.Unlock()
	return re.MatchString(name)
}

func compileGlob(p string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	rs := []rune(p)
	for i := 0; i < len(rs); i++ {
		switch c := rs[i]; c {
		case '*':
			if i+1 < len(rs) && rs[i+1] == '*' {
				i++
				if i+1 < len(rs) && rs[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
