package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DesignReference stores a captured web UI snapshot — screenshot(s) + serialized
// DOM + computed-styles JSON + asset URL list. Uploaded by the browser
// extension at sdk/feedback/browser-extension/ and meant to be fed to a vibing
// task as an AI-readable design reference. Distinct from FeedbackReport, which
// describes an in-app bug from a guest device.
type DesignReference struct {
	ID           string                   `json:"id"`
	URL          string                   `json:"url,omitempty"`
	Title        string                   `json:"title,omitempty"`
	Source       string                   `json:"source"` // "browser-extension", "manual", ...
	Mode         string                   `json:"mode"`   // "viewport", "fullpage", "element"
	RootSelector string                   `json:"rootSelector,omitempty"`
	Screenshots  []string                 `json:"screenshots,omitempty"` // absolute paths under baseDir/<id>/
	HTMLPath     string                   `json:"htmlPath,omitempty"`
	StylesPath   string                   `json:"stylesPath,omitempty"`
	Viewport     *DesignReferenceViewport `json:"viewport,omitempty"`
	DocSize      *DesignReferenceViewport `json:"docSize,omitempty"`
	NodeCount    int                      `json:"nodeCount,omitempty"`
	AssetURLs    []string                 `json:"assetUrls,omitempty"`
	UserAgent    string                   `json:"userAgent,omitempty"`
	CapturedAt   string                   `json:"capturedAt,omitempty"`
	CreatedAt    string                   `json:"createdAt"`
	Notes        string                   `json:"notes,omitempty"`
}

type DesignReferenceViewport struct {
	W int `json:"w"`
	H int `json:"h"`
}

type DesignReferenceSummary struct {
	ID         string `json:"id"`
	URL        string `json:"url,omitempty"`
	Title      string `json:"title,omitempty"`
	Source     string `json:"source"`
	Mode       string `json:"mode"`
	NumScreens int    `json:"numScreenshots"`
	NodeCount  int    `json:"nodeCount"`
	CreatedAt  string `json:"createdAt"`
}

// incoming metadata posted by the extension — fields are optional and
// best-effort. The extension always supplies `mode`, `meta.url`, `meta.title`.
type designReferenceIncoming struct {
	Kind         string `json:"kind"`
	Source       string `json:"source"`
	Mode         string `json:"mode"`
	Selector     string `json:"selector"`
	CapturedAt   string `json:"capturedAt"`
	Notes        string `json:"notes"`
	Meta         struct {
		URL        string                  `json:"url"`
		Title      string                  `json:"title"`
		Viewport   DesignReferenceViewport `json:"viewport"`
		DocSize    DesignReferenceViewport `json:"docSize"`
		UserAgent  string                  `json:"userAgent"`
		CapturedAt string                  `json:"capturedAt"`
	} `json:"meta"`
}

// DesignReferenceManager owns the on-disk store of captured references.
// Mirrors FeedbackManager's pattern — separate baseDir so privacy/cleanup
// rules can be different from feedback bugs.
type DesignReferenceManager struct {
	mu      sync.RWMutex
	refs    map[string]*DesignReference
	baseDir string // ~/.yaver/design-references/
}

func NewDesignReferenceManager() (*DesignReferenceManager, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	baseDir := filepath.Join(dir, "design-references")
	if err := os.MkdirAll(baseDir, 0700); err != nil {
		return nil, err
	}
	m := &DesignReferenceManager{
		refs:    make(map[string]*DesignReference),
		baseDir: baseDir,
	}
	m.loadExisting()
	return m, nil
}

func (m *DesignReferenceManager) loadExisting() {
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(m.baseDir, e.Name(), "metadata.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var ref DesignReference
		if err := json.Unmarshal(data, &ref); err != nil {
			continue
		}
		m.refs[ref.ID] = &ref
	}
}

