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
