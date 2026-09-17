package assistant

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Artifacts — the assistant's rich outputs. See
// docs/agora-assistant-artifacts-plan.md §3-§5.
//
// This file is the CONTENT contract and nothing else: kinds, caps, and the
// shape of the two structured specs. It is pure (no DB, no HTTP) because the
// same rules have to hold in two places that cannot share a transaction — the
// tool executor, which must bounce a bad spec back to the model BEFORE
// anything is written, and any future importer.
//
// Validation failures are written to be read by the MODEL. Each one names the
// field that is wrong, because the only useful outcome of a rejected spec is
// the model fixing that field and calling again — "invalid chart spec" costs a
// round trip and teaches nothing.

// The four kinds. chart/table are data-only structured specs rendered
// natively; markdown goes through the sanitized chat renderer; html is the
// escape hatch and is the only one that ever reaches a sandboxed iframe.
const (
	ArtifactKindChart    = "chart"
	ArtifactKindTable    = "table"
	ArtifactKindMarkdown = "markdown"
	ArtifactKindHTML     = "html"
)

// Chart types the native renderer supports.
const (
	ChartTypeBar  = "bar"
	ChartTypeLine = "line"
	ChartTypeArea = "area"
	ChartTypePie  = "pie"
)

const (
	// MaxArtifactContentBytes caps one artifact body. 256 KB is far more than
	// any honest chart spec or report needs and far less than a transcript
	// row the transport would choke on.
	MaxArtifactContentBytes = 256 * 1024
	// MaxArtifactsPerSession bounds how many a single conversation can
	// accumulate. A model that answers every follow-up with a NEW artifact
	// instead of updating the one on screen is the failure this catches.
	MaxArtifactsPerSession = 20
	// MaxArtifactTitleLen caps the card label.
	MaxArtifactTitleLen = 200
	// MaxArtifactRevisions bounds the immutable history one artifact keeps
	// (docs/agora-assistant-final-plan.md §4 Phase 4). 50 is chosen against
	// the thing that actually generates revisions: a scheduled refresh
	// (artifacts plan §8 Phase 3) writing a new version on every run, where 50
	// covers seven weeks of a daily dashboard — long enough that "compare this
	// with last month's" is answerable — while capping one artifact's storage
	// at 50 x 256 KB.
	//
	// v1 is never dropped by the cap: it is the only version whose meaning is
	// self-contained ("what this was before anyone edited it"), so the oldest
	// surviving revision never silently starts claiming to be the original.
	MaxArtifactRevisions = 50
)

// ArtifactKinds is the ordered kind list, for prompts and error text.
var ArtifactKinds = []string{ArtifactKindChart, ArtifactKindTable, ArtifactKindMarkdown, ArtifactKindHTML}

// IsArtifactKind reports whether kind is one this build renders.
func IsArtifactKind(kind string) bool {
	switch kind {
	case ArtifactKindChart, ArtifactKindTable, ArtifactKindMarkdown, ArtifactKindHTML:
		return true
	}
	return false
}

// ValidateArtifactKind turns an unknown kind into a correction naming the four
// that exist.
func ValidateArtifactKind(kind string) error {
	if !IsArtifactKind(kind) {
		return fmt.Errorf("kind must be one of %s (got %q)", strings.Join(ArtifactKinds, ", "), kind)
	}
	return nil
}

// ValidateArtifactTitle checks the card label.
func ValidateArtifactTitle(title string) error {
	if strings.TrimSpace(title) == "" {
		return errors.New("title is required — it is what the user sees on the artifact card")
	}
	if len(title) > MaxArtifactTitleLen {
		return fmt.Errorf("title is too long (max %d characters)", MaxArtifactTitleLen)
	}
	return nil
}

