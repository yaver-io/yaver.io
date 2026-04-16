package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hirochachacha/go-smb2"
)

type SharedStorageProfileView struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Type               string `json:"type"`
	Path               string `json:"path,omitempty"`
	MountPath          string `json:"mountPath,omitempty"`
	Remote             string `json:"remote,omitempty"`
	Endpoint           string `json:"endpoint,omitempty"`
	Bucket             string `json:"bucket,omitempty"`
	Region             string `json:"region,omitempty"`
	ReadOnly           bool   `json:"readOnly,omitempty"`
	Notes              string `json:"notes,omitempty"`
	Available          bool   `json:"available"`
	SupportsBrowse     bool   `json:"supportsBrowse"`
	SupportsRead       bool   `json:"supportsRead"`
	SupportsSearch     bool   `json:"supportsSearch"`
	ResolvedLocation   string `json:"resolvedLocation,omitempty"`
	Status             string `json:"status,omitempty"`
	ContainerMountMode string `json:"containerMountMode,omitempty"`
	ContainerPath      string `json:"containerPath,omitempty"`
	ContainerMountable bool   `json:"containerMountable,omitempty"`
}

type SharedStorageEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDir       bool   `json:"isDir"`
	Size        int64  `json:"size"`
	MTime       int64  `json:"mtime,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

type SharedStorageSearchHit struct {
	ProfileID   string `json:"profileId"`
	ProfileName string `json:"profileName"`
	Path        string `json:"path"`
	Size        int64  `json:"size,omitempty"`
	MTime       int64  `json:"mtime,omitempty"`
	MatchType   string `json:"matchType"`
	Snippet     string `json:"snippet,omitempty"`
}

func loadSharedStorageProfiles() ([]SharedStorageProfile, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	return cfg.SharedStorage, nil
}

func upsertSharedStorageProfile(profile SharedStorageProfile) (SharedStorageProfile, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return SharedStorageProfile{}, err
	}
	p, err := normalizeSharedStorageProfile(profile)
	if err != nil {
		return SharedStorageProfile{}, err
	}
	replaced := false
	for i := range cfg.SharedStorage {
		if cfg.SharedStorage[i].ID == p.ID {
			cfg.SharedStorage[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		cfg.SharedStorage = append(cfg.SharedStorage, p)
	}
	sort.Slice(cfg.SharedStorage, func(i, j int) bool {
		if cfg.SharedStorage[i].Name != cfg.SharedStorage[j].Name {
			return cfg.SharedStorage[i].Name < cfg.SharedStorage[j].Name
		}
		return cfg.SharedStorage[i].ID < cfg.SharedStorage[j].ID
	})
	if err := SaveConfig(cfg); err != nil {
		return SharedStorageProfile{}, err
	}
	return p, nil
}

func deleteSharedStorageProfile(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("profile id required")
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	next := make([]SharedStorageProfile, 0, len(cfg.SharedStorage))
	found := false
	for _, p := range cfg.SharedStorage {
		if p.ID == id {
			found = true
			continue
		}
		next = append(next, p)
	}
	if !found {
		return fmt.Errorf("profile not found")
	}
	cfg.SharedStorage = next
	return SaveConfig(cfg)
}

func normalizeSharedStorageProfile(profile SharedStorageProfile) (SharedStorageProfile, error) {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Type = strings.ToLower(strings.TrimSpace(profile.Type))
	profile.Path = strings.TrimSpace(profile.Path)
	profile.MountPath = strings.TrimSpace(profile.MountPath)
	profile.Remote = strings.TrimSpace(profile.Remote)
	profile.Endpoint = strings.TrimSpace(profile.Endpoint)
	profile.Bucket = strings.TrimSpace(profile.Bucket)
	profile.Region = strings.TrimSpace(profile.Region)
	profile.Username = strings.TrimSpace(profile.Username)
	profile.Notes = strings.TrimSpace(profile.Notes)
	profile.ContainerMountMode = strings.ToLower(strings.TrimSpace(profile.ContainerMountMode))
	profile.ContainerPath = strings.TrimSpace(profile.ContainerPath)
	if profile.ID == "" {
		profile.ID = fmt.Sprintf("shared-%d", time.Now().UnixNano())
	}
	if profile.Name == "" {
		return SharedStorageProfile{}, fmt.Errorf("name required")
	}
	switch profile.Type {
	case "local", "smb", "webdav", "storagebox", "s3":
	default:
		return SharedStorageProfile{}, fmt.Errorf("unsupported type %q", profile.Type)
	}
	if profile.Type == "s3" {
		if profile.Endpoint == "" || profile.Bucket == "" {
			return SharedStorageProfile{}, fmt.Errorf("s3 profiles require endpoint and bucket")
		}
	} else if (profile.Type == "smb" || profile.Type == "storagebox") && firstNonEmpty(profile.Remote, profile.Endpoint) == "" {
		return SharedStorageProfile{}, fmt.Errorf("%s profiles require remote like //host/share", profile.Type)
	} else if profile.Type == "webdav" && firstNonEmpty(profile.Endpoint, profile.Remote) == "" {
		return SharedStorageProfile{}, fmt.Errorf("webdav profiles require endpoint")
	} else if profile.Type == "local" && sharedStorageResolvedPath(profile) == "" {
		return SharedStorageProfile{}, fmt.Errorf("%s profiles require path or mount_path", profile.Type)
	}
	switch profile.ContainerMountMode {
	case "", "none", "host", "guests", "all":
	default:
		return SharedStorageProfile{}, fmt.Errorf("unsupported container_mount_mode %q", profile.ContainerMountMode)
	}
	return profile, nil
}

func getSharedStorageProfile(id string) (*SharedStorageProfile, error) {
	profiles, err := loadSharedStorageProfiles()
	if err != nil {
		return nil, err
	}
	for i := range profiles {
		if profiles[i].ID == id {
			return &profiles[i], nil
		}
	}
	return nil, fmt.Errorf("profile not found")
}

func sharedStorageResolvedPath(profile SharedStorageProfile) string {
	switch profile.Type {
	case "smb", "webdav", "storagebox":
		return strings.TrimSpace(profile.Path)
	}
	if strings.TrimSpace(profile.MountPath) != "" {
		return strings.TrimSpace(profile.MountPath)
	}
	return strings.TrimSpace(profile.Path)
}

func sharedStorageCanBrowse(profile SharedStorageProfile) bool {
	if profile.Type == "s3" {
		return true
	}
	if profile.Type == "smb" || profile.Type == "webdav" || profile.Type == "storagebox" {
		return firstNonEmpty(profile.Remote, profile.Endpoint) != ""
	}
	return sharedStorageResolvedPath(profile) != ""
}

func sharedStorageCanRead(profile SharedStorageProfile) bool {
	if profile.Type == "s3" {
		return false
	}
	if profile.Type == "smb" || profile.Type == "webdav" || profile.Type == "storagebox" {
		return firstNonEmpty(profile.Remote, profile.Endpoint) != ""
	}
	return sharedStorageResolvedPath(profile) != ""
}

func sharedStorageCanSearch(profile SharedStorageProfile) bool {
	return sharedStorageCanBrowse(profile)
}

func sharedStorageContainerSourcePath(profile SharedStorageProfile) string {
	if strings.TrimSpace(profile.MountPath) != "" {
		return strings.TrimSpace(profile.MountPath)
	}
	if profile.Type == "local" {
		return strings.TrimSpace(profile.Path)
	}
	return ""
}

func sharedStorageContainerTargetPath(profile SharedStorageProfile) string {
	if strings.TrimSpace(profile.ContainerPath) != "" {
		return strings.TrimSpace(profile.ContainerPath)
	}
	return "/mnt/yaver-shared/" + strings.ToLower(strings.ReplaceAll(profile.ID, " ", "-"))
}

func sharedStorageContainerMountable(profile SharedStorageProfile) bool {
	src := sharedStorageContainerSourcePath(profile)
	if src == "" || profile.ContainerMountMode == "" || profile.ContainerMountMode == "none" {
		return false
	}
	st, err := os.Stat(src)
	return err == nil && st.IsDir()
}

func sharedStorageView(profile SharedStorageProfile) SharedStorageProfileView {
	view := SharedStorageProfileView{
		ID:                 profile.ID,
		Name:               profile.Name,
		Type:               profile.Type,
		Path:               profile.Path,
		MountPath:          profile.MountPath,
		Remote:             profile.Remote,
		Endpoint:           profile.Endpoint,
		Bucket:             profile.Bucket,
		Region:             profile.Region,
		ReadOnly:           profile.ReadOnly,
		Notes:              profile.Notes,
		SupportsBrowse:     sharedStorageCanBrowse(profile),
		SupportsRead:       sharedStorageCanRead(profile),
		SupportsSearch:     sharedStorageCanSearch(profile),
		ResolvedLocation:   sharedStorageResolvedPath(profile),
		ContainerMountMode: profile.ContainerMountMode,
		ContainerPath:      sharedStorageContainerTargetPath(profile),
		ContainerMountable: sharedStorageContainerMountable(profile),
	}
	switch profile.Type {
	case "s3":
		view.Available = true
		view.ResolvedLocation = strings.TrimRight(profile.Endpoint, "/") + "/" + profile.Bucket
		view.Status = "object storage"
	case "smb", "storagebox":
		view.Available = firstNonEmpty(profile.Remote, profile.Endpoint) != ""
		view.ResolvedLocation = firstNonEmpty(profile.Remote, profile.Endpoint)
		if profile.Path != "" {
			view.ResolvedLocation += "/" + strings.TrimPrefix(profile.Path, "/")
		}
		if view.Available {
			view.Status = "native smb"
		} else {
			view.Status = "remote/share not configured"
		}
	case "webdav":
		view.Available = firstNonEmpty(profile.Endpoint, profile.Remote) != ""
		view.ResolvedLocation = strings.TrimRight(firstNonEmpty(profile.Endpoint, profile.Remote), "/")
		if profile.Path != "" {
			view.ResolvedLocation += "/" + strings.TrimPrefix(profile.Path, "/")
		}
		if view.Available {
			view.Status = "native webdav"
		} else {
			view.Status = "endpoint not configured"
		}
	default:
		root := sharedStorageResolvedPath(profile)
		if root == "" {
			view.Status = "not configured"
			return view
		}
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			view.Available = true
			view.Status = "mounted"
		} else if err != nil {
			view.Status = err.Error()
		} else {
			view.Status = "not a directory"
		}
	}
	return view
}

func listSharedStorageProfilesView() ([]SharedStorageProfileView, error) {
	profiles, err := loadSharedStorageProfiles()
	if err != nil {
		return nil, err
	}
	views := make([]SharedStorageProfileView, 0, len(profiles))
	for _, p := range profiles {
		views = append(views, sharedStorageView(p))
	}
	return views, nil
}

func listSharedStorageProfilesForUser(guestUserID string, guestMgr *GuestConfigManager) ([]SharedStorageProfileView, error) {
	views, err := listSharedStorageProfilesView()
	if err != nil {
		return nil, err
	}
	if guestUserID == "" || guestMgr == nil {
		return views, nil
	}
	allowedIDs := guestMgr.GetSharedStorageAccess(guestUserID)
	if len(allowedIDs) == 0 {
		return []SharedStorageProfileView{}, nil
	}
	allowed := map[string]bool{}
	for _, id := range allowedIDs {
		allowed[id] = true
	}
	out := make([]SharedStorageProfileView, 0, len(views))
	for _, view := range views {
		if allowed[view.ID] {
			out = append(out, view)
		}
	}
	return out, nil
}

func listSharedStorageEntries(profile SharedStorageProfile, sub string) ([]SharedStorageEntry, error) {
	if profile.Type == "s3" {
		prefix := strings.Trim(strings.TrimSpace(sub), "/")
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		files, err := ListObjects(ObjectStorage{
			Endpoint:  profile.Endpoint,
			Bucket:    profile.Bucket,
			AccessKey: profile.AccessKey,
			SecretKey: profile.SecretKey,
			Region:    profile.Region,
		}, prefix, 200)
		if err != nil {
			return nil, err
		}
		out := make([]SharedStorageEntry, 0, len(files))
		for _, f := range files {
			out = append(out, SharedStorageEntry{
				Name:  filepath.Base(f.Key),
				Path:  f.Key,
				IsDir: false,
				Size:  f.Size,
				MTime: f.LastModified.UnixMilli(),
			})
		}
		return out, nil
	}
	if profile.Type == "smb" || profile.Type == "storagebox" {
		return smbListSharedStorageEntries(profile, sub)
	}
	if profile.Type == "webdav" {
		return webdavListSharedStorageEntries(profile, sub)
	}

	root := sharedStorageResolvedPath(profile)
	abs, ok := safeJoin(root, strings.TrimPrefix(sub, "/"))
	if !ok {
		return nil, fmt.Errorf("path escapes root")
	}
	infos, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]SharedStorageEntry, 0, len(infos))
	for _, fi := range infos {
		info, err := fi.Info()
		if err != nil {
			continue
		}
		name := fi.Name()
		out = append(out, SharedStorageEntry{
			Name:  name,
			Path:  filepath.Join(strings.TrimPrefix(sub, "/"), name),
			IsDir: fi.IsDir(),
			Size:  info.Size(),
			MTime: info.ModTime().UnixMilli(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func readSharedStorageFile(profile SharedStorageProfile, sub string) (map[string]interface{}, error) {
	if profile.Type == "smb" || profile.Type == "storagebox" {
		data, err := smbReadSharedStorageFile(profile, sub, MaxReadableFileSize)
		if err != nil {
			return nil, err
		}
		return sharedStorageReadResponse(data), nil
	}
	if profile.Type == "webdav" {
		data, err := webdavReadSharedStorageFile(profile, sub, MaxReadableFileSize)
		if err != nil {
			return nil, err
		}
		return sharedStorageReadResponse(data), nil
	}
	if !sharedStorageCanRead(profile) {
		return nil, fmt.Errorf("profile does not support file reads")
	}
	root := sharedStorageResolvedPath(profile)
	abs, ok := safeJoin(root, strings.TrimPrefix(sub, "/"))
	if !ok {
		return nil, fmt.Errorf("path escapes root")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("is a directory")
	}
	truncated := false
	readSize := info.Size()
	if readSize > MaxReadableFileSize {
		readSize = MaxReadableFileSize
		truncated = true
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, readSize)
	n, _ := f.Read(buf)
	buf = buf[:n]
	if looksBinary(buf) {
		return map[string]interface{}{
			"ok":        true,
			"binary":    true,
			"size":      info.Size(),
			"truncated": truncated,
		}, nil
	}
	return map[string]interface{}{
		"ok":        true,
		"content":   string(buf),
		"size":      info.Size(),
		"truncated": truncated,
	}, nil
}

func searchSharedStorage(profile SharedStorageProfile, query, sub string, limit int) ([]SharedStorageSearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query required")
	}
	if limit <= 0 {
		limit = 25
	}
	q := strings.ToLower(query)

	if profile.Type == "s3" {
		files, err := ListObjects(ObjectStorage{
			Endpoint:  profile.Endpoint,
			Bucket:    profile.Bucket,
			AccessKey: profile.AccessKey,
			SecretKey: profile.SecretKey,
			Region:    profile.Region,
		}, strings.Trim(strings.TrimSpace(sub), "/"), 500)
		if err != nil {
			return nil, err
		}
		hits := make([]SharedStorageSearchHit, 0, limit)
		for _, f := range files {
			if !strings.Contains(strings.ToLower(f.Key), q) {
				continue
			}
			hits = append(hits, SharedStorageSearchHit{
				ProfileID:   profile.ID,
				ProfileName: profile.Name,
				Path:        f.Key,
				Size:        f.Size,
				MTime:       f.LastModified.UnixMilli(),
				MatchType:   "name",
			})
			if len(hits) >= limit {
				break
			}
		}
		return hits, nil
	}
	if profile.Type == "smb" || profile.Type == "storagebox" {
		return searchRemoteSharedStorage(profile, query, sub, limit, smbListSharedStorageEntries, smbReadSharedStorageFile)
	}
	if profile.Type == "webdav" {
		return searchRemoteSharedStorage(profile, query, sub, limit, webdavListSharedStorageEntries, webdavReadSharedStorageFile)
	}

	root := sharedStorageResolvedPath(profile)
	abs, ok := safeJoin(root, strings.TrimPrefix(sub, "/"))
	if !ok {
		return nil, fmt.Errorf("path escapes root")
	}
	hits := make([]SharedStorageSearchHit, 0, limit)
	scanned := 0
	err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(hits) >= limit || scanned >= 2000 {
			return fs.SkipAll
		}
		if d.IsDir() {
			return nil
		}
		scanned++
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = d.Name()
		}
		rel = filepath.ToSlash(rel)
		nameMatch := strings.Contains(strings.ToLower(rel), q)
		if nameMatch {
			hits = append(hits, SharedStorageSearchHit{
				ProfileID:   profile.ID,
				ProfileName: profile.Name,
				Path:        rel,
				Size:        info.Size(),
				MTime:       info.ModTime().UnixMilli(),
				MatchType:   "name",
			})
			if len(hits) >= limit {
				return fs.SkipAll
			}
		}
		if !sharedStorageTextSearchable(path) {
			return nil
		}
		snippet, ok := searchTextFile(path, q)
		if !ok {
			return nil
		}
		hits = append(hits, SharedStorageSearchHit{
			ProfileID:   profile.ID,
			ProfileName: profile.Name,
			Path:        rel,
			Size:        info.Size(),
			MTime:       info.ModTime().UnixMilli(),
			MatchType:   "content",
			Snippet:     snippet,
		})
		if len(hits) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	return hits, nil
}

func sharedStorageTextSearchable(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md", ".markdown", ".json", ".jsonl", ".yaml", ".yml", ".csv", ".tsv", ".xml", ".html", ".htm", ".js", ".ts", ".tsx", ".jsx", ".go", ".py", ".rb", ".java", ".kt", ".rs", ".sh", ".env", ".log":
		return true
	default:
		return false
	}
}

func searchTextFile(path, q string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf := make([]byte, 256*1024)
	n, _ := f.Read(buf)
	buf = buf[:n]
	if looksBinary(buf) {
		return "", false
	}
	text := string(buf)
	lower := strings.ToLower(text)
	idx := strings.Index(lower, q)
	if idx < 0 {
		return "", false
	}
	start := idx - 80
	if start < 0 {
		start = 0
	}
	end := idx + len(q) + 120
	if end > len(text) {
		end = len(text)
	}
	return strings.TrimSpace(strings.ReplaceAll(text[start:end], "\n", " ")), true
}

func searchRemoteSharedStorage(profile SharedStorageProfile, query, sub string, limit int, listFn func(SharedStorageProfile, string) ([]SharedStorageEntry, error), readFn func(SharedStorageProfile, string, int64) ([]byte, error)) ([]SharedStorageSearchHit, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, fmt.Errorf("query required")
	}
	type queueItem struct{ path string }
	queue := []queueItem{{path: strings.Trim(strings.TrimSpace(sub), "/")}}
	hits := make([]SharedStorageSearchHit, 0, limit)
	visited := 0
	for len(queue) > 0 && len(hits) < limit && visited < 200 {
		item := queue[0]
		queue = queue[1:]
		entries, err := listFn(profile, item.path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if len(hits) >= limit {
				break
			}
			if entry.IsDir {
				queue = append(queue, queueItem{path: entry.Path})
				continue
			}
			visited++
			if strings.Contains(strings.ToLower(entry.Path), q) {
				hits = append(hits, SharedStorageSearchHit{
					ProfileID:   profile.ID,
					ProfileName: profile.Name,
					Path:        entry.Path,
					Size:        entry.Size,
					MTime:       entry.MTime,
					MatchType:   "name",
				})
				if len(hits) >= limit {
					break
				}
			}
			if !sharedStorageTextSearchable(entry.Path) {
				continue
			}
			data, err := readFn(profile, entry.Path, 256*1024)
			if err != nil || looksBinary(data) {
				continue
			}
			text := string(data)
			lower := strings.ToLower(text)
			idx := strings.Index(lower, q)
			if idx < 0 {
				continue
			}
			start := idx - 80
			if start < 0 {
				start = 0
			}
			end := idx + len(q) + 120
			if end > len(text) {
				end = len(text)
			}
			hits = append(hits, SharedStorageSearchHit{
				ProfileID:   profile.ID,
				ProfileName: profile.Name,
				Path:        entry.Path,
				Size:        entry.Size,
				MTime:       entry.MTime,
				MatchType:   "content",
				Snippet:     strings.TrimSpace(strings.ReplaceAll(text[start:end], "\n", " ")),
			})
		}
	}
	return hits, nil
}

func sharedStorageReadResponse(data []byte) map[string]interface{} {
	if looksBinary(data) {
		return map[string]interface{}{
			"ok":     true,
			"binary": true,
			"size":   len(data),
		}
	}
	return map[string]interface{}{
		"ok":      true,
		"content": string(data),
		"size":    len(data),
	}
}

func normalizeRemoteSubpath(base, sub string) string {
	parts := []string{}
	for _, p := range []string{base, sub} {
		p = strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/")
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "/")
}

func parseSMBRemote(profile SharedStorageProfile) (host, share string, port int, err error) {
	remote := firstNonEmpty(profile.Remote, profile.Endpoint)
	if remote == "" {
		return "", "", 0, fmt.Errorf("smb remote required, expected //host/share")
	}
	port = 445
	if strings.HasPrefix(remote, "//") {
		remote = "smb:" + remote
	}
	if strings.HasPrefix(remote, "smb://") || strings.HasPrefix(remote, "smb:") {
		u, perr := url.Parse(remote)
		if perr != nil {
			return "", "", 0, perr
		}
		host = u.Hostname()
		if u.Port() != "" {
			fmt.Sscanf(u.Port(), "%d", &port)
		}
		share = strings.Trim(strings.TrimPrefix(u.Path, "/"), "/")
		if i := strings.Index(share, "/"); i >= 0 {
			share = share[:i]
		}
	} else {
		trimmed := strings.TrimPrefix(remote, "//")
		parts := strings.Split(strings.ReplaceAll(trimmed, "\\", "/"), "/")
		if len(parts) < 2 {
			return "", "", 0, fmt.Errorf("invalid smb remote %q", remote)
		}
		host = parts[0]
		share = parts[1]
	}
	if host == "" || share == "" {
		return "", "", 0, fmt.Errorf("invalid smb remote %q", remote)
	}
	return host, share, port, nil
}

func withSMBShare(profile SharedStorageProfile, fn func(*smb2.Share) error) error {
	host, share, port, err := parseSMBRemote(profile)
	if err != nil {
		return err
	}
	conn, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(context.Background(), "tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return err
	}
	defer conn.Close()
	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     profile.Username,
			Password: profile.Password,
		},
	}
	sess, err := d.Dial(conn)
	if err != nil {
		return err
	}
	defer sess.Logoff()
	fs, err := sess.Mount(share)
	if err != nil {
		return err
	}
	defer fs.Umount()
	return fn(fs)
}

func smbListSharedStorageEntries(profile SharedStorageProfile, sub string) ([]SharedStorageEntry, error) {
	full := normalizeRemoteSubpath(profile.Path, sub)
	var out []SharedStorageEntry
	err := withSMBShare(profile, func(fs *smb2.Share) error {
		infos, err := fs.ReadDir(full)
		if err != nil {
			return err
		}
		out = make([]SharedStorageEntry, 0, len(infos))
		for _, info := range infos {
			out = append(out, SharedStorageEntry{
				Name:  info.Name(),
				Path:  normalizeRemoteSubpath(sub, info.Name()),
				IsDir: info.IsDir(),
				Size:  info.Size(),
				MTime: info.ModTime().UnixMilli(),
			})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].IsDir != out[j].IsDir {
				return out[i].IsDir
			}
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
		return nil
	})
	return out, err
}

func smbReadSharedStorageFile(profile SharedStorageProfile, sub string, maxBytes int64) ([]byte, error) {
	full := normalizeRemoteSubpath(profile.Path, sub)
	var data []byte
	err := withSMBShare(profile, func(fs *smb2.Share) error {
		f, err := fs.Open(full)
		if err != nil {
			return err
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, maxBytes+1))
		if err != nil {
			return err
		}
		if int64(len(data)) > maxBytes {
			data = data[:maxBytes]
		}
		return nil
	})
	return data, err
}

type webdavMultiStatus struct {
	Responses []webdavResponse `xml:"response"`
}

type webdavResponse struct {
	Href     string             `xml:"href"`
	Propstat []webdavPropStatus `xml:"propstat"`
}

type webdavPropStatus struct {
	Prop webdavProp `xml:"prop"`
}

type webdavProp struct {
	DisplayName   string `xml:"displayname"`
	ContentLength int64  `xml:"getcontentlength"`
	ContentType   string `xml:"getcontenttype"`
	LastModified  string `xml:"getlastmodified"`
	Collection    string `xml:"resourcetype>collection"`
}

func webdavBaseURL(profile SharedStorageProfile, sub string) (string, error) {
	base := strings.TrimRight(firstNonEmpty(profile.Endpoint, profile.Remote), "/")
	if base == "" {
		return "", fmt.Errorf("webdav endpoint required")
	}
	if profile.Path != "" {
		base += "/" + strings.Trim(strings.TrimPrefix(profile.Path, "/"), "/")
	}
	if sub != "" {
		base += "/" + strings.Trim(strings.TrimPrefix(sub, "/"), "/")
	}
	return base, nil
}

func webdavRequest(profile SharedStorageProfile, method, sub string, body []byte, depth string) (*http.Response, error) {
	u, err := webdavBaseURL(profile, sub)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if profile.Username != "" || profile.Password != "" {
		req.SetBasicAuth(profile.Username, profile.Password)
	}
	if depth != "" {
		req.Header.Set("Depth", depth)
	}
	if method == "PROPFIND" {
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	}
	return provisionHTTP.Do(req)
}

func webdavListSharedStorageEntries(profile SharedStorageProfile, sub string) ([]SharedStorageEntry, error) {
	body := []byte(`<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><displayname/><resourcetype/><getcontentlength/><getlastmodified/><getcontenttype/></prop></propfind>`)
	res, err := webdavRequest(profile, "PROPFIND", sub, body, "1")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("webdav list: %s", strings.TrimSpace(string(msg)))
	}
	var ms webdavMultiStatus
	if err := xml.NewDecoder(res.Body).Decode(&ms); err != nil {
		return nil, err
	}
	out := []SharedStorageEntry{}
	for i, resp := range ms.Responses {
		if i == 0 {
			continue
		}
		name := pathBaseFromHref(resp.Href)
		if name == "" {
			continue
		}
		prop := firstWebdavProp(resp)
		mtime := int64(0)
		if t, err := http.ParseTime(prop.LastModified); err == nil {
			mtime = t.UnixMilli()
		}
		out = append(out, SharedStorageEntry{
			Name:        name,
			Path:        normalizeRemoteSubpath(sub, name),
			IsDir:       prop.Collection != "",
			Size:        prop.ContentLength,
			MTime:       mtime,
			ContentType: prop.ContentType,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func webdavReadSharedStorageFile(profile SharedStorageProfile, sub string, maxBytes int64) ([]byte, error) {
	res, err := webdavRequest(profile, http.MethodGet, sub, nil, "")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("webdav read: %s", strings.TrimSpace(string(msg)))
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		data = data[:maxBytes]
	}
	return data, nil
}

func firstWebdavProp(resp webdavResponse) webdavProp {
	for _, p := range resp.Propstat {
		return p.Prop
	}
	return webdavProp{}
}

func pathBaseFromHref(href string) string {
	href, _ = url.PathUnescape(href)
	href = strings.TrimRight(href, "/")
	if href == "" {
		return ""
	}
	parts := strings.Split(href, "/")
	return parts[len(parts)-1]
}

func mcpSharedStorageProfiles() interface{} {
	views, err := listSharedStorageProfilesView()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"profiles": views}
}

func mcpSharedStorageUpsert(profileJSON string) interface{} {
	var profile SharedStorageProfile
	if err := json.Unmarshal([]byte(profileJSON), &profile); err != nil {
		return map[string]interface{}{"error": fmt.Sprintf("invalid profile json: %v", err)}
	}
	saved, err := upsertSharedStorageProfile(profile)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true, "profile": sharedStorageView(saved)}
}

func mcpSharedStorageDelete(id string) interface{} {
	if err := deleteSharedStorageProfile(id); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true}
}

func mcpSharedStorageList(id, sub string) interface{} {
	profile, err := getSharedStorageProfile(id)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	entries, err := listSharedStorageEntries(*profile, sub)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{
		"profile": sharedStorageView(*profile),
		"path":    strings.TrimPrefix(sub, "/"),
		"entries": entries,
	}
}

func mcpSharedStorageSearch(id, query, sub string, limit int) interface{} {
	profile, err := getSharedStorageProfile(id)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	hits, err := searchSharedStorage(*profile, query, sub, limit)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{
		"profile": sharedStorageView(*profile),
		"hits":    hits,
	}
}

func sharedStorageGuestDenied(r *http.Request, guestMgr *GuestConfigManager, profileID string) *AccessDeniedReason {
	if guestMgr == nil {
		return nil
	}
	guestUserID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID"))
	if guestUserID == "" {
		return nil
	}
	return guestMgr.CheckSharedStorage(guestUserID, profileID)
}

func sharedStorageContainerMountsForTask(guestUserID string, guestMgr *GuestConfigManager) ([]string, error) {
	profiles, err := loadSharedStorageProfiles()
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, profile := range profiles {
		mode := profile.ContainerMountMode
		if mode == "" || mode == "none" {
			continue
		}
		if guestUserID == "" {
			if mode != "host" && mode != "all" {
				continue
			}
		} else {
			if mode != "guests" && mode != "all" {
				continue
			}
			if guestMgr == nil || guestMgr.CheckSharedStorage(guestUserID, profile.ID) != nil {
				continue
			}
		}
		src := sharedStorageContainerSourcePath(profile)
		if src == "" {
			continue
		}
		st, err := os.Stat(src)
		if err != nil || !st.IsDir() {
			continue
		}
		target := sharedStorageContainerTargetPath(profile)
		mount := fmt.Sprintf("%s:%s", src, target)
		if profile.ReadOnly || guestUserID != "" {
			mount += ":ro"
		}
		out = append(out, mount)
	}
	sort.Strings(out)
	return out, nil
}

func (s *HTTPServer) handleSharedStorageProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		views, err := listSharedStorageProfilesForUser(strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID")), s.guestConfigMgr)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "profiles": views})
	case http.MethodPost:
		if strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID")) != "" {
			jsonError(w, http.StatusForbidden, "guests cannot manage shared storage profiles")
			return
		}
		var profile SharedStorageProfile
		if err := json.NewDecoder(r.Body).Decode(&profile); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json")
			return
		}
		saved, err := upsertSharedStorageProfile(profile)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "profile": sharedStorageView(saved)})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

func (s *HTTPServer) handleSharedStorageDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID")) != "" {
		jsonError(w, http.StatusForbidden, "guests cannot manage shared storage profiles")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := deleteSharedStorageProfile(body.ID); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleSharedStorageList(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if denied := sharedStorageGuestDenied(r, s.guestConfigMgr, id); denied != nil {
		jsonError(w, http.StatusForbidden, denied.Reason)
		return
	}
	profile, err := getSharedStorageProfile(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	sub := r.URL.Query().Get("path")
	entries, err := listSharedStorageEntries(*profile, sub)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"profile": sharedStorageView(*profile),
		"path":    strings.TrimPrefix(sub, "/"),
		"entries": entries,
	})
}

func (s *HTTPServer) handleSharedStorageRead(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if denied := sharedStorageGuestDenied(r, s.guestConfigMgr, id); denied != nil {
		jsonError(w, http.StatusForbidden, denied.Reason)
		return
	}
	profile, err := getSharedStorageProfile(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	sub := r.URL.Query().Get("path")
	out, err := readSharedStorageFile(*profile, sub)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, out)
}

func (s *HTTPServer) handleSharedStorageRaw(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if denied := sharedStorageGuestDenied(r, s.guestConfigMgr, id); denied != nil {
		jsonError(w, http.StatusForbidden, denied.Reason)
		return
	}
	profile, err := getSharedStorageProfile(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, err.Error())
		return
	}
	if !sharedStorageCanRead(*profile) {
		jsonError(w, http.StatusBadRequest, "profile does not support raw file reads")
		return
	}
	sub := strings.TrimPrefix(r.URL.Query().Get("path"), "/")
	if profile.Type == "smb" || profile.Type == "storagebox" {
		data, err := smbReadSharedStorageFile(*profile, sub, 20<<20)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(sub)))
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "private, max-age=300")
		_, _ = w.Write(data)
		return
	}
	if profile.Type == "webdav" {
		data, err := webdavReadSharedStorageFile(*profile, sub, 20<<20)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(sub)))
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "private, max-age=300")
		_, _ = w.Write(data)
		return
	}
	root := sharedStorageResolvedPath(*profile)
	abs, ok := safeJoin(root, sub)
	if !ok {
		jsonError(w, http.StatusBadRequest, "path escapes root")
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		jsonError(w, http.StatusNotFound, "not a file")
		return
	}
	const maxRaw = 20 << 20
	if info.Size() > maxRaw {
		jsonError(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	ct := "application/octet-stream"
	switch strings.ToLower(filepath.Ext(abs)) {
	case ".png":
		ct = "image/png"
	case ".jpg", ".jpeg":
		ct = "image/jpeg"
	case ".gif":
		ct = "image/gif"
	case ".webp":
		ct = "image/webp"
	case ".svg":
		ct = "image/svg+xml"
	case ".bmp":
		ct = "image/bmp"
	case ".pdf":
		ct = "application/pdf"
	case ".txt", ".md", ".json", ".log", ".csv":
		ct = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeFile(w, r, abs)
}

func (s *HTTPServer) handleSharedStorageSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	sub := r.URL.Query().Get("path")
	limit := 25
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		fmt.Sscanf(raw, "%d", &limit)
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id != "" {
		if denied := sharedStorageGuestDenied(r, s.guestConfigMgr, id); denied != nil {
			jsonError(w, http.StatusForbidden, denied.Reason)
			return
		}
		profile, err := getSharedStorageProfile(id)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		hits, err := searchSharedStorage(*profile, query, sub, limit)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "hits": hits, "profile": sharedStorageView(*profile)})
		return
	}

	profiles, err := loadSharedStorageProfiles()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	all := make([]SharedStorageSearchHit, 0, limit)
	for _, profile := range profiles {
		if denied := sharedStorageGuestDenied(r, s.guestConfigMgr, profile.ID); denied != nil {
			continue
		}
		hits, err := searchSharedStorage(profile, query, sub, limit-len(all))
		if err != nil {
			continue
		}
		all = append(all, hits...)
		if len(all) >= limit {
			break
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "hits": all})
}
