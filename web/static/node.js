import Task from './task.js';

export default {
  template: `
    <v-card  color="primary-lighten-4" rounded="lg" variant="tonal">
      <v-card-item>
        <v-card-title>
          <v-badge :color="nodeStatus(node.status)" dot inline floating :title="node.status"></v-badge> {{
          node.hostname }}
          <span v-if="node.availability !== 'active'">({{ node.availability }})</span>
        </v-card-title>
        <v-card-subtitle>id: {{ node.id }}</v-card-subtitle>
      </v-card-item>
      
      <v-card-text class="mt-n2">
        <v-chip color="primary" class="ma-1 pa-1" label size="x-medium">{{ node.role }}</v-chip>
        <v-chip color="primary" class="ma-1 pa-1" label size="x-medium">{{ node.platformArchitecture
          }}</v-chip>
        <v-spacer />
        Memory: {{ formatBytes(node.memoryBytes) }}
      </v-card-text>

      <v-card-text class="mt-n4">
        <Task v-for="task in sortedAndFilteredServices(node.tasks)" :key="task.id" :task="task" />
      </v-card-text>
    </v-card>
    `,
  components: {
    Task
  },
  props: {
    node: Object,
    filters: Object,
    sort: String,
  },
  methods: {
    formatBytes(bytes) {
      const units = ['B', 'KB', 'MB', 'GB', 'TB'];
      let unitIndex = 0;

      while (bytes >= 1024 && unitIndex < units.length - 1) {
        bytes /= 1024;
        unitIndex++;
      }

      return `${bytes.toFixed(2)} ${units[unitIndex]}`;
    },
    sortedAndFilteredServices(tasks) {
      return tasks
        .filter(task => {
          const filterText = (this.filters.filterText ? this.filters.filterText.trim() : '').toLowerCase();
          if (filterText.length >= 0){
            if (task.service.name.toLowerCase().includes(filterText)) {
              return true;
            } else if (task.id.startsWith(filterText)) {
              return true;
            } else if (task.containerId.startsWith(filterText)) {
              return true;
            }
          }
          return false;
        })
        .filter(task => {
          if (!this.filters.globalIncluded && task.service.mode === 'global') {
            return false;
          }
          return true;
        })
        .filter(task => {
          if (!this.filters.replicasIncluded && task.service.mode === 'replicated') {
            return false;
          }
          return true;
        })
        .filter(task => {
          if (this.filters.service === 'all') {
            return true;
          }
          return this.filters.servicesSelection.includes(task.service.id);
        })
        .sort((a, b) => {
          if (a.service.mode < b.service.mode) return -1;
          if (a.service.mode > b.service.mode) return 1;

          return 0; // If both properties are equal
          if (this.sort === 'Created') {
            return a.createdAt.localeCompare(b.createdAt);
          }
          return a.service.name.localeCompare(b.service.name);
        }
        );
    },
    nodeStatus(status) {
      switch (status) {
        case 'ready': return 'success'; break
        default: return 'error'; break
      }
    },
  }
}
