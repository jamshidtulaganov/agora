// Value mapping (docs/importers-plan.md §3.4).
//
// Three tables, all the same shape: an adapter DEFAULT, a per-workspace
// OVERRIDE, and both visible in the dry-run report so the operator can argue
// with either before confirming.
//
// Two rules the Bitrix and Zoho mappers already proved and this generalizes:
//
//   - Category first, name second. The source's own category (Linear's
//     WorkflowState.type, Jira's statusCategory) is stable across installs;
//     the state NAME is a hand-tuned artifact of one customer's workflow. The
//     name only refines WITHIN the category the source already assigned —
//     which is how "In Review" (a Linear `started` state) reaches in_review
//     instead of flattening to in_progress.
//   - Never drop an issue for an unknown status. `todo` is the "never dropped"
//     landing zone, the same contract bitrix.MapStatus and
//     zohoprojects.MapStatus keep. An unmapped state is a line in the report,
//     not a lost issue.
//
// What is deliberately NOT lifted from Bitrix is its keyword TABLE — the
// ordering comments ("release BEFORE done"), the mixed Russian/English labels
// with typos. That table is one portal's artifact. The mechanism generalizes;
// the table belongs to the adapter.
package imports

import (
	"encoding/json"
	"sort"
	"strings"
)

// Defaults is what an adapter contributes to the mapping: how ITS categories
// and ITS state names land in Agora, and how its priority ladder lines up with
// the canonical 0..4.
type Defaults struct {
	// StatusByCategory maps the source's own state category (lowercased) to an
	// Agora status. This is the primary table.
	StatusByCategory map[string]string
	// StatusByName maps an exact source state name (lowercased) to an Agora
	// status, for the handful the source names unambiguously. Consulted before
	// the category so an adapter can pin a specific state.
	StatusByName map[string]string
	// StatusRefine narrows a category result by keyword in the state name. Key
	// is the category (lowercased); the slice is ordered and first match wins,
	// because "in review" must beat "in progress" when a name contains both.
	StatusRefine map[string][]Refinement
	// Priority maps the canonical ladder to Agora priority names. Nil means the
	// straight ordinal map, which is what every source so far needs.
	Priority map[Priority]string
	// Fallback is the status an unmapped state lands on. Empty means StatusTodo
	// — an adapter may not choose "drop it".
	Fallback string
}

// Refinement is one keyword→status rule applied within a category.
type Refinement struct {
	Keywords []string
	Status   string
}

// Overrides are the per-workspace corrections, read from
// workspace.settings.import_mapping.<source>. Keys are lowercased source state
// names / priority names; values are Agora statuses and priorities.
type Overrides struct {
	Status   map[string]string `json:"status,omitempty"`
	Priority map[string]string `json:"priority,omitempty"`
	// Containers maps a source container id or key to an existing Agora
	// project id, for the "import into the project we already have" case. An
	// unlisted container gets a project created for it.
	Containers map[string]string `json:"containers,omitempty"`
}

// SettingsKeyMapping / SettingsKeyAliases are the workspace.settings keys the
// importer reads. Both are generic-by-source, replacing the per-vendor
// bitrix_stage_map / bitrix_identity_aliases keys.
const (
	SettingsKeyMapping = "import_mapping"
	SettingsKeyAliases = "import_identity_aliases"
)

// ParseOverrides reads workspace.settings and returns the overrides for one
// source. A missing key, a malformed blob, or a value of the wrong JSON type is
// an EMPTY override set, never an error: an import must not be blocked by
// something else's write to a free-form settings document. Unknown Agora
// statuses in the blob are dropped here and reported by the plan, so a typo in
// settings cannot produce a 23514 mid-run.
func ParseOverrides(settings []byte, source string) Overrides {
	out := Overrides{}
	if len(settings) == 0 || source == "" {
		return out
	}
	var doc struct {
		Mapping map[string]Overrides `json:"import_mapping"`
	}
	if err := json.Unmarshal(settings, &doc); err != nil {
		return out
	}
	raw, ok := doc.Mapping[source]
	if !ok {
		return out
	}
	if len(raw.Status) > 0 {
		out.Status = map[string]string{}
		for name, status := range raw.Status {
			if ValidStatus(status) {
				out.Status[foldKey(name)] = status
			}
		}
	}
	if len(raw.Priority) > 0 {
		out.Priority = map[string]string{}
		for name, prio := range raw.Priority {
			if validPriorityName(prio) {
				out.Priority[foldKey(name)] = prio
			}
		}
	}
	if len(raw.Containers) > 0 {
		out.Containers = map[string]string{}
		for key, projectID := range raw.Containers {
			if strings.TrimSpace(projectID) != "" {
				out.Containers[foldKey(key)] = strings.TrimSpace(projectID)
			}
		}
	}
	return out
}

