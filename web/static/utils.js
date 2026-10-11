export function formatBytes(bytes = 0) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let unitIndex = 0;

  while (bytes >= 1024 && unitIndex < units.length - 1) {
    bytes /= 1024;
    unitIndex++;
  }

  return `${bytes.toFixed(2)} ${units[unitIndex]}`;
}
// What the search box matches a task against. Lives here because both the node
// cards (node.js) and the card-level predicate (index.html) have to agree: when
// they drifted apart, the drawer advertised matches the cards could not show.
// `query` is expected already trimmed and lower-cased; empty matches everything.
// A service's stack. Services deployed outside a stack carry no namespace
// label and are grouped under a synthetic 'default', so the fallback belongs
// with the lookup: searching "default" has to reach them too.
export function stackNameOf(service) {
  return service?.Spec?.Labels?.['com.docker.stack.namespace'] || 'default';
}

export function serviceMatchesQuery(service, query) {
  if (!query) return true;
  if (!service) return false;
  if (service.Spec.Name.toLowerCase().includes(query)) return true;
  if (stackNameOf(service).toLowerCase().includes(query)) return true;
  return (service.networks || []).some(network => network && network.Name.toLowerCase().includes(query));
}

// The tasks a card lists once the drawer's filters and sort are applied. Shared
// by the node cards and the unassigned card so the two cannot disagree.
// `hostname` is the card's node; tasks that are on no node have none.
export function visibleTasks(tasks, filters, sort, hostname = '') {
  const filterText = filters.filterText ? filters.filterText.trim().toLowerCase() : '';
  // The selections are consulted once per task; as arrays that made every
  // card's work grow with the number of services.
  const selectedServices = new Set(filters.servicesSelection);
  const selectedNetworks = new Set(filters.networksSelection);

  return (tasks || [])
    .filter(task => task.service != null)
    .filter(task => {
      if (filterText.length === 0) {
        return true;
      }
      // A hostname match selects the whole node, not a subset of its tasks.
      if (hostname.toLowerCase().includes(filterText)) {
        return true;
      }
      return serviceMatchesQuery(task.service, filterText);
    })
    .filter(task => {
      if (!filters.serviceMode) {
        return true;
      }
      return "global" === filters.serviceMode && task.service.Spec.Mode.Global !== undefined ||
             "replicated" === filters.serviceMode && task.service.Spec.Mode.Replicated !== undefined;
    })
    .filter(task => selectedServices.has(task.service.ID))
    .filter(task => {
      if (!filters.taskStateFilter) return true;
      const isStopped = task.Status.State === 'failed' || task.Status.State === 'complete';
      if (filters.taskStateFilter === 'healthy') return !isStopped;
      if (filters.taskStateFilter === 'failed') return isStopped;
      return true;
    })
    .filter(task => {
      const attached = task.service.networks || [];
      if (attached.length === 0) {
        return selectedNetworks.has('(none)');
      }
      return attached.some(network => selectedNetworks.has(network.Id));
    })
    .sort((a, b) => {
      const aMode = a.service.Spec.Mode.Replicated ? 'replicated' : 'global';
      const bMode = b.service.Spec.Mode.Replicated ? 'replicated' : 'global';

      if (aMode < bMode) return -1;
      if (aMode > bMode) return 1;

      if (sort === 'Created') {
        return a.CreatedAt.localeCompare(b.CreatedAt);
      }
      return a.service.Spec.Name.localeCompare(b.service.Spec.Name);
    });
}

// For each replicated or global service, how many tasks are running against
// how many the service wants: Map of service ID -> { running, desired }. A
// replicated service wants its replica count; a global service wants one task
// on every node the swarm scheduled it to, which is the number of tasks meant
// to be running. Job services have no steady count and get no entry.
export function serviceTallies(services, tasks) {
  const counts = new Map();
  for (const task of tasks) {
    if (task.DesiredState !== 'running') continue;
    const count = counts.get(task.ServiceID) ?? { wanted: 0, running: 0 };
    count.wanted++;
    if (task.Status.State === 'running') count.running++;
    counts.set(task.ServiceID, count);
  }

  const tallies = new Map();
  for (const service of services) {
    const count = counts.get(service.ID) ?? { wanted: 0, running: 0 };
    if (service.Spec.Mode.Replicated) {
      tallies.set(service.ID, { running: count.running, desired: service.Spec.Mode.Replicated.Replicas ?? 0 });
    } else if (service.Spec.Mode.Global) {
      tallies.set(service.ID, { running: count.running, desired: count.wanted });
    }
  }
  return tallies;
}

