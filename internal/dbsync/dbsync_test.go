package dbsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const validIRPayload = "Filetype: IR signals file\nVersion: 1\n#\nname: Power\ntype: parsed\nprotocol: NEC\naddress: 04 00 00 00\ncommand: 08 00 00 00\n"
const validIRPayloadV2 = "Filetype: IR signals file\nVersion: 1\n#\nname: Power\ntype: parsed\nprotocol: NEC\naddress: 04 00 00 00\ncommand: 09 00 00 00\n"

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestDefaultSourcesAndValidation(t *testing.T) {
	defaults := DefaultSources()
	if len(defaults) < 4 {
		t.Fatalf("expected >= 4 default sources, got %d", len(defaults))
	}
	for i := range defaults {
		if err := defaults[i].NormalizeAndValidate(); err != nil {
			t.Fatalf("default source %q invalid: %v", defaults[i].ID, err)
		}
		if defaults[i].Prune {
			t.Fatalf("default source %q must have prune=false", defaults[i].ID)
		}
	}

	badSources := []DatabaseSource{
		{ID: "", Kind: SourceGitHubTree, Repo: "a/b"},
		{ID: "user", Kind: SourceGitHubTree, Repo: "a/b"},
		{ID: "root", Kind: SourceGitHubTree, Repo: "a/b"},
		{ID: "-bad", Kind: SourceGitHubTree, Repo: "a/b"},
		{ID: "bad.id", Kind: SourceGitHubTree, Repo: "a/b"},
		{ID: strings.Repeat("a", 33), Kind: SourceGitHubTree, Repo: "a/b"},
		{ID: "ok", Kind: SourceGitHubTree, Repo: "onlyowner"},
		{ID: "ok", Kind: SourceGitHubTree, Repo: "a/b", PathPrefix: "../etc"},
		{ID: "ok", Kind: SourceJSONManifest, URL: "http://example.com/m.json"},
		{ID: "ok", Kind: SourceJSONManifest, URL: "file:///etc/passwd"},
		{ID: "ok", Kind: SourceKind("ftp"), URL: "https://example.com/m.json"},
	}
	for i, src := range badSources {
		if err := src.NormalizeAndValidate(); !errors.Is(err, ErrInvalidSource) {
			t.Fatalf("case %d (%+v): got err=%v, want ErrInvalidSource", i, src, err)
		}
	}

	// Loopback http:// is allowed for local testing; uppercase ID normalizes to lowercase.
	loopback := DatabaseSource{ID: "Local_Test", Kind: SourceJSONManifest, URL: "http://127.0.0.1:8080/m.json"}
	if err := loopback.NormalizeAndValidate(); err != nil || loopback.ID != "local_test" {
		t.Fatalf("loopback validation failed: %+v, %v", loopback, err)
	}
}

func TestParseGitHubURLAndSourcesPersistence(t *testing.T) {
	repo, branch, prefix, err := ParseGitHubURL("https://github.com/Lucaslhm/Flipper-IRDB/tree/main/ACs/Daikin")
	if err != nil || repo != "Lucaslhm/Flipper-IRDB" || branch != "main" || prefix != "ACs/Daikin" {
		t.Fatalf("ParseGitHubURL = (%q, %q, %q, %v)", repo, branch, prefix, err)
	}
	if _, _, _, err := ParseGitHubURL("http://github.com/a/b"); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource for http github url, got %v", err)
	}

	path := filepath.Join(t.TempDir(), "cfg", "sources.json")
	if got, err := LoadSources(path); err != nil || got != nil {
		t.Fatalf("missing sources.json = (%v, %v); want (nil, nil)", got, err)
	}

	src := DefaultSources()[0]
	list, err := UpsertSource(path, src)
	if err != nil || len(list) != 1 {
		t.Fatalf("UpsertSource: %v, %v", list, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("sources.json perm = %o, want 0600", info.Mode().Perm())
	}

	// Upsert same ID replaces in place.
	src.Enabled = false
	list, err = UpsertSource(path, src)
	if err != nil || len(list) != 1 || list[0].Enabled {
		t.Fatalf("UpsertSource replace failed: %+v, %v", list, err)
	}

	// Remove missing ID returns ErrSourceNotFound.
	if _, err := RemoveSource(path, "nonexistent"); !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("expected ErrSourceNotFound, got %v", err)
	}
	list, err = RemoveSource(path, src.ID)
	if err != nil || len(list) != 0 {
		t.Fatalf("RemoveSource failed: %+v, %v", list, err)
	}
}

