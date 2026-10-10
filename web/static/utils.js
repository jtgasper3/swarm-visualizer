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
    .filter(task => filters.servicesSelection.includes(task.service.ID))
    .filter(task => {
      if (!filters.taskStateFilter) return true;
      const isStopped = task.Status.State === 'failed' || task.Status.State === 'complete';
      if (filters.taskStateFilter === 'healthy') return !isStopped;
      if (filters.taskStateFilter === 'failed') return isStopped;
      return true;
    })
    .filter(task => {
      const networkIds = task.service.networks ? task.service.networks.map(n => n.Id) : [];
      if (networkIds.length === 0 && filters.networksSelection.includes('(none)')) {
        return true;
      }
      return filters.networksSelection.some(networkId => networkIds.includes(networkId));
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
