package designcontext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

var contentHashPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._-]{7,127}$`)

const (
	CurrentVersion = 1
	MaxAge         = 30 * 24 * time.Hour
)

type Source struct {
	Kind        string `json:"kind"`
	Locator     string `json:"locator"`
	Revision    string `json:"revision,omitempty"`
	ContentHash string `json:"content_hash"`
	CapturedAt  string `json:"captured_at"`
}

type Component struct {
	Name        string `json:"name"`
	CodeRef     string `json:"code_ref,omitempty"`
	FigmaNodeID string `json:"figma_node_id,omitempty"`
	Usage       string `json:"usage,omitempty"`
}

type Context struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Figma   struct {
		LibraryFileKey string `json:"library_file_key,omitempty"`
		Notes          string `json:"notes,omitempty"`
	} `json:"figma"`
	Tokens struct {
		Colors     map[string]string `json:"colors"`
		Typography map[string]string `json:"typography"`
		Spacing    map[string]string `json:"spacing"`
	} `json:"tokens"`
	Components       []Component `json:"components"`
	Conventions      []string    `json:"conventions"`
	AntiPatterns     []string    `json:"anti_patterns"`
	LegacyNotes      string      `json:"legacy_notes,omitempty"`
	ScreensReference string      `json:"screens_reference,omitempty"`
	Sources          []Source    `json:"sources"`
}

type Freshness struct {
	Status       string   `json:"status"`
	StaleSources []string `json:"stale_sources,omitempty"`
}

func DecodeProposal(raw []byte) (Context, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return Context{}, errors.New("design context must be a JSON object")
	}
	for _, field := range []string{"version", "kind", "figma", "tokens", "components", "conventions", "anti_patterns", "sources"} {
		if _, ok := fields[field]; !ok {
			return Context{}, fmt.Errorf("design context requires %s", field)
		}
	}
	var c Context
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Context{}, fmt.Errorf("invalid design context: %w", err)
	}
	if err := ensureEOF(dec); err != nil {
		return Context{}, err
	}
	normalize(&c)
	if err := Validate(c); err != nil {
		return Context{}, err
	}
	return c, nil
}

// ParseStored accepts pre-v1 backfill rows leniently. New writes always pass
// through DecodeProposal; this reader only keeps migrated deployments usable.
func ParseStored(raw []byte) (Context, error) {
	var c Context
	if err := json.Unmarshal(raw, &c); err != nil {
		return Context{}, err
	}
	normalize(&c)
	if c.Version == 0 {
		c.Version = CurrentVersion
	}
	if c.Kind != "tokens" && c.Kind != "inventory" {
		return Context{}, errors.New("design context kind must be tokens or inventory")
	}
	return c, nil
}

func Validate(c Context) error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("design context version must be %d", CurrentVersion)
	}
	if c.Kind != "tokens" && c.Kind != "inventory" {
		return errors.New("design context kind must be tokens or inventory")
	}
	if len(c.Components) > 500 {
		return errors.New("design context has too many components")
	}
	if len(c.Conventions) > 200 || len(c.AntiPatterns) > 200 {
		return errors.New("design context has too many rules")
	}
	if len(c.Sources) == 0 {
		return errors.New("design context requires at least one authoritative source")
	}
	for i, source := range c.Sources {
		source.Kind = strings.TrimSpace(source.Kind)
		if source.Kind != "figma" && source.Kind != "storybook" && source.Kind != "repository" && source.Kind != "manual" {
			return fmt.Errorf("sources[%d].kind is not supported", i)
		}
		if strings.TrimSpace(source.Locator) == "" || strings.TrimSpace(source.ContentHash) == "" {
			return fmt.Errorf("sources[%d] requires locator and content_hash", i)
		}
		if len(source.Locator) > 2048 || len(source.Revision) > 256 || !contentHashPattern.MatchString(source.ContentHash) {
			return fmt.Errorf("sources[%d] has invalid provenance metadata", i)
		}
		capturedAt, err := time.Parse(time.RFC3339, source.CapturedAt)
		if err != nil {
			return fmt.Errorf("sources[%d].captured_at must be RFC3339", i)
		}
		if capturedAt.After(time.Now().UTC().Add(5 * time.Minute)) {
			return fmt.Errorf("sources[%d].captured_at cannot be in the future", i)
		}
	}
	for i, component := range c.Components {
		if strings.TrimSpace(component.Name) == "" {
			return fmt.Errorf("components[%d].name is required", i)
		}
		if len(component.Name) > 200 || len(component.CodeRef) > 2048 || len(component.FigmaNodeID) > 256 || len(component.Usage) > 2000 {
			return fmt.Errorf("components[%d] exceeds field limits", i)
		}
	}
	if len(c.Figma.LibraryFileKey) > 256 || len(c.Figma.Notes) > 4000 || len(c.LegacyNotes) > 8000 || len(c.ScreensReference) > 2048 {
		return errors.New("design context exceeds field limits")
	}
	for _, values := range []map[string]string{c.Tokens.Colors, c.Tokens.Typography, c.Tokens.Spacing} {
		if len(values) > 500 {
			return errors.New("design context has too many tokens")
		}
		for key, value := range values {
			if len(key) > 200 || len(value) > 1000 {
				return errors.New("design context token exceeds field limits")
			}
		}
	}
	return nil
}

func Hash(c Context) (contextHash, sourceHash string, contextJSON, sourcesJSON []byte, err error) {
	contextJSON, err = json.Marshal(c)
	if err != nil {
		return "", "", nil, nil, err
	}
	sourcesJSON, err = json.Marshal(c.Sources)
	if err != nil {
		return "", "", nil, nil, err
	}
	return sha256Hex(contextJSON), sha256Hex(sourcesJSON), contextJSON, sourcesJSON, nil
}

func EvaluateFreshness(c Context, now time.Time) Freshness {
	if len(c.Sources) == 0 {
		return Freshness{Status: "unverified"}
	}
	stale := make([]string, 0)
	for _, source := range c.Sources {
		captured, err := time.Parse(time.RFC3339, source.CapturedAt)
		if err != nil || strings.TrimSpace(source.ContentHash) == "" {
			return Freshness{Status: "unverified"}
		}
		if now.Sub(captured) > MaxAge {
			stale = append(stale, source.Kind+":"+source.Locator)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		return Freshness{Status: "stale", StaleSources: stale}
	}
	return Freshness{Status: "fresh"}
}

// Merge returns one deterministic runtime snapshot. Project values override
// workspace values by token key and component name; list rules are de-duplicated
// while preserving the workspace-first, project-second precedence.
func Merge(workspace, project Context) Context {
	result := cloneContext(workspace)
	normalize(&result)
	if project.Version != 0 {
		result.Version = project.Version
	}
	if project.Kind != "" {
		result.Kind = project.Kind
	}
	if project.Figma.LibraryFileKey != "" {
		result.Figma.LibraryFileKey = project.Figma.LibraryFileKey
	}
	if project.Figma.Notes != "" {
		result.Figma.Notes = project.Figma.Notes
	}
	mergeMap(result.Tokens.Colors, project.Tokens.Colors)
	mergeMap(result.Tokens.Typography, project.Tokens.Typography)
	mergeMap(result.Tokens.Spacing, project.Tokens.Spacing)
	result.Components = mergeComponents(result.Components, project.Components)
	result.Conventions = mergeStrings(result.Conventions, project.Conventions)
	result.AntiPatterns = mergeStrings(result.AntiPatterns, project.AntiPatterns)
	result.Sources = mergeSources(result.Sources, project.Sources)
	if project.LegacyNotes != "" {
		result.LegacyNotes = project.LegacyNotes
	}
	if project.ScreensReference != "" {
		result.ScreensReference = project.ScreensReference
	}
	return result
}

func cloneContext(source Context) Context {
	result := source
	result.Tokens.Colors = cloneMap(source.Tokens.Colors)
	result.Tokens.Typography = cloneMap(source.Tokens.Typography)
	result.Tokens.Spacing = cloneMap(source.Tokens.Spacing)
	result.Components = append([]Component(nil), source.Components...)
	result.Conventions = append([]string(nil), source.Conventions...)
	result.AntiPatterns = append([]string(nil), source.AntiPatterns...)
	result.Sources = append([]Source(nil), source.Sources...)
	return result
}

func cloneMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func normalize(c *Context) {
	if c.Tokens.Colors == nil {
		c.Tokens.Colors = map[string]string{}
	}
	if c.Tokens.Typography == nil {
		c.Tokens.Typography = map[string]string{}
	}
	if c.Tokens.Spacing == nil {
		c.Tokens.Spacing = map[string]string{}
	}
	if c.Components == nil {
		c.Components = []Component{}
	}
	if c.Conventions == nil {
		c.Conventions = []string{}
	}
	if c.AntiPatterns == nil {
		c.AntiPatterns = []string{}
	}
	if c.Sources == nil {
		c.Sources = []Source{}
	}
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("design context must contain one JSON object")
		}
		return fmt.Errorf("invalid design context: %w", err)
	}
	return nil
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func mergeMap(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func mergeComponents(base, override []Component) []Component {
	byName := make(map[string]Component, len(base)+len(override))
	for _, component := range append(append([]Component{}, base...), override...) {
		byName[strings.ToLower(strings.TrimSpace(component.Name))] = component
	}
	keys := make([]string, 0, len(byName))
	for key := range byName {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]Component, 0, len(keys))
	for _, key := range keys {
		result = append(result, byName[key])
	}
	return result
}

func mergeStrings(base, override []string) []string {
	seen := make(map[string]bool, len(base)+len(override))
	result := make([]string, 0, len(base)+len(override))
	for _, value := range append(append([]string{}, base...), override...) {
		key := strings.ToLower(strings.TrimSpace(value))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func mergeSources(base, override []Source) []Source {
	byKey := make(map[string]Source, len(base)+len(override))
	for _, source := range append(append([]Source{}, base...), override...) {
		key := strings.ToLower(strings.TrimSpace(source.Kind)) + "\x00" + strings.TrimSpace(source.Locator)
		byKey[key] = source
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]Source, 0, len(keys))
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result
}
