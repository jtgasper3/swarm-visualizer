// Tests for the frontend's pure helpers. They need nothing but Node:
//
//   node --test web/test/
//
// What the page renders from them is covered by the browser test in
// web/browser_test.go.
import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  accentStyle, assignAccents, formatBytes, formatDuration, reservedBy, serviceMatchesQuery,
  serviceTallies, stackNameOf, tallyAlert, visibleTasks,
} from '../static/utils.js';

test('formatBytes picks the largest unit that fits', () => {
  assert.deepEqual(
    [undefined, 0, 1023, 1024, 1536, 1024 ** 2, 1024 ** 3 * 1.5, 1024 ** 5].map(bytes => formatBytes(bytes)),
    ['0.00 B', '0.00 B', '1023.00 B', '1.00 KB', '1.50 KB', '1.00 MB', '1.50 GB', '1024.00 TB']);
});

test('formatDuration shows the two largest units', () => {
  const m = 60000, h = 60 * m, d = 24 * h;
  assert.deepEqual(
    [0, 59999, m, 59 * m, h, h + 20 * m, 23 * h + 59 * m, d, d + 2 * h, 400 * d + 5 * h, -5000].map(formatDuration),
    ['<1m', '<1m', '1m', '59m', '1h', '1h 20m', '23h 59m', '1d', '1d 2h', '400d 5h', '<1m']);
});

test('a service outside any stack belongs to "default"', () => {
  assert.equal(stackNameOf({ Spec: { Labels: { 'com.docker.stack.namespace': 'shop' } } }), 'shop');
  assert.equal(stackNameOf({ Spec: { Labels: {} } }), 'default');
  assert.equal(stackNameOf({ Spec: {} }), 'default');
  assert.equal(stackNameOf(undefined), 'default');
});

test('the search box matches a service by name, stack and network', () => {
  const service = {
    Spec: { Name: 'shop_web', Labels: { 'com.docker.stack.namespace': 'shop' } },
    // A network that is not in the published list leaves a gap.
    networks: [{ Name: 'frontend' }, undefined],
  };
  assert.equal(serviceMatchesQuery(service, ''), true);
  assert.equal(serviceMatchesQuery(service, 'web'), true);
  assert.equal(serviceMatchesQuery(service, 'shop'), true);
  assert.equal(serviceMatchesQuery(service, 'front'), true);
  assert.equal(serviceMatchesQuery(service, 'backend'), false);
  assert.equal(serviceMatchesQuery(undefined, 'web'), false);
  assert.equal(serviceMatchesQuery(undefined, ''), true);
  assert.equal(serviceMatchesQuery({ Spec: { Name: 'solo' } }, 'default'), true);
});

// --- running against desired

const service = (ID, Mode, extra = {}) => ({ ID, Spec: { Name: ID, Mode, TaskTemplate: {} }, ...extra });
const task = (ID, ServiceID, State, DesiredState = 'running', NodeID = 'n1') =>
  ({ ID, ServiceID, NodeID, DesiredState, Status: { State }, CreatedAt: ID });

const services = [
  service('full', { Replicated: { Replicas: 2 } }),
  service('partial', { Replicated: { Replicas: 3 } }),
  service('down', { Replicated: { Replicas: 1 } }),
  service('zero', { Replicated: { Replicas: 0 } }),
  service('glob', { Global: {} }),
  service('globdown', { Global: {} }),
  service('nowhere', { Global: {} }),
  service('job', { ReplicatedJob: { MaxConcurrent: 1 } }),
];
const tasks = [
  task('a', 'full', 'running'), task('b', 'full', 'running'),
  task('c', 'partial', 'running'), task('d', 'partial', 'pending', 'running', ''), task('e', 'partial', 'pending', 'running', ''),
  task('f', 'down', 'pending', 'running', ''),
  task('g', 'glob', 'running'), task('h', 'glob', 'running', 'running', 'n2'),
  task('i', 'globdown', 'starting'),
  // Stopped tasks kept on screen for the grace period count as neither wanted
  // nor running.
  task('j', 'full', 'failed', 'shutdown'),
  task('k', 'full', 'running', 'shutdown'),
];

