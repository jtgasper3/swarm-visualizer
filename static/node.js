import Task from './task.js';

const { useDate } = Vuetify;

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
        <Task v-for="task in node.tasks" :key="task.id" :task="task" />
      </v-card-text>
    </v-card>
    `,
    components: {
      Task
    },
    data() {
      return {
        date: useDate(),
      }
    },
    props: {
      node: Object
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
      nodeStatus(status) {
        switch (status) {
          case 'ready': return 'success'; break
          default: return 'error'; break
        }
      },
    }
}