// ParseAliases reads workspace.settings.import_identity_aliases — the "same
// human, two email addresses" override lifted as-is from
// bitrix_identity_aliases. Keys and values are both case-folded emails.
func ParseAliases(settings []byte) map[string]string {
	if len(settings) == 0 {
		return nil
	}
	var doc struct {
		Aliases map[string]string `json:"import_identity_aliases"`
	}
	if err := json.Unmarshal(settings, &doc); err != nil || len(doc.Aliases) == 0 {
		return nil
	}
	out := make(map[string]string, len(doc.Aliases))
	for from, to := range doc.Aliases {
		from, to = foldKey(from), foldKey(to)
		if from != "" && to != "" {
			out[from] = to
		}
	}
	return out
}

// Mapping is the resolved table for one import: adapter defaults with the
// workspace's overrides on top. It records what it could not map so the plan
// can list it — the operator assigns those in conversation and the answer lands
// back in workspace.settings as an override.
type Mapping struct {
	Source    string
	defaults  Defaults
	overrides Overrides

	// unmapped collects source state names that reached the fallback, with a
	// count each. A map, not a slice: a 10k-issue import must not accumulate
	// 10k identical lines.
	unmapped map[string]int
}

// NewMapping layers overrides on defaults. Neither argument is retained by
// reference in a way the caller can mutate afterwards for the tables that
// matter, because a mapping is frozen into import_job.mapping at confirm time
// and must not drift under a later settings edit.
func NewMapping(source string, defaults Defaults, overrides Overrides) *Mapping {
	m := &Mapping{
		Source:    source,
		defaults:  defaults,
		overrides: overrides,
		unmapped:  map[string]int{},
	}
	if m.defaults.Fallback == "" || !ValidStatus(m.defaults.Fallback) {
		m.defaults.Fallback = StatusTodo
	}
	return m
}

// Status maps one source workflow state to an Agora status.
//
// Resolution order, first hit wins:
//
//  1. per-workspace override on the exact state name;
//  2. adapter default on the exact state name;
//  3. adapter default on the source's CATEGORY, then refined by keyword within
//     that category;
//  4. the fallback (todo) — recorded as unmapped so the report can show it.
func (m *Mapping) Status(state State) string {
	name := foldKey(state.Name)
	if status, ok := m.overrides.Status[name]; ok && ValidStatus(status) {
		return status
	}
	if status, ok := m.defaults.StatusByName[name]; ok && ValidStatus(status) {
		return status
	}
	category := foldKey(state.Category)
	if status, ok := m.defaults.StatusByCategory[category]; ok && ValidStatus(status) {
		if refined, ok := m.refine(category, name); ok {
			return refined
		}
		return status
	}
	// A state the source gave no usable category for. Try the refinements of
	// every category before giving up — a state literally named "Blocked"
	// should not land on todo just because its category was blank.
	if refined, ok := m.refineAny(name); ok {
		return refined
	}
	m.noteUnmapped(state)
	return m.defaults.Fallback
}

func (m *Mapping) refine(category, name string) (string, bool) {
	for _, r := range m.defaults.StatusRefine[category] {
		for _, kw := range r.Keywords {
			if strings.Contains(name, kw) && ValidStatus(r.Status) {
				return r.Status, true
			}
		}
	}
	return "", false
}

func (m *Mapping) refineAny(name string) (string, bool) {
	categories := make([]string, 0, len(m.defaults.StatusRefine))
	for category := range m.defaults.StatusRefine {
		categories = append(categories, category)
	}
	// Deterministic: a map walk must not make the same import produce two
	// different statuses on two runs.
	sort.Strings(categories)
	for _, category := range categories {
		if status, ok := m.refine(category, name); ok {
			return status, true
		}
	}
	return "", false
}

func (m *Mapping) noteUnmapped(state State) {
	name := strings.TrimSpace(state.Name)
	if name == "" {
		name = "(unnamed state)"
	}
	m.unmapped[name]++
}

