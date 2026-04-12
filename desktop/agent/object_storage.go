package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ObjectStorage describes a connected S3-compatible bucket. Zero-config mode
// defaults to the local MinIO that Yaver's services preset spins up.
type ObjectStorage struct {
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Region    string `json:"region,omitempty"`
}

// ObjectFile is a universal entry returned by ListObjects.
type ObjectFile struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"lastModified,omitempty"`
	ETag         string    `json:"etag,omitempty"`
}

// applyS3Auth picks the right auth scheme for the storage target:
// - MinIO default (minioadmin): basic auth (MinIO accepts it for root)
// - Anything else (AWS, R2, B2): SigV4 with extracted region
func applyS3Auth(req *http.Request, s ObjectStorage, body []byte) {
	// MinIO default credentials — basic auth works.
	if s.AccessKey == "minioadmin" && strings.Contains(s.Endpoint, "127.0.0.1") {
		req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
		req.SetBasicAuth(s.AccessKey, s.SecretKey)
		return
	}
	region := s.Region
	if region == "" {
		region = regionFromS3URL(s.Endpoint)
	}
	if region == "" {
		region = "auto"
	}
	signSigV4(req, s.AccessKey, s.SecretKey, region, "s3", body)
}

// defaultLocalStorage points at Yaver's local MinIO preset.
func defaultLocalStorage(bucket string) ObjectStorage {
	if bucket == "" {
		bucket = "yaver"
	}
	return ObjectStorage{
		Endpoint: "http://127.0.0.1:9000", Bucket: bucket,
		AccessKey: "minioadmin", SecretKey: "minioadmin",
	}
}

// ListObjects returns keys under a prefix. Uses S3's v1 ListBucket API which
// works against MinIO, AWS, R2, B2 — no SDK needed, a simple signed GET.
// We use path-style addressing (bucket in path) which is what MinIO wants.
func ListObjects(s ObjectStorage, prefix string, limit int) ([]ObjectFile, error) {
	if limit <= 0 {
		limit = 100
	}
	u := strings.TrimRight(s.Endpoint, "/") + "/" + url.PathEscape(s.Bucket) +
		"?list-type=2&max-keys=" + fmt.Sprint(limit)
	if prefix != "" {
		u += "&prefix=" + url.QueryEscape(prefix)
	}
	req, _ := http.NewRequest("GET", u, nil)
	// MinIO with default creds accepts unsigned requests when built-in policy
	// allows it; for AWS/R2 we'd need SigV4. Solo-dev mode uses MinIO locally,
	// so we try unsigned first, then fall back to basic-auth-ish query creds.
	applyS3Auth(req, s, nil)
	res, err := provisionHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("s3 list %s: %d %s", s.Bucket, res.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		XMLName  xml.Name `xml:"ListBucketResult"`
		Contents []struct {
			Key          string    `xml:"Key"`
			Size         int64     `xml:"Size"`
			LastModified time.Time `xml:"LastModified"`
			ETag         string    `xml:"ETag"`
		} `xml:"Contents"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	out := make([]ObjectFile, 0, len(parsed.Contents))
	for _, c := range parsed.Contents {
		out = append(out, ObjectFile{Key: c.Key, Size: c.Size, LastModified: c.LastModified, ETag: c.ETag})
	}
	return out, nil
}

// UploadObject PUTs bytes at <endpoint>/<bucket>/<key>.
func UploadObject(s ObjectStorage, key string, body []byte, contentType string) error {
	u := strings.TrimRight(s.Endpoint, "/") + "/" + url.PathEscape(s.Bucket) + "/" + key
	req, _ := http.NewRequest("PUT", u, bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	applyS3Auth(req, s, body)
	res, err := provisionHTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		data, _ := io.ReadAll(res.Body)
		return fmt.Errorf("s3 upload: %d %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

// DeleteObject removes a key.
func DeleteObject(s ObjectStorage, key string) error {
	u := strings.TrimRight(s.Endpoint, "/") + "/" + url.PathEscape(s.Bucket) + "/" + key
	req, _ := http.NewRequest("DELETE", u, nil)
	applyS3Auth(req, s, nil)
	res, err := provisionHTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		data, _ := io.ReadAll(res.Body)
		return fmt.Errorf("s3 delete: %d %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

// ---- HTTP ----

func loadStorageFromQuery(r *http.Request) ObjectStorage {
	q := r.URL.Query()
	s := defaultLocalStorage(q.Get("bucket"))
	if ep := q.Get("endpoint"); ep != "" {
		s.Endpoint = ep
	}
	if ak := q.Get("accessKey"); ak != "" {
		s.AccessKey = ak
	}
	if sk := q.Get("secretKey"); sk != "" {
		s.SecretKey = sk
	}
	// Also accept env fallbacks.
	if s.AccessKey == "" {
		s.AccessKey = os.Getenv("AWS_ACCESS_KEY_ID")
	}
	if s.SecretKey == "" {
		s.SecretKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	}
	return s
}

func (srv *HTTPServer) handleObjectList(w http.ResponseWriter, r *http.Request) {
	s := loadStorageFromQuery(r)
	prefix := r.URL.Query().Get("prefix")
	files, err := ListObjects(s, prefix, 200)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"bucket": s.Bucket, "files": files})
}

func (srv *HTTPServer) handleObjectUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		jsonError(w, http.StatusBadRequest, "key required")
		return
	}
	s := loadStorageFromQuery(r)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	if err := UploadObject(s, key, body, r.Header.Get("Content-Type")); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "key": key, "size": len(body)})
}

func (srv *HTTPServer) handleObjectDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Key       string `json:"key"`
		Bucket    string `json:"bucket"`
		Endpoint  string `json:"endpoint"`
		AccessKey string `json:"accessKey"`
		SecretKey string `json:"secretKey"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	s := defaultLocalStorage(b.Bucket)
	if b.Endpoint != "" {
		s.Endpoint = b.Endpoint
	}
	if b.AccessKey != "" {
		s.AccessKey = b.AccessKey
	}
	if b.SecretKey != "" {
		s.SecretKey = b.SecretKey
	}
	if err := DeleteObject(s, b.Key); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}
