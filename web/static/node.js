import Task from './task.js';
import { formatBytes } from './utils.js';

export default {
  template: `
    <v-card  color="primary-lighten-4" rounded="lg" variant="tonal">
      <v-card-item>
        <v-card-title>
          <v-badge :color="nodeStatus(node.status)" dot inline floating :title="node.status"></v-badge>
          <span :title="node.hostname">{{ node.hostname }}</span>
          <span v-if="node.availability !== 'active'">({{ node.availability }})</span>
        </v-card-title>
        <v-card-subtitle>Node id: {{ node.id }}</v-card-subtitle>
      </v-card-item>
      
      <v-card-text class="mt-n2">
        <v-chip color="primary" class="ma-1 pa-1" label size="x-medium">{{ node.role }}</v-chip>
        <v-chip color="primary" class="ma-1 pa-1" label size="x-medium">{{ node.platformArchitecture
          }}</v-chip>
        <v-spacer />

        <v-table id="server" density="compact" title="Node Resources" aria-label="Node Resources">
          <thead>
            <tr>
              <th class="text-left">
              </th>
              <th class="text-left">
                Physical
              </th>
              <th class="text-left">
                Reserved
              </th>
              <th class="text-left">
                Limited
              </th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <th>Cores</th>
              <td>{{ node.cpuCores }}</td>
              <td>{{ combinedServiceStats.reservedCpu }}</td>
              <td>{{ combinedServiceStats.limitedCpu }}</td>
            </tr>
            <tr>
              <th>Memory</th>
              <td>{{ formatBytes(node.memoryBytes) }}</td>
              <td>{{ formatBytes(combinedServiceStats.reservedMemory) }}</td>
              <td>{{ formatBytes(combinedServiceStats.limitedMemory) }}</td>
            </tr>
          </tbody>
        </v-table>
      </v-card-text>

      <v-card-text class="mt-n4">
        <v-list :aria-label="'Services on ' + node.hostname" class="pa-0">
          <v-list-item v-for="task in sortedAndFilteredServices(node.tasks)" :key="task.id" :aria-label="task.service.name" class="pa-0">
            <Task :task="task" :service="task.service" />
          </v-list-item>
        </v-list>
      </v-card-text>
    </v-card>
    `,
  components: {
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
      return this.node.tasks.reduce((accumulator, task) => {
        const service = task.service;
        if (service.reservationsCpu) {
          accumulator.reservedCpu += service.reservationsCpu;
        }
        if (service.reservationsMemory) {
          accumulator.reservedMemory += service.reservationsMemory;
        }
        if (service.limitsCpu) {
          accumulator.limitedCpu += service.limitsCpu;
        }
        if (service.limitsMemory) {
          accumulator.limitedMemory += service.limitsMemory;
        }
        return accumulator;
      }, { reservedCpu: 0, reservedMemory: 0, limitedCpu: 0, limitedMemory: 0 });
    }
  },
  methods: {
    sortedAndFilteredServices(tasks) {
      return tasks
        .filter(task => {
          const filterText = this.filters.filterText ? this.filters.filterText.trim() : '';
          if (filterText.length >= 0 && task.service.name.toLowerCase().includes(filterText.toLowerCase())) {
            return true;
          }
          return false;
        })
        .filter(task => {
          if (!this.filters.serviceMode) {
            return true;
          }
          return task.service.mode === this.filters.serviceMode
        })
        .filter(task => {
          if (this.filters.service === 'all') {
            return true;
          }
          return this.filters.servicesSelection.includes(task.service.id);
        })
        .filter(task => {
          if (this.filters.networks === 'all') {
            return true;
          }
          const networkIds = task.service.networks ? task.service.networks.map(n => n.id) : [];
          if (networkIds.length === 0 && this.filters.networksSelection.includes('(none)')) {
            return true;
          }
          return this.filters.networksSelection.some(networkId => networkIds.includes(networkId));
        })
        .sort((a, b) => {
          if (a.service.mode < b.service.mode) return -1;
          if (a.service.mode > b.service.mode) return 1;
          // return 0; // If both properties are equal
          
          if (this.sort === 'Created') {
            return a.createdAt.localeCompare(b.createdAt);
          }
          return a.service.name.localeCompare(b.service.name);
        });
    },
    nodeStatus(status) {
      switch (status) {
        case 'ready': return 'success'; break
        default: return 'error'; break
      }
    },
  }
}
