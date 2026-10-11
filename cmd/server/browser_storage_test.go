package main

import (
	"strings"
	"testing"
)

// underContextRoot serves the dashboard under a path, as behind a reverse
// proxy that shares its host name with other things.
func underContextRoot(root string) settings {
	return func(string) map[string]string { return map[string]string{"CONTEXT_ROOT": root} }
}

// Two dashboards on one host share the browser's storage. Each keeps its
// settings under its own context root, so that neither overwrites the other's
// filters, and one that is not at the root starts from whatever was saved
// under the plain names every dashboard used to share.
func TestDashboardInABrowser_ContextRoot(t *testing.T) {
	p := startBrowser(t)
	server := startDashboard(t, "swarm", underContextRoot("/viz/"))

	// A setting saved by an earlier version, or by a dashboard at the root:
	// it is there before the page starts.
	p.beforeLoad(t, `if (localStorage.getItem('sort') === null) localStorage.setItem('sort', 'Created')`)

	p.open(t, server.URL+"/viz/")
	p.waitForCards(t, 4)

	stored := func() map[string]string {
		var all map[string]string
		p.eval(t, `Object.fromEntries(Object.keys(localStorage).map(key => [key, localStorage.getItem(key)]))`, &all)
		return all
	}

	t.Run("settings are kept under the context root", func(t *testing.T) {
		all := stored()
		var names []string
		for name := range all {
			names = append(names, name)
		}
		// The plain "sort" is the one put there above, left as it was.
		want := map[string]bool{"sort": true, "/viz/sort": true, "/viz/drawer": true, "/viz/filters.5": true}
		for _, name := range names {
			if !want[name] {
				t.Errorf("unexpected setting %q among %q", name, names)
			}
			delete(want, name)
		}
		for name := range want {
			t.Errorf("no setting %q among %q", name, names)
		}
	})

	t.Run("and start from what was saved before", func(t *testing.T) {
		if got := stored()["/viz/sort"]; got != "Created" {
			t.Errorf("/viz/sort = %q, want the value saved under the plain name", got)
		}
		if drawer := p.text(t, ".v-navigation-drawer"); !strings.Contains(drawer, "SORT Created") {
			t.Errorf("the page does not use it; the drawer reads: %s", drawer)
		}
	})

	t.Run("a change is saved there and nowhere else", func(t *testing.T) {
		p.eval(t, `[...document.querySelectorAll('.v-navigation-drawer button')].find(button => button.innerText.trim() === 'STOPPED').click()`, nil)
		p.waitFor(t, `(localStorage.getItem('/viz/filters.5') ?? '').includes('"taskStateFilter":"failed"')`)

		all := stored()
		if _, shared := all["filters.5"]; shared {
			t.Errorf("the filters were also saved under the plain name: %q", all["filters.5"])
		}
		if all["sort"] != "Created" {
			t.Errorf("the plain sort setting changed to %q", all["sort"])
		}
	})

	t.Run("links work under the context root", func(t *testing.T) {
		p.eval(t, `location.hash = '#/service/svc-web'`, nil)
		p.waitFor(t, `(document.querySelector('.v-overlay--active .v-card')?.innerText ?? '').replace(/\s+/g, ' ').includes('Service: svc-web ')`)

		var path string
		p.eval(t, `location.pathname`, &path)
		if path != "/viz/" {
			t.Errorf("the page is at %q", path)
		}
	})

	assertNothingWentWrong(t, p)
}
