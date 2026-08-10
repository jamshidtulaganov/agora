package repoindex

import (
	"context"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	corpusCacheVersion              = 1
	maxCachedTermsPerDocument       = 16_384
	maxCorpusCacheBytes       int64 = 512 << 20
)

// Stats.CacheHit distinguishes fast retrieval from the one-time full corpus
// build. It intentionally remains local daemon telemetry for now; task-level
// experiment reporting can add it without changing pack semantics.

type cachedDocument struct {
	Path          string
	Language      string
	SourcePath    string
	Length        int
	TermFrequency map[string]uint16
}

type cachedCorpus struct {
	Documents     []cachedDocument
	TotalLength   int
	FilesScanned  int
	FilesExcluded int
	Partial       bool
	IsGit         bool
}

type corpusCacheFile struct {
	Version     int
	Fingerprint string
	Corpus      cachedCorpus
}

var corpusCacheLocks sync.Map

// PackRootsCached separates full-corpus reading from per-task retrieval. It
// builds a compact term index once, stores it under cacheDir with 0600
// permissions, and reuses it while the Git/directory fingerprint is stable.
// A task then scores metadata and opens only the handful of selected files.
// Cache failures degrade to the existing streaming PackRoots implementation.
func PackRootsCached(ctx context.Context, roots []string, query string, tokenBudget int, cacheDir string) (string, Stats, error) {
	if strings.TrimSpace(cacheDir) == "" {
		return PackRoots(ctx, roots, query, tokenBudget)
	}
	if tokenBudget <= 0 {
		tokenBudget = DefaultTokenBudget
	}
	q := newRanker(query)
	if len(q.qTerms) == 0 {
		return "", Stats{Degraded: true}, nil
	}

	normalizedRoots := normalizeRoots(roots)
	if len(normalizedRoots) == 0 {
		return "", Stats{Degraded: true}, nil
	}
	fingerprint, err := corpusFingerprint(ctx, normalizedRoots)
	if err != nil {
		return PackRoots(ctx, normalizedRoots, query, tokenBudget)
	}
	cachePath := filepath.Join(cacheDir, rootsCacheKey(normalizedRoots)+".gob")
	lockValue, _ := corpusCacheLocks.LoadOrStore(cachePath, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	corpus, cacheHit := loadCachedCorpus(cachePath, fingerprint)
	if !cacheHit {
		corpus, err = buildCachedCorpus(ctx, normalizedRoots)
		if err != nil {
			return "", statsFromCorpus(corpus, false), err
		}
		// A checkout can change while it is being indexed. Only persist a cache
		// when the post-build fingerprint still matches the snapshot we read.
		if after, afterErr := corpusFingerprint(ctx, normalizedRoots); afterErr == nil && after == fingerprint {
			_ = saveCachedCorpus(cachePath, fingerprint, corpus)
		}
	}

	stats := statsFromCorpus(corpus, cacheHit)
	hits := corpus.top(q.qTerms, maxFilesInPack)
	if len(hits) == 0 {
		return "", stats, nil
	}
	sources := make(map[string]string, len(corpus.Documents))
	for _, doc := range corpus.Documents {
		sources[doc.Path] = doc.SourcePath
	}
	return renderResolved(hits, tokenBudget, stats, func(hit Hit) (string, bool) {
		path, ok := sources[hit.Path]
		if !ok {
			return "", false
		}
		return readTextFile(path)
	})
}

func normalizeRoots(roots []string) []string {
	result := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		if real, err := filepath.EvalSymlinks(root); err == nil {
			root = real
		}
		result = append(result, filepath.Clean(root))
	}
	return result
}