// Unmapped returns the source state names that reached the fallback, with how
// many issues each covered, sorted by name so the report is stable.
func (m *Mapping) Unmapped() []UnmappedStatus {
	out := make([]UnmappedStatus, 0, len(m.unmapped))
	for name, count := range m.unmapped {
		out = append(out, UnmappedStatus{Name: name, Issues: count, FellBackTo: m.defaults.Fallback})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// UnmappedStatus is one report line: a source state nothing claimed.
type UnmappedStatus struct {
	Name       string `json:"name"`
	Issues     int    `json:"issues"`
	FellBackTo string `json:"fell_back_to"`
}

// Priority maps the canonical ladder position to an Agora priority name.
// Overrides are keyed by the canonical NAME ("urgent"), which is what the
// operator sees in the report, not by the ordinal.
func (m *Mapping) Priority(p Priority) string {
	name := p.Name()
	if override, ok := m.overrides.Priority[name]; ok && validPriorityName(override) {
		return override
	}
	if mapped, ok := m.defaults.Priority[p]; ok && validPriorityName(mapped) {
		return mapped
	}
	return name
}

// ContainerProject returns the existing Agora project id the operator pinned a
// source container to, if any. Matching tries the container's id first and its
// key second, so a mapping written as {"ENG": "<uuid>"} works as naturally as
// one written with the source's opaque id.
func (m *Mapping) ContainerProject(c Container) (string, bool) {
	if len(m.overrides.Containers) == 0 {
		return "", false
	}
	for _, key := range []string{c.ExternalID, c.Key, c.Name} {
		if key == "" {
			continue
		}
		if id, ok := m.overrides.Containers[foldKey(key)]; ok {
			return id, true
		}
	}
	return "", false
}

// StatusTable renders the mapping the report shows: every state in the bundle,
// what it maps to, and how that was decided. This is the artifact the operator
// argues with, so "via" is part of it.
func (m *Mapping) StatusTable(states []State) []StatusMapping {
	out := make([]StatusMapping, 0, len(states))
	for _, state := range states {
		name := foldKey(state.Name)
		via := "default"
		switch {
		case m.overrides.Status[name] != "":
			via = "override"
		case m.defaults.StatusByName[name] != "":
			via = "default:name"
		case m.defaults.StatusByCategory[foldKey(state.Category)] != "":
			via = "default:category"
		default:
			via = "fallback"
		}
		out = append(out, StatusMapping{
			SourceName:     strings.TrimSpace(state.Name),
			SourceCategory: strings.TrimSpace(state.Category),
			Status:         m.Status(state),
			Via:            via,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceName < out[j].SourceName })
	return out
}

// StatusMapping is one row of the report's status table.
type StatusMapping struct {
	SourceName     string `json:"source_name"`
	SourceCategory string `json:"source_category,omitempty"`
	Status         string `json:"status"`
	Via            string `json:"via"` // override | default:name | default:category | fallback
}

// PriorityTable renders the five-row priority table for the report.
func (m *Mapping) PriorityTable() []PriorityMapping {
	ladder := []Priority{PriorityNone, PriorityUrgent, PriorityHigh, PriorityMedium, PriorityLow}
	out := make([]PriorityMapping, 0, len(ladder))
	for _, p := range ladder {
		via := "default"
		if _, ok := m.overrides.Priority[p.Name()]; ok {
			via = "override"
		}
		out = append(out, PriorityMapping{Source: p.Name(), Priority: m.Priority(p), Via: via})
	}
	return out
}

// PriorityMapping is one row of the report's priority table.
type PriorityMapping struct {
	Source   string `json:"source"`
	Priority string `json:"priority"`
	Via      string `json:"via"`
}

// Frozen renders the mapping actually used, for import_job.mapping. The row is
// written at confirm time so a later settings edit cannot change what the human
// authorized.
func (m *Mapping) Frozen() Overrides {
	return Overrides{
		Status:     copyStringMap(m.overrides.Status),
		Priority:   copyStringMap(m.overrides.Priority),
		Containers: copyStringMap(m.overrides.Containers),
	}
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func validPriorityName(s string) bool {
	switch s {
	case PriorityNameNone, PriorityNameUrgent, PriorityNameHigh, PriorityNameMedium, PriorityNameLow:
		return true
	}
	return false
}

// foldKey normalizes a lookup key: trimmed and lowercased. Every map in this
// file is keyed through it so "In Progress", "in progress" and " IN PROGRESS "
// are one entry rather than three near-misses.
func foldKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
