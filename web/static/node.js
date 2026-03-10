import Details from './details.js';
import Task from './task.js';
import { formatBytes } from './utils.js';

export default {
  name: 'Node',
  template: `
    <v-card  color="primary-lighten-4" rounded="lg" variant="tonal">
      <v-card-item>
        <template v-slot:append>
          <Details :node="node" v-slot="props">
            <v-btn icon="mdi-chevron-right" density="compact" v-bind="props" :aria-label="'Details for node ' + node.Description.Hostname"></v-btn>
          </Details>
        </template>
        <v-card-title>
          <v-badge :color="nodeStatus(node.Status.State)" dot inline floating :title="node.Status.State" :aria-label="'Node status: ' + node.Status.State"></v-badge>
          <span :title="node.Description.Hostname">{{ node.Description.Hostname }}</span>
          <span v-if="node.Spec.Availability !== 'active'">({{ node.Spec.Availability }})</span>
        </v-card-title>
        <v-card-subtitle>Node id: {{ node.ID.substring(0, 12) }}</v-card-subtitle>
      </v-card-item>
      
      <v-card-text class="mt-n2">
        <v-chip color="primary" class="ma-1 pa-1" label size="x-medium" :aria-label="'Role: ' + node.Spec.Role">{{ node.Spec.Role }}</v-chip>
        <v-chip color="primary" class="ma-1 pa-1" label size="x-medium" :aria-label="'Architecture: ' + node.Description.Platform?.Architecture">{{ node.Description.Platform?.Architecture }}</v-chip>
        <v-spacer />

        <v-table id="server" density="compact" title="Node Resources" aria-label="Node Resources">
          <thead>
            <tr>
              <th class="text-left">
              </th>
              <th class="text-left">Phys.</th>
              <th class="text-left">Res.</th>
              <th class="text-left">Limit</th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <th>vCPUs</th>
              <td>{{ node.Description.Resources?.NanoCPUs / 1e9 }}</td>
              <td>{{ combinedServiceStats.reservedCpu / 1e9 }}</td>
              <td>{{ combinedServiceStats.limitedCpu / 1e9 }}</td>
            </tr>
            <tr>
              <th>Memory</th>
              <td>{{ formatBytes(node.Description.Resources?.MemoryBytes) }}</td>
              <td>{{ formatBytes(combinedServiceStats.reservedMemory) }}</td>
              <td>{{ formatBytes(combinedServiceStats.limitedMemory) }}</td>
            </tr>
          </tbody>
        </v-table>
      </v-card-text>

      <v-card-text class="mt-n4">
        <v-list :aria-label="'Services on ' + node.Description.Hostname" class="pa-0">
          <v-list-item v-for="task in sortedAndFilteredServices(node.tasks)" :key="task.id" :aria-label="task.service?.Spec.Name" class="pa-0">
            <Task :task="task" :service="task.service" />
          </v-list-item>
        </v-list>
      </v-card-text>
    </v-card>
  `,
  components: {
    Details,
    Task
  },
  data() {
    return {
      formatBytes: formatBytes, // make the utility function available in the template
    }
  },
  props: {
    node: Object,
    filters: Object,
    sort: String,
  },
  computed: {
    combinedServiceStats() {
      return (this.node.tasks || []).reduce((accumulator, task) => {
        const service = task.service;
        if (!service) return accumulator;
        if (service.Spec.TaskTemplate.Resources.Reservations?.NanoCPUs) {
          accumulator.reservedCpu += (service.Spec.TaskTemplate.Resources.Reservations?.NanoCPUs ?? 0);
        }
        if (service.Spec.TaskTemplate.Resources.Reservations?.MemoryBytes) {
          accumulator.reservedMemory += (service.Spec.TaskTemplate.Resources.Reservations?.MemoryBytes ?? 0);
        }
        if (service.Spec.TaskTemplate.Resources.Limits?.NanoCPUs) {
          accumulator.limitedCpu += (service.Spec.TaskTemplate.Resources.Limits?.NanoCPUs ?? 0);
        }
        if (service.Spec.TaskTemplate.Resources.Limits?.MemoryBytes) {
          accumulator.limitedMemory += (service.Spec.TaskTemplate.Resources.Limits?.MemoryBytes ?? 0);
        }
        return accumulator;
      }, { reservedCpu: 0, reservedMemory: 0, limitedCpu: 0, limitedMemory: 0 });
    }
  },
  methods: {
    sortedAndFilteredServices(tasks) {
      return tasks
        .filter(task => task.service != null)
        .filter(task => {
          const filterText = this.filters.filterText ? this.filters.filterText.trim() : '';
          if (filterText.length === 0 || task.service.Spec.Name.toLowerCase().includes(filterText.toLowerCase())) {
            return true;
          }
          return false;
        })
        .filter(task => {
          if (!this.filters.serviceMode) {
            return true;
          }
          return "global" === this.filters.serviceMode && task.service.Spec.Mode.Global !== undefined ||
                 "replicated" === this.filters.serviceMode && task.service.Spec.Mode.Replicated !== undefined;
        })
        .filter(task => this.filters.servicesSelection.includes(task.service.ID))
        .filter(task => {
          if (!this.filters.taskStateFilter) return true;
          const isStopped = task.Status.State === 'failed' || task.Status.State === 'complete';
          if (this.filters.taskStateFilter === 'healthy') return !isStopped;
          if (this.filters.taskStateFilter === 'failed') return isStopped;
          return true;
        })
        .filter(task => {
          const networkIds = task.service.networks ? task.service.networks.map(n => n.Id) : [];
          if (networkIds.length === 0 && this.filters.networksSelection.includes('(none)')) {
            return true;
          }
          return this.filters.networksSelection.some(networkId => networkIds.includes(networkId));
        })
        .sort((a, b) => {
          const aMode = a.service.Spec.Mode.Replicated ? 'replicated' : 'global';
          const bMode = b.service.Spec.Mode.Replicated ? 'replicated' : 'global';

          if (aMode < bMode) return -1;
          if (aMode > bMode) return 1;
          // return 0; // If both properties are equal
          
          if (this.sort === 'Created') {
            return a.CreatedAt.localeCompare(b.CreatedAt);
          }
          return a.service.Spec.Name.localeCompare(b.service.Spec.Name);
        });
    },
    nodeStatus(status) {
      switch (status) {
        case 'ready': return 'success';
        default: return 'error';
      }
    },
  }
}