// The severity of a tally for display: 'error' when nothing is running,
// 'warning' when some tasks are missing, '' when the service has all it wants.
export function tallyAlert(tally) {
  if (!tally || tally.running >= tally.desired) return '';
  return tally.running === 0 ? 'error' : 'warning';
}

// The border colours and styles that tell one service's tasks from another's.
// https://htmlcolorcodes.com/color-chart/
const ACCENT_COLORS = ['#039be5', '#3949ab', '#FF80F2', '#e53935', '#fb8c00', '#8e24aa', '#fdd835', '#80FF8D', '#00897b', '#00acc1', '#6d4c41', '#80B3FF', '#CC80FF', '#7cb342'];
const ACCENT_STYLES = ['solid', 'dashed', 'dotted', 'groove', 'inset', 'double'];

// FNV-1a, to turn a service ID into a number that does not depend on anything
// but the ID.
function hashOf(text) {
  let hash = 2166136261;
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

// Gives each service an accent: a number that accentStyle turns into a border.
// Returns a Map of service ID -> accent.
//
// The accent used to be the service's position in the list, so adding or
// removing one service recoloured every service after it. Here a service's ID
// picks its colour, and the next free colour is taken when that one is in use,
// so each service keeps a border of its own for as long as there are
// combinations to go round. Plain borders are handed out before patterned
// ones. `previous` is the last result: a service that already has an accent
// keeps it, whatever arrives or leaves around it.
export function assignAccents(serviceIds, previous = new Map()) {
  const accents = new Map();
  const taken = new Set();

  // Everything goes in ID order, so that the result does not depend on the
  // order the services were listed in, and two browsers shown the same
  // services agree on the colours.
  const unplaced = [];
  for (const id of [...serviceIds].sort()) {
    const accent = previous.get(id);
    if (accent !== undefined && !taken.has(accent)) {
      accents.set(id, accent);
      taken.add(accent);
    } else {
      unplaced.push(id);
    }
  }

  for (const id of unplaced) {
    const hash = hashOf(id);
    const preferred = hash % ACCENT_COLORS.length;
    // With more services than combinations some have to share. One that is
    // sharing already stays as it is until a combination comes free.
    let accent = previous.get(id) ?? hash % (ACCENT_COLORS.length * ACCENT_STYLES.length);
    search:
    for (let style = 0; style < ACCENT_STYLES.length; style++) {
      for (let step = 0; step < ACCENT_COLORS.length; step++) {
        const candidate = style * ACCENT_COLORS.length + (preferred + step) % ACCENT_COLORS.length;
        if (!taken.has(candidate)) {
          accent = candidate;
          break search;
        }
      }
    }
    accents.set(id, accent);
    taken.add(accent);
  }
  return accents;
}

export function accentStyle(accent) {
  return {
    'border-color': ACCENT_COLORS[accent % ACCENT_COLORS.length],
    'border-style': ACCENT_STYLES[Math.trunc(accent / ACCENT_COLORS.length) % ACCENT_STYLES.length],
    'border-width': '3px',
  };
}

// A length of time in its two largest units: "<1m", "12m", "3h 20m", "5d 2h".
export function formatDuration(milliseconds) {
  const minutes = Math.floor(Math.max(0, milliseconds) / 60000);
  if (minutes < 1) return '<1m';
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return minutes % 60 ? `${hours}h ${minutes % 60}m` : `${hours}h`;
  const days = Math.floor(hours / 24);
  return hours % 24 ? `${days}d ${hours % 24}h` : `${days}d`;
}

// The states a task does not come back from. Swarm's scheduler stops counting
// a task against its node once it reaches one of them.
const FINISHED_STATES = new Set(['complete', 'shutdown', 'failed', 'rejected', 'remove', 'orphaned']);

// The resources the tasks on a node hold, from each task's own spec: during a
// rolling update the old and new tasks of a service can differ. A task that
// has stopped holds nothing, whatever state it is still meant to be in: one
// that has just failed is listed as meant to be running until it is replaced.
export function reservedBy(tasks) {
  const totals = { reservedCpu: 0, reservedMemory: 0, limitedCpu: 0, limitedMemory: 0 };
  for (const task of tasks || []) {
    if (FINISHED_STATES.has(task.Status?.State)) continue;
    const resources = task.Spec?.Resources;
    totals.reservedCpu += resources?.Reservations?.NanoCPUs ?? 0;
    totals.reservedMemory += resources?.Reservations?.MemoryBytes ?? 0;
    totals.limitedCpu += resources?.Limits?.NanoCPUs ?? 0;
    totals.limitedMemory += resources?.Limits?.MemoryBytes ?? 0;
  }
  return totals;
}