// ValidateArtifactContent checks the body against its kind: size for every
// kind, plus the spec shape for the two structured ones.
//
// It is called BEFORE any write, so a rejected spec leaves no row behind — the
// model sees the correction and retries into an unchanged session.
func ValidateArtifactContent(kind, content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("content is required")
	}
	if len(content) > MaxArtifactContentBytes {
		return fmt.Errorf("content is too large (%d bytes, max %d)", len(content), MaxArtifactContentBytes)
	}
	switch kind {
	case ArtifactKindChart:
		return validateChartSpec(content)
	case ArtifactKindTable:
		return validateTableSpec(content)
	case ArtifactKindMarkdown, ArtifactKindHTML:
		// Free text by definition. html is never trusted as markup here — it
		// is rendered only inside the sandboxed iframe (plan §7.1), so there
		// is nothing to validate that would make it safer.
		return nil
	}
	return ValidateArtifactKind(kind)
}

// ValidateArtifact is the whole create-time check in one call.
func ValidateArtifact(kind, title, content string) error {
	if err := ValidateArtifactKind(kind); err != nil {
		return err
	}
	if err := ValidateArtifactTitle(title); err != nil {
		return err
	}
	return ValidateArtifactContent(kind, content)
}

// decodeSpecObject parses a structured spec body into a generic object. A
// generic map rather than a typed struct is deliberate: unmarshalling
// `"series": [1,2,3]` into a typed field yields a json error about Go types,
// and the model cannot act on that. Walking the map lets every failure name
// the JSON field the model actually wrote.
func decodeSpecObject(content, kind string) (map[string]any, error) {
	var parsed any
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("a %s artifact's content must be a JSON object, and this is not valid JSON: %v", kind, err)
	}
	obj, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("a %s artifact's content must be a JSON object", kind)
	}
	return obj, nil
}

// validateChartSpec checks {type, x, series[], rows[]} — plan §3.
func validateChartSpec(content string) error {
	spec, err := decodeSpecObject(content, ArtifactKindChart)
	if err != nil {
		return err
	}

	switch t, _ := spec["type"].(string); t {
	case ChartTypeBar, ChartTypeLine, ChartTypeArea, ChartTypePie:
	default:
		return fmt.Errorf(`chart spec: "type" must be one of %s, %s, %s, %s`,
			ChartTypeBar, ChartTypeLine, ChartTypeArea, ChartTypePie)
	}

	if x, _ := spec["x"].(string); strings.TrimSpace(x) == "" {
		return errors.New(`chart spec: "x" must be a non-empty string naming the row field used for the category axis`)
	}

	series, ok := spec["series"].([]any)
	if !ok || len(series) == 0 {
		return errors.New(`chart spec: "series" must be a non-empty array of {"key": "..."} objects`)
	}
	for i, raw := range series {
		entry, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf(`chart spec: series[%d] must be an object with a "key"`, i)
		}
		if key, _ := entry["key"].(string); strings.TrimSpace(key) == "" {
			return fmt.Errorf(`chart spec: series[%d].key must be a non-empty string naming a field present in every row`, i)
		}
	}

	rows, ok := spec["rows"].([]any)
	if !ok {
		return errors.New(`chart spec: "rows" must be an array of objects, one per point on the x axis`)
	}
	for i, raw := range rows {
		if _, ok := raw.(map[string]any); !ok {
			return fmt.Errorf("chart spec: rows[%d] must be an object", i)
		}
	}
	return nil
}

// validateTableSpec checks {columns[], rows[][]} — plan §3.
func validateTableSpec(content string) error {
	spec, err := decodeSpecObject(content, ArtifactKindTable)
	if err != nil {
		return err
	}

	columns, ok := spec["columns"].([]any)
	if !ok || len(columns) == 0 {
		return errors.New(`table spec: "columns" must be a non-empty array of column-header strings`)
	}
	for i, raw := range columns {
		if _, ok := raw.(string); !ok {
			return fmt.Errorf("table spec: columns[%d] must be a string", i)
		}
	}

	rows, ok := spec["rows"].([]any)
	if !ok {
		return errors.New(`table spec: "rows" must be an array of arrays — one inner array of cells per row`)
	}
	for i, raw := range rows {
		if _, ok := raw.([]any); !ok {
			return fmt.Errorf("table spec: rows[%d] must be an array of cell values", i)
		}
	}
	return nil
}
