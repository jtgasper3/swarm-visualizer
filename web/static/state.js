// State that several components share and that is not part of a snapshot: the
// time, whose details are open, and where the browser keeps this dashboard's
// settings.
import { shallowRef } from 'vue';

// now moves on by itself, so that the durations shown on the page keep up
// between snapshots. A snapshot only arrives when something changes.
export const now = shallowRef(Date.now());
setInterval(() => { now.value = Date.now(); }, 30000);

// target is the node, service or task whose details are open, as { kind, id },
// or null. It lives in the address (#/service/<id>) so that a view can be
// bookmarked or sent to someone, and so that Back closes it.
function parseTarget(hash) {
  const match = /^#\/(node|service|task)\/(.+)$/.exec(hash);
  if (!match) return null;
  try {
    return { kind: match[1], id: decodeURIComponent(match[2]) };
  } catch {
    // Escapes that do not decode: not a link this page wrote. Without this the
    // error would stop the page loading at all.
    return null;
  }
}

export const target = shallowRef(parseTarget(window.location.hash));
window.addEventListener('hashchange', () => { target.value = parseTarget(window.location.hash); });

// The element the details were opened from, which gets the keyboard focus back
// when they close. A link inside the dialog only changes what it shows, so it
// does not count: closing still returns to where the dialog was opened.
let opener = null;

export function showDetails(kind, id, from) {
  if (!from?.closest('[role="dialog"]')) opener = from ?? null;
  window.location.hash = `#/${kind}/${encodeURIComponent(id)}`;
}

export function closeDetails() {
  // Assigning an empty hash would leave a bare "#" in the address.
  history.pushState(null, '', window.location.pathname + window.location.search);
  target.value = null;
}

export function returnFocus() {
  const element = opener;
  opener = null;
  if (element?.isConnected) element.focus({ preventScroll: true });
}

// Signing in goes by way of the identity provider and comes back to the bare
// address. Keep the target across that trip, or a link to a service would open
// the dashboard and nothing else for anyone not already signed in. The name
// carries this dashboard's path: another dashboard on the same host shares the
// storage, and must not open a link saved by a sign-in abandoned on this one.
const RETURN_KEY = 'swarm-visualizer.return-to:' + window.location.pathname;

export function rememberTarget() {
  try {
    if (window.location.hash) sessionStorage.setItem(RETURN_KEY, window.location.hash);
  } catch {
    // Storage can be unavailable; the link is then simply not restored.
  }
}

try {
  const remembered = sessionStorage.getItem(RETURN_KEY);
  if (remembered) {
    sessionStorage.removeItem(RETURN_KEY);
    if (!window.location.hash && parseTarget(remembered)) {
      history.replaceState(null, '', remembered);
      target.value = parseTarget(remembered);
    }
  }
} catch {
  // As above.
}

// storageKey names a setting in the browser's storage. Two dashboards on one
// host, under different context roots, share that storage, and used to share
// the names too, so each kept overwriting the other's filters. The root is now
// part of the name. A dashboard at "/" keeps the plain names it has always
// had, and any other root starts from what was saved under them.
export function storageKey(name) {
  const root = window.location.pathname;
  if (root === '/') return name;

  const scoped = root + name;
  try {
    const shared = localStorage.getItem(name);
    if (shared !== null && localStorage.getItem(scoped) === null) {
      localStorage.setItem(scoped, shared);
    }
  } catch {
    // As above.
  }
  return scoped;
}