test('serviceTallies counts running tasks against what each service wants', () => {
  const tallies = serviceTallies(services, tasks);
  assert.deepEqual(tallies.get('full'), { running: 2, desired: 2 });
  assert.deepEqual(tallies.get('partial'), { running: 1, desired: 3 });
  assert.deepEqual(tallies.get('down'), { running: 0, desired: 1 });
  assert.deepEqual(tallies.get('zero'), { running: 0, desired: 0 });
  assert.deepEqual(tallies.get('glob'), { running: 2, desired: 2 });
  assert.deepEqual(tallies.get('globdown'), { running: 0, desired: 1 });
  assert.deepEqual(tallies.get('nowhere'), { running: 0, desired: 0 });
  assert.equal(tallies.has('job'), false);

  assert.deepEqual(
    ['full', 'partial', 'down', 'zero', 'glob', 'globdown', 'nowhere', 'job'].map(id => tallyAlert(tallies.get(id))),
    ['', 'warning', 'error', '', '', 'error', '', '']);
});

test('a service running more than it wants is not in trouble', () => {
  // A start-first rolling update does this for a moment.
  assert.equal(tallyAlert({ running: 3, desired: 2 }), '');
});

// --- which tasks a card lists

const filters = {
  filterText: '', serviceMode: false, taskStateFilter: false,
  servicesSelection: ['full', 'partial'], networksSelection: ['(none)'],
};
const joined = [
  ...tasks.map(each => ({ ...each, service: services.find(s => s.ID === each.ServiceID) })),
  // A task whose service has been removed.
  { ...task('orphan', 'gone', 'running'), service: undefined },
];
const listed = (overrides, hostname) =>
  visibleTasks(joined, { ...filters, ...overrides }, 'Service Name', hostname).map(each => each.ID);

test('visibleTasks copes with a swarm that has no tasks', () => {
  assert.deepEqual(visibleTasks(null, filters, 'Service Name'), []);
  assert.deepEqual(visibleTasks(undefined, filters, 'Service Name', 'host'), []);
});

test('visibleTasks applies the service selection and drops tasks with no service', () => {
  assert.deepEqual(listed({}), ['a', 'b', 'j', 'k', 'c', 'd', 'e']);
});

test('visibleTasks applies the search text', () => {
  assert.deepEqual(listed({ filterText: ' PART ' }), ['c', 'd', 'e']);
  // A match on the card's own hostname keeps everything on it. A card with no
  // hostname, the unassigned one, can only match by service.
  assert.equal(listed({ filterText: 'mgr' }, 'mgr-01').length, 7);
  assert.equal(listed({ filterText: 'mgr' }).length, 0);
});

test('visibleTasks applies the task state filter', () => {
  assert.deepEqual(listed({ taskStateFilter: 'failed' }), ['j']);
  assert.deepEqual(listed({ taskStateFilter: 'healthy' }), ['a', 'b', 'k', 'c', 'd', 'e']);
});

test('visibleTasks applies the network selection', () => {
  const on = (ID, networkIds) => ({
    ID, Spec: { Name: ID, Mode: { Replicated: { Replicas: 1 } } }, networks: networkIds.map(Id => ({ Id, Name: Id })),
  });
  const attached = { a: on('a', ['n1']), b: on('b', ['n2']), c: on('c', []), d: on('d', ['n1', 'n2']) };
  const all = Object.values(attached).map((owner, i) => ({ ID: String(i + 1), service: owner, CreatedAt: String(i), Status: { State: 'running' } }));
  const shown = (servicesSelection, networksSelection) =>
    visibleTasks(all, { ...filters, servicesSelection, networksSelection }, 'Service Name').map(each => each.ID).join('');

  assert.equal(shown(['a', 'b', 'c', 'd'], ['n1', 'n2', '(none)']), '1234');
  assert.equal(shown(['a', 'b', 'c', 'd'], ['n1']), '14');
  assert.equal(shown(['a', 'b', 'c', 'd'], ['(none)']), '3');
  assert.equal(shown(['a', 'b', 'c', 'd'], []), '');
  assert.equal(shown(['b', 'c'], ['n1', 'n2', '(none)']), '23');
});

// --- what a node's tasks hold

test('reservedBy sums each task\'s own spec and skips tasks that have stopped', () => {
  const resources = (cpu, memory) => ({
    Reservations: { NanoCPUs: cpu * 1e9, MemoryBytes: memory },
    Limits: { NanoCPUs: cpu * 2e9, MemoryBytes: memory * 2 },
  });
  const state = State => ({ State });
  const onNode = [
    { DesiredState: 'running', Status: state('running'), Spec: { Resources: resources(1, 512) } },    // already on the new spec
    { DesiredState: 'running', Status: state('starting'), Spec: { Resources: resources(0.5, 256) } }, // not yet replaced
    { DesiredState: 'shutdown', Status: state('failed'), Spec: { Resources: resources(4, 4096) } },   // kept for the grace period
    { DesiredState: 'running', Status: state('failed'), Spec: { Resources: resources(8, 8192) } },    // just failed, not yet replaced
    { DesiredState: 'running', Status: state('complete'), Spec: { Resources: resources(8, 8192) } },
    { DesiredState: 'running', Status: state('rejected'), Spec: { Resources: resources(8, 8192) } },
    { DesiredState: 'running', Status: state('running'), Spec: {} },                                  // created with no resources block
    { DesiredState: 'running', Status: state('running') },
    { DesiredState: 'running' },                                                                      // status redacted
  ];
  assert.deepEqual(reservedBy(onNode), { reservedCpu: 1.5e9, reservedMemory: 768, limitedCpu: 3e9, limitedMemory: 1536 });
  assert.deepEqual(reservedBy(undefined), { reservedCpu: 0, reservedMemory: 0, limitedCpu: 0, limitedMemory: 0 });
});