func rootsCacheKey(roots []string) string {
	h := sha256.New()
	for i, root := range roots {
		if i > 0 {
			_, _ = h.Write([]byte{0})
		}
		_, _ = io.WriteString(h, root)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func buildCachedCorpus(ctx context.Context, roots []string) (cachedCorpus, error) {
	corpus := cachedCorpus{}
	labelCounts := map[string]int{}
	for _, root := range roots {
		labelCounts[filepath.Base(root)]++
	}
	seenLabels := map[string]int{}
	for _, root := range roots {
		label := ""
		if len(roots) > 1 {
			base := filepath.Base(root)
			label = base
			if labelCounts[base] > 1 {
				seenLabels[base]++
				label = fmt.Sprintf("%s-%d", base, seenLabels[base])
			}
		}
		scan, err := scanRepo(ctx, root, func(relPath, lang, body string) {
			displayPath := relPath
			if label != "" {
				displayPath = filepath.ToSlash(filepath.Join(label, filepath.FromSlash(relPath)))
			}
			doc := makeCachedDocument(displayPath, lang, filepath.Join(root, filepath.FromSlash(relPath)), body)
			corpus.TotalLength += doc.Length
			corpus.Documents = append(corpus.Documents, doc)
		})
		corpus.FilesScanned += scan.Scanned
		corpus.FilesExcluded += scan.Excluded
		corpus.Partial = corpus.Partial || scan.Partial
		corpus.IsGit = corpus.IsGit || scan.IsGit
		if err != nil {
			return corpus, err
		}
	}
	return corpus, nil
}

func makeCachedDocument(path, lang, sourcePath, body string) cachedDocument {
	tf := make(map[string]uint16)
	length := 0
	add := func(token string, weight int) {
		length += weight
		if _, exists := tf[token]; !exists && len(tf) >= maxCachedTermsPerDocument {
			return
		}
		value := int(tf[token]) + weight
		if value > math.MaxUint16 {
			value = math.MaxUint16
		}
		tf[token] = uint16(value)
	}
	for _, token := range tokenize(path) {
		add(token, pathBoost)
	}
	for _, token := range tokenize(body) {
		add(token, 1)
	}
	return cachedDocument{
		Path: path, Language: lang, SourcePath: sourcePath,
		Length: length, TermFrequency: tf,
	}
}

func (c cachedCorpus) top(queryTerms []string, limit int) []Hit {
	if len(c.Documents) == 0 || c.TotalLength == 0 {
		return nil
	}
	df := make(map[string]int, len(queryTerms))
	for _, doc := range c.Documents {
		for _, term := range queryTerms {
			if doc.TermFrequency[term] > 0 {
				df[term]++
			}
		}
	}
	avgLength := float64(c.TotalLength) / float64(len(c.Documents))
	n := float64(len(c.Documents))
	hits := make([]Hit, 0)
	for _, doc := range c.Documents {
		score := 0.0
		matched := make([]string, 0, len(queryTerms))
		for _, term := range queryTerms {
			frequency := doc.TermFrequency[term]
			if frequency == 0 {
				continue
			}
			documentFrequency := float64(df[term])
			idf := math.Log(1 + (n-documentFrequency+0.5)/(documentFrequency+0.5))
			tf := float64(frequency)
			normalized := tf * (bm25K1 + 1) /
				(tf + bm25K1*(1-bm25B+bm25B*float64(doc.Length)/avgLength))
			score += idf * normalized
			matched = append(matched, term)
		}
		if len(matched) == 0 {
			continue
		}
		sort.Strings(matched)
		hits = append(hits, Hit{
			Path: doc.Path, Lang: doc.Language,
			Score: score * langWeight(doc.Language, doc.Path), Terms: matched,
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Path < hits[j].Path
	})
	hits = dropLocaleTwins(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func statsFromCorpus(c cachedCorpus, cacheHit bool) Stats {
	return Stats{
		FilesScanned: c.FilesScanned, FilesExcluded: c.FilesExcluded,
		Partial: c.Partial, IsGit: c.IsGit, Degraded: true, CacheHit: cacheHit,
	}
}

func loadCachedCorpus(path, fingerprint string) (cachedCorpus, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 || info.Size() > maxCorpusCacheBytes {
		return cachedCorpus{}, false
	}
	file, err := os.Open(path)
	if err != nil {
		return cachedCorpus{}, false
	}
	defer file.Close()
	var cached corpusCacheFile
	if err := gob.NewDecoder(io.LimitReader(file, maxCorpusCacheBytes)).Decode(&cached); err != nil {
		return cachedCorpus{}, false
	}
	if cached.Version != corpusCacheVersion || cached.Fingerprint != fingerprint {
		return cachedCorpus{}, false
	}
	return cached.Corpus, true
}

func saveCachedCorpus(path, fingerprint string, corpus cachedCorpus) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".repo-index-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	encodeErr := gob.NewEncoder(tmp).Encode(corpusCacheFile{
		Version: corpusCacheVersion, Fingerprint: fingerprint, Corpus: corpus,
	})
	closeErr := tmp.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmpPath, path)
}

func corpusFingerprint(ctx context.Context, roots []string) (string, error) {
	h := sha256.New()
	for _, root := range roots {
		_, _ = io.WriteString(h, root)
		_, _ = h.Write([]byte{0})
		if head, err := gitHead(ctx, root); err == nil {
			_, _ = io.WriteString(h, "git\x00"+head+"\x00")
			cmd := exec.CommandContext(ctx, "git", "-C", root, "diff", "--no-ext-diff", "--binary", "HEAD", "--", ".")
			cmd.Stdout = h
			if err := cmd.Run(); err != nil {
				return "", err
			}
			continue
		}
		if err := fingerprintDirectory(ctx, h, root); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func gitHead(ctx context.Context, root string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func fingerprintDirectory(ctx context.Context, h hash.Hash, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && isDeniedDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isDeniedFile(entry.Name()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes || info.Size() == 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		_, _ = io.WriteString(h, filepath.ToSlash(rel))
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, strconv.FormatInt(info.Size(), 10))
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, strconv.FormatInt(info.ModTime().UnixNano(), 10))
		_, _ = h.Write([]byte{0})
		return nil
	})
}