func FuzzParseGitHubURL(f *testing.F) {
	f.Add("https://github.com/Lucaslhm/Flipper-IRDB/tree/main/TVs")
	f.Add("https://github.com/owner/repo.git")
	f.Add("not-a-url")
	f.Add("https://github.com/a/b/tree/main/../../etc")
	f.Fuzz(func(t *testing.T, raw string) {
		_, _, _, _ = ParseGitHubURL(raw)
	})
}

func TestSyncerGitHubTreeETagDeltaAndPrune(t *testing.T) {
	var blobGets atomic.Int32
	etag := `"etag-v1"`
	treeBody := map[string]any{
		"tree": []map[string]any{
			{"path": "TVs/Sony/Bravia.ir", "type": "blob", "sha": "sha1"},
			{"path": "TVs/LG.ir", "type": "blob", "sha": "sha2"},
			{"path": "TVs/README.md", "type": "blob", "sha": "sha-md"},
			{"path": "ACs/Other.ir", "type": "blob", "sha": "sha-ac"},
		},
	}
	blobs := map[string]string{
		"/Lucaslhm/Flipper-IRDB/main/TVs/Sony/Bravia.ir": validIRPayload,
		"/Lucaslhm/Flipper-IRDB/main/TVs/LG.ir":          validIRPayload,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/repos/") {
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
			_ = json.NewEncoder(w).Encode(treeBody)
			return
		}
		blobGets.Add(1)
		if body, ok := blobs[r.URL.Path]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dbDir := filepath.Join(t.TempDir(), "databases")
	syncer := NewSyncer(dbDir, srv.Client())
	syncer.GitHubAPIURL = srv.URL
	syncer.GitHubRawURL = srv.URL

	src := DatabaseSource{
		ID: "flipper-tvs", Kind: SourceGitHubTree,
		Repo: "Lucaslhm/Flipper-IRDB", Branch: "main", PathPrefix: "TVs",
		Enabled: true, Prune: false,
	}

	// 1. Initial sync downloads 2 valid .ir files (flattening Sony/Bravia.ir -> Sony__Bravia.ir).
	res, err := syncer.SyncSource(context.Background(), src)
	if err != nil {
		t.Fatalf("initial SyncSource: %v", err)
	}
	if res.Downloaded != 2 || res.Pruned != 0 || res.Status != "updated" {
		t.Fatalf("unexpected initial result: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dbDir, "flipper-tvs", "Sony__Bravia.ir")); err != nil {
		t.Fatalf("flattened file missing: %v", err)
	}

	// 2. Second sync gets 304 Not Modified -> 0 additional blob GETs.
	getsBefore := blobGets.Load()
	res, err = syncer.SyncSource(context.Background(), src)
	if err != nil || res.Status != "up-to-date" || blobGets.Load() != getsBefore {
		t.Fatalf("304 sync failed: res=%+v err=%v gets=%d->%d", res, err, getsBefore, blobGets.Load())
	}

	// 3. Mark LG.ir ignored, remove it locally, bump ETag & change Sony SHA + remove LG from tree.
	if err := syncer.MarkIgnored("flipper-tvs", "LG.ir"); err != nil {
		t.Fatalf("MarkIgnored: %v", err)
	}
	etag = `"etag-v2"`
	blobs["/Lucaslhm/Flipper-IRDB/main/TVs/Sony/Bravia.ir"] = validIRPayloadV2
	treeBody = map[string]any{
		"tree": []map[string]any{
			{"path": "TVs/Sony/Bravia.ir", "type": "blob", "sha": "sha1-v2"},
			{"path": "TVs/LG.ir", "type": "blob", "sha": "sha2-v2"},
		},
	}
	res, err = syncer.SyncSource(context.Background(), src)
	if err != nil || res.Downloaded != 1 || res.Skipped != 1 {
		t.Fatalf("delta+ignored sync: res=%+v err=%v", res, err)
	}

	// 4. Remove Sony/Bravia.ir from upstream; with Prune=true it is removed locally.
	etag = `"etag-v3"`
	treeBody = map[string]any{"tree": []map[string]any{}}
	src.Prune = true
	res, err = syncer.SyncSource(context.Background(), src)
	if err != nil || res.Pruned != 1 {
		t.Fatalf("prune sync: res=%+v err=%v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dbDir, "flipper-tvs", "Sony__Bravia.ir")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected pruned file to be deleted")
	}
}

func TestSyncerManifestAdversarialPayloadsAndRateLimit(t *testing.T) {
	var rateLimit atomic.Bool
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimit.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		switch r.URL.Path {
		case "/manifest.json":
			doc := map[string]any{
				"version": 1,
				"files": []map[string]any{
					{"name": "Good.ir", "url": srvURL + "/good.ir", "sha256": sha256Hex(validIRPayload)},
					{"name": "BadHash.ir", "url": srvURL + "/good.ir", "sha256": strings.Repeat("0", 64)},
					{"name": "HtmlErr.ir", "url": srvURL + "/html.ir", "sha256": sha256Hex("<html>404</html>")},
					{"name": "Huge.ir", "url": srvURL + "/huge.ir"},
					{"name": "../../evil.ir", "url": srvURL + "/good.ir"},
				},
			}
			_ = json.NewEncoder(w).Encode(doc)
		case "/good.ir":
			_, _ = w.Write([]byte(validIRPayload))
		case "/html.ir":
			_, _ = w.Write([]byte("<html>404</html>"))
		case "/huge.ir":
			_, _ = w.Write(bytes.Repeat([]byte("X"), MaxIRFileBytes+32))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	dbDir := filepath.Join(t.TempDir(), "databases")
	syncer := NewSyncer(dbDir, srv.Client())
	src := DatabaseSource{
		ID: "lab-manifest", Kind: SourceJSONManifest,
		URL: srv.URL + "/manifest.json", Enabled: true,
	}

	res, err := syncer.SyncSource(context.Background(), src)
	if err != nil {
		t.Fatalf("manifest SyncSource: %v", err)
	}
	if res.Downloaded != 1 || res.Skipped != 4 || len(res.Errors) != 4 {
		t.Fatalf("unexpected manifest result: %+v", res)
	}
	// Only Good.ir + .sync-state.json should exist in pack dir.
	entries, err := os.ReadDir(filepath.Join(dbDir, "lab-manifest"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("expected 2 entries (Good.ir + .sync-state.json), got %d (%v)", len(entries), err)
	}

	// 429 Rate Limit returns ErrRateLimited without deleting Good.ir.
	rateLimit.Store(true)
	if _, err := syncer.SyncSource(context.Background(), src); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dbDir, "lab-manifest", "Good.ir")); err != nil {
		t.Fatalf("rate limit corrupted existing Good.ir: %v", err)
	}
}

func TestDeletePackDirAdversarialAndSyncAll(t *testing.T) {
	dbDir := filepath.Join(t.TempDir(), "databases")
	syncer := NewSyncer(dbDir, nil)

	packDir := filepath.Join(dbDir, "my-pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "Test.ir"), []byte(validIRPayload), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"", ".", "..", "../remotes", "user", "root", "a/b"} {
		if err := syncer.DeletePackDir(bad); !errors.Is(err, ErrInvalidSource) {
			t.Fatalf("DeletePackDir(%q) = %v, want ErrInvalidSource", bad, err)
		}
	}
	if err := syncer.DeletePackDir("my-pack"); err != nil {
		t.Fatalf("DeletePackDir(my-pack): %v", err)
	}
	if _, err := os.Stat(packDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected my-pack dir removed")
	}

	// SyncAll skips disabled sources cleanly.
	results, err := syncer.SyncAll(context.Background(), []DatabaseSource{
		{ID: "off", Kind: SourceGitHubTree, Repo: "a/b", Enabled: false},
	})
	if err != nil || len(results) != 0 {
		t.Fatalf("SyncAll disabled = (%+v, %v)", results, err)
	}
}

func TestSyncerEdgeCasesCancelAndErrors(t *testing.T) {
	if p, err := SourcesPath(); err != nil || !strings.HasSuffix(p, "sources.json") {
		t.Fatalf("SourcesPath() = (%q, %v)", p, err)
	}

	// Malformed sources.json and duplicate IDs.
	tmp := t.TempDir()
	badJSON := filepath.Join(tmp, "bad.json")
	if err := os.WriteFile(badJSON, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSources(badJSON); err == nil {
		t.Fatalf("expected error on malformed sources.json")
	}
	dup := DefaultSources()[0]
	if err := SaveSourcesAtomic(filepath.Join(tmp, "dup.json"), []DatabaseSource{dup, dup}); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected duplicate ID ErrInvalidSource, got %v", err)
	}

	// SanitizePackFilename edge cases.
	for _, bad := range []string{"", "/abs.ir", "a\\b.ir", "../a.ir", ".hidden.ir", "readme.txt"} {
		if _, err := SanitizePackFilename(bad); !errors.Is(err, ErrPathTraversal) {
			t.Fatalf("SanitizePackFilename(%q) = %v, want ErrPathTraversal", bad, err)
		}
	}

	// Manifest 304 Not Modified, mid-sync context cancel saving partial progress, and SyncAll with error.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/m304.json":
			if r.Header.Get("If-None-Match") == `"m1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"m1"`)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"version": 1,
				"files": []map[string]any{
					{"name": "First.ir", "url": srvURL + "/first.ir", "sha256": sha256Hex(validIRPayload)},
					{"name": "Second.ir", "url": srvURL + "/second.ir", "sha256": sha256Hex(validIRPayload)},
				},
			})
		case "/first.ir":
			_, _ = w.Write([]byte(validIRPayload))
		case "/second.ir":
			cancel() // cancel mid-sync on second file
			_, _ = w.Write([]byte(validIRPayload))
		case "/bad500.json":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	dbDir := filepath.Join(tmp, "databases")
	syncer := NewSyncer(dbDir, srv.Client())
	src := DatabaseSource{ID: "cancel-pack", Kind: SourceJSONManifest, URL: srv.URL + "/m304.json", Enabled: true}

	// Mid-sync cancel saves First.ir and partial .sync-state.json.
	res, err := syncer.SyncSource(ctx, src)
	if !errors.Is(err, context.Canceled) || res.Status != "cancelled" || res.Downloaded != 1 {
		t.Fatalf("mid-sync cancel: res=%+v err=%v", res, err)
	}
	st, err := LoadSyncState(filepath.Join(dbDir, "cancel-pack"))
	if err != nil || st.Files["First.ir"] == "" {
		t.Fatalf("expected partial state saved for First.ir, got %+v, %v", st, err)
	}

	// Next sync with fresh context completes, then 304 Not Modified.
	res, err = syncer.SyncSource(context.Background(), src)
	if err != nil || res.Downloaded != 1 || res.Skipped != 1 {
		t.Fatalf("resume sync: res=%+v err=%v", res, err)
	}
	res, err = syncer.SyncSource(context.Background(), src)
	if err != nil || res.Status != "up-to-date" {
		t.Fatalf("manifest 304 sync: res=%+v err=%v", res, err)
	}

	// SyncAll reports errors cleanly for failing sources.
	badSrc := DatabaseSource{ID: "err-pack", Kind: SourceJSONManifest, URL: srv.URL + "/bad500.json", Enabled: true}
	results, err := syncer.SyncAll(context.Background(), []DatabaseSource{src, badSrc})
	if err == nil || len(results) != 2 {
		t.Fatalf("SyncAll mixed = (%+v, %v)", results, err)
	}

	// Active sync guard blocks concurrent DeletePackDir / SyncSource.
	if err := syncer.acquireSync(); err != nil {
		t.Fatal(err)
	}
	if err := syncer.DeletePackDir("cancel-pack"); !errors.Is(err, ErrSyncInProgress) {
		t.Fatalf("expected ErrSyncInProgress on DeletePackDir, got %v", err)
	}
	if _, err := syncer.SyncSource(context.Background(), src); !errors.Is(err, ErrSyncInProgress) {
		t.Fatalf("expected ErrSyncInProgress on SyncSource, got %v", err)
	}
	syncer.releaseSync()
}
