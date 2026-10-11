package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
	"testing"
)

// TestVendoredFilesMatchChecksums verifies that the third-party files built
// into the server are the ones vendor.sh downloaded: nothing edited by hand,
// nothing added, nothing missing.
func TestVendoredFilesMatchChecksums(t *testing.T) {
	recorded, err := os.ReadFile("vendor.sha256")
	if err != nil {
		t.Fatalf("read checksums: %v", err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(recorded)), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("malformed checksum line %q", line)
		}
		want[name] = sum
	}
	if len(want) == 0 {
		t.Fatal("vendor.sha256 lists no files")
	}

	vendor, err := fs.Sub(Static(), "vendor")
	if err != nil {
		t.Fatalf("open vendor: %v", err)
	}
	seen := map[string]bool{}
	err = fs.WalkDir(vendor, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := fs.ReadFile(vendor, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		switch recorded, listed := want[name]; {
		case !listed:
			t.Errorf("%s is in static/vendor but not in vendor.sha256", name)
		case recorded != hex.EncodeToString(sum[:]):
			t.Errorf("%s does not match its recorded checksum", name)
		}
		seen[name] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk vendor: %v", err)
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s is in vendor.sha256 but not in static/vendor", name)
		}
	}
}

var (
	htmlComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	externalURL  = regexp.MustCompile(`(?i)(https?:)?//[a-z0-9.-]+\.[a-z]{2,}[^\s"'<>)]*`)
	stylesheet   = regexp.MustCompile(`<link href="([^"]+)" rel="stylesheet"`)
	importMap    = regexp.MustCompile(`(?s)<script type="importmap">(.*?)</script>`)
	cssReference = regexp.MustCompile(`url\(\s*["']?([^"')]+)["']?\s*\)`)
)

// TestPageLoadsNothingFromOtherSites verifies that the dashboard is complete in
// itself: every stylesheet and module the page names is one of the files built
// into the server, and none of them, or the stylesheets' own references, point
// anywhere else. A dashboard for a swarm with no route to the internet would
// otherwise come up blank.
func TestPageLoadsNothingFromOtherSites(t *testing.T) {
	static := Static()
	raw, err := fs.ReadFile(static, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	page := htmlComment.ReplaceAllString(string(raw), "")

	if found := externalURL.FindAllString(page, -1); len(found) > 0 {
		t.Errorf("index.html refers to other sites: %q", found)
	}

	exists := func(name string) bool {
		name, _, _ = strings.Cut(name, "?")
		_, err := fs.Stat(static, path.Clean(name))
		return err == nil
	}

	sheets := stylesheet.FindAllStringSubmatch(page, -1)
	if len(sheets) == 0 {
		t.Fatal("found no stylesheets in index.html")
	}
	for _, sheet := range sheets {
		href := sheet[1]
		if !exists(href) {
			t.Errorf("stylesheet %s is not among the built-in files", href)
			continue
		}
		css, err := fs.ReadFile(static, path.Clean(href))
		if err != nil {
			t.Fatalf("read %s: %v", href, err)
		}
		for _, ref := range cssReference.FindAllStringSubmatch(string(css), -1) {
			target := ref[1]
			switch {
			case strings.HasPrefix(target, "data:"):
			case externalURL.MatchString(target):
				t.Errorf("%s refers to another site: %s", href, target)
			case strings.HasSuffix(strings.SplitN(target, "?", 2)[0], ".woff2"):
				// Each font is listed in several formats; woff2 is the one
				// kept, and the one browsers that can run the page ask for.
				if !exists(path.Join(path.Dir(href), target)) {
					t.Errorf("%s refers to %s, which is not among the built-in files", href, target)
				}
			}
		}
	}

	found := importMap.FindStringSubmatch(page)
	if found == nil {
		t.Fatal("found no import map in index.html")
	}
	var parsed struct {
		Imports map[string]string `json:"imports"`
	}
	if err := json.Unmarshal([]byte(found[1]), &parsed); err != nil {
		t.Fatalf("parse import map: %v", err)
	}
	for specifier, address := range parsed.Imports {
		if !strings.HasPrefix(address, "./") || !exists(address) {
			t.Errorf("import %q maps to %s, which is not among the built-in files", specifier, address)
		}
	}
}