// ReceiveReference stores a new design reference. The metadata JSON is parsed
// best-effort; files are persisted under baseDir/<id>/ with sanitized basenames.
// The styles JSON ("styles") and HTML blob ("html") are recognized and indexed
// separately so they can be served directly without parsing metadata each time.
func (m *DesignReferenceManager) ReceiveReference(metadata json.RawMessage, files map[string][]byte) (*DesignReference, error) {
	var in designReferenceIncoming
	if err := json.Unmarshal(metadata, &in); err != nil {
		return nil, fmt.Errorf("invalid metadata: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	ref := &DesignReference{
		ID:           uuid.New().String()[:8],
		URL:          in.Meta.URL,
		Title:        in.Meta.Title,
		Source:       firstNonEmpty(in.Source, "browser-extension"),
		Mode:         firstNonEmpty(in.Mode, "viewport"),
		RootSelector: in.Selector,
		Viewport:     viewportOrNil(in.Meta.Viewport),
		DocSize:      viewportOrNil(in.Meta.DocSize),
		UserAgent:    in.Meta.UserAgent,
		CapturedAt:   firstNonEmpty(in.Meta.CapturedAt, in.CapturedAt, now),
		CreatedAt:    now,
		Notes:        in.Notes,
	}

	refDir := filepath.Join(m.baseDir, ref.ID)
	if err := os.MkdirAll(refDir, 0700); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}

	for name, data := range files {
		safe := sanitizeFeedbackUploadName(name)
		if safe == "" {
			log.Printf("[design-reference] rejecting unsafe filename %q", name)
			continue
		}
		filePath := filepath.Join(refDir, safe)
		if err := os.WriteFile(filePath, data, 0600); err != nil {
			log.Printf("[design-reference] write %s failed: %v", safe, err)
			continue
		}
		switch {
		case safe == "dom.html" || strings.HasSuffix(safe, ".html"):
			ref.HTMLPath = filePath
		case safe == "styles.json":
			ref.StylesPath = filePath
			// Pull nodeCount + assetUrls out of the styles blob so the
			// summary view can show them without re-reading the file.
			if nc, assets := extractStylesSummary(data); nc > 0 {
				ref.NodeCount = nc
				ref.AssetURLs = assets
			}
		case strings.HasSuffix(safe, ".png") || strings.HasSuffix(safe, ".jpg") || strings.HasSuffix(safe, ".jpeg") || strings.HasSuffix(safe, ".webp"):
			ref.Screenshots = append(ref.Screenshots, filePath)
		}
	}

	// Stable screenshot order — extension emits viewport.png + fullpage_<i>.png.
	sort.Strings(ref.Screenshots)

	m.mu.Lock()
	m.refs[ref.ID] = ref
	m.mu.Unlock()
	if err := m.writeMetadata(ref); err != nil {
		log.Printf("[design-reference] persist metadata failed: %v", err)
	}

	log.Printf("[design-reference] received %s url=%s mode=%s screenshots=%d nodes=%d",
		ref.ID, ref.URL, ref.Mode, len(ref.Screenshots), ref.NodeCount)
	return ref, nil
}

func (m *DesignReferenceManager) Get(id string) (*DesignReference, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.refs[id]
	return r, ok
}

func (m *DesignReferenceManager) List() []DesignReferenceSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]DesignReferenceSummary, 0, len(m.refs))
	for _, r := range m.refs {
		out = append(out, DesignReferenceSummary{
			ID:         r.ID,
			URL:        r.URL,
			Title:      r.Title,
			Source:     r.Source,
			Mode:       r.Mode,
			NumScreens: len(r.Screenshots),
			NodeCount:  r.NodeCount,
			CreatedAt:  r.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

func (m *DesignReferenceManager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.refs[id]; !ok {
		return fmt.Errorf("design reference %q not found", id)
	}
	delete(m.refs, id)
	return os.RemoveAll(filepath.Join(m.baseDir, id))
}

func (m *DesignReferenceManager) writeMetadata(ref *DesignReference) error {
	data, err := json.MarshalIndent(ref, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(m.baseDir, ref.ID, "metadata.json")
	return os.WriteFile(path, data, 0600)
}

func viewportOrNil(v DesignReferenceViewport) *DesignReferenceViewport {
	if v.W == 0 && v.H == 0 {
		return nil
	}
	return &v
}

// extractStylesSummary peeks at the styles.json blob the extension uploads
// and returns (nodeCount, assetUrls). Failures return (0, nil) — the value
// is only used for the summary view, never for serving content.
func extractStylesSummary(data []byte) (int, []string) {
	var s struct {
		Nodes  []json.RawMessage `json:"nodes"`
		Assets []string          `json:"assets"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return 0, nil
	}
	return len(s.Nodes), s.Assets
}
