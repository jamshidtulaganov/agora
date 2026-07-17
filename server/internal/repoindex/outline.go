package repoindex

import (
	"regexp"
	"strings"
)

// Symbol is one definition found in a file.
type Symbol struct {
	Name string
	Kind string // func, type, class, method, const...
	Line int    // 1-indexed
	Sig  string // the trimmed source line the symbol was declared on
}

// Phase 0 outlining is regex-based on purpose. A real parser (tree-sitter)
// buys correctness on dynamic PHP/Vue and is the Phase 1 upgrade — but it is
// a heavy dependency to take on before the A/B has shown the pack pays for
// itself. Regex outlines are wrong at the margins (a `func` inside a string
// literal, a commented-out class) and that is acceptable: the outline is a
// navigation hint the agent verifies by reading, never an authority.
//
// Every pattern captures the symbol name in group 1.
var outlinePatterns = map[string][]struct {
	kind string
	re   *regexp.Regexp
}{
	"go": {
		{"func", regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*[\(\[]`)},
		{"type", regexp.MustCompile(`^type\s+([A-Za-z_]\w*)\s`)},
	},
	"php": {
		{"class", regexp.MustCompile(`^\s*(?:final\s+|abstract\s+)?class\s+([A-Za-z_]\w*)`)},
		{"interface", regexp.MustCompile(`^\s*interface\s+([A-Za-z_]\w*)`)},
		{"trait", regexp.MustCompile(`^\s*trait\s+([A-Za-z_]\w*)`)},
		{"func", regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|static\s+)*function\s+([A-Za-z_]\w*)\s*\(`)},
	},
	"python": {
		{"class", regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`)},
		{"func", regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`)},
	},
	"kotlin": {
		{"class", regexp.MustCompile(`^\s*(?:data\s+|sealed\s+|abstract\s+|open\s+)*class\s+([A-Za-z_]\w*)`)},
		{"interface", regexp.MustCompile(`^\s*interface\s+([A-Za-z_]\w*)`)},
		{"object", regexp.MustCompile(`^\s*object\s+([A-Za-z_]\w*)`)},
		{"func", regexp.MustCompile(`^\s*(?:private\s+|internal\s+|public\s+|suspend\s+|override\s+)*fun\s+(?:<[^>]+>\s*)?([A-Za-z_]\w*)\s*\(`)},
	},
	"sql": {
		{"table", regexp.MustCompile(`(?i)^\s*create\s+table\s+(?:if\s+not\s+exists\s+)?"?([A-Za-z_]\w*)"?`)},
		{"view", regexp.MustCompile(`(?i)^\s*create\s+(?:or\s+replace\s+)?view\s+"?([A-Za-z_]\w*)"?`)},
		{"func", regexp.MustCompile(`(?i)^\s*create\s+(?:or\s+replace\s+)?function\s+"?([A-Za-z_]\w*)"?`)},
		// sqlc/dbmate style named queries — the real navigation unit in this
		// repo's own SQL, where every file is one big CREATE-less script.
		{"query", regexp.MustCompile(`^--\s*name:\s*([A-Za-z_]\w*)`)},
	},
}

// tsPatterns cover the TS/JS family. Shared by ts, tsx, js, jsx and (for the
// <script> half) vue.
var tsPatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"class", regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`)},
	{"interface", regexp.MustCompile(`^\s*(?:export\s+)?interface\s+([A-Za-z_$][\w$]*)`)},
	{"type", regexp.MustCompile(`^\s*(?:export\s+)?type\s+([A-Za-z_$][\w$]*)\s*=`)},
	{"func", regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)`)},
	// `export const Foo = (...) =>` and `const Foo = function` — the dominant
	// component/handler shape in this codebase.
	{"func", regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*(?::[^=]+)?=>`)},
	{"const", regexp.MustCompile(`^\s*export\s+(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*[:=]`)},
}

// maxOutlineSymbols bounds one file's outline. A generated 5000-symbol file
// would otherwise crowd every other file out of the pack.
const maxOutlineSymbols = 60

// Outline extracts a file's definitions. Unknown languages return nil — the
// file is still searchable, it just contributes no structural summary.
func Outline(lang, body string) []Symbol {
	patterns, ok := outlinePatterns[lang]
	if !ok {
		switch lang {
		case "ts", "tsx", "js", "jsx", "vue":
			patterns = tsPatterns
		default:
			return nil
		}
	}
	var syms []Symbol
	for i, line := range strings.Split(body, "\n") {
		if len(syms) >= maxOutlineSymbols {
			break
		}
		// Cheap pre-filter: a deeply indented or enormous line is either
		// nested detail or minified output, neither worth outlining.
		if len(line) > 400 {
			continue
		}
		for _, p := range patterns {
			m := p.re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			syms = append(syms, Symbol{
				Name: m[1],
				Kind: p.kind,
				Line: i + 1,
				Sig:  strings.TrimSpace(line),
			})
			break // one symbol per line; first pattern wins
		}
	}
	return syms
}