// --- service colours

const ids = n => Array.from({ length: n }, (_, i) => 'svc' + String(i * 7919).padStart(6, '0'));
const border = accent => { const style = accentStyle(accent); return style['border-color'] + '/' + style['border-style']; };
const sorted = accents => [...accents].sort();

test('every service gets a border of its own while there are combinations left', () => {
  // 14 colours in 6 styles.
  for (const n of [1, 5, 14, 30, 84]) {
    const accents = assignAccents(ids(n));
    assert.equal(new Set([...accents.values()].map(border)).size, n, `${n} services should have ${n} distinct borders`);
  }
  assert.equal(assignAccents(ids(200)).size, 200);
});

test('plain borders are handed out before patterned ones', () => {
  const accents = [...assignAccents(ids(14)).values()];
  assert.ok(accents.every(accent => accentStyle(accent)['border-style'] === 'solid'));
  assert.equal(new Set(accents.map(accent => accentStyle(accent)['border-color'])).size, 14);
});

test('the same services get the same borders in whatever order they are listed', () => {
  assert.deepEqual(sorted(assignAccents(ids(30))), sorted(assignAccents(ids(30).reverse())));

  const given = ['b', 'a', 'c'];
  assignAccents(given);
  assert.deepEqual(given, ['b', 'a', 'c'], 'the list passed in was reordered');
});

test('a service keeps its border when others arrive or leave', () => {
  const before = assignAccents(ids(20));
  const grown = assignAccents([...ids(20), 'aaa-new-1', 'zzz-new-2', 'mmm-new-3'], before);
  for (const [id, accent] of before) assert.equal(grown.get(id), accent, `${id} changed when services were added`);
  assert.equal(new Set(grown.values()).size, 23);

  const shrunk = assignAccents(ids(20).filter((_, i) => i % 3 !== 0), grown);
  for (const [id, accent] of shrunk) assert.equal(accent, before.get(id), `${id} changed when services were removed`);
});

test('past the last combination services share, and still nothing moves by itself', () => {
  const many = Array.from({ length: 85 }, (_, i) => 'service-' + String(i).padStart(2, '0'));
  const crowded = assignAccents(many);
  assert.equal(new Set(crowded.values()).size, 84);
  assert.deepEqual(sorted(assignAccents([...many].reverse(), crowded)), sorted(crowded), 'the listing order changed a border');
  assert.deepEqual(sorted(assignAccents(many, crowded)), sorted(crowded), 'a repeated snapshot changed a border');

  // When one service leaves, the only change is that a sharing service takes
  // the border it freed.
  const sharing = many.filter(id => [...crowded.values()].filter(accent => accent === crowded.get(id)).length > 1).sort();
  assert.equal(sharing.length, 2);
  assert.ok(!sharing.includes('service-00'), 'this test needs service-00 not to be one of the two that share');
  const afterLeaving = assignAccents(many.slice(1), crowded);
  const changed = many.slice(1).filter(id => afterLeaving.get(id) !== crowded.get(id));
  assert.deepEqual(changed, [sharing[1]]);
  assert.equal(afterLeaving.get(sharing[1]), crowded.get('service-00'));
  assert.equal(new Set(afterLeaving.values()).size, 84);
});

test('a newcomer that has to share does not push the other service off its border', () => {
  const rest = Array.from({ length: 84 }, (_, i) => 'service-' + String(i + 1).padStart(2, '0'));
  const full = assignAccents(rest);
  assert.equal(new Set(full.values()).size, 84);

  // service-00 sorts ahead of the service whose border it ends up sharing.
  const joined = assignAccents(['service-00', ...rest], full);
  for (const [id, accent] of full) assert.equal(joined.get(id), accent, `${id} moved when a service that has to share arrived`);
  assert.deepEqual(sorted(assignAccents(['service-00', ...rest], joined)), sorted(joined), 'a sharing service moved on the next snapshot');
});
