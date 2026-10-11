import Task from './task.js';

// Tasks that are on no listed node: the swarm has not found one that can run
// them, or their node is gone. They belong to no node card, so without this
// card a service stuck waiting would not appear in the main view at all.
export default {
  name: 'Unassigned',
  template: `
    <v-card color="warning" rounded="lg" variant="tonal">
      <v-card-item>
        <v-card-title>
          <v-icon icon="mdi-alert" size="small" class="me-1"></v-icon>
          <span class="text-high-emphasis">Unassigned</span>
        </v-card-title>
        <v-card-subtitle class="text-high-emphasis">Tasks not placed on a node</v-card-subtitle>
      </v-card-item>

      <v-card-text class="mt-n2">
        <v-list aria-label="Unassigned tasks" class="pa-0">
          <v-list-item v-for="task in tasks" :key="task.ID" :aria-label="task.service?.Spec.Name" class="pa-0">
            <Task :task="task" :service="task.service" />
          </v-list-item>
        </v-list>
      </v-card-text>
    </v-card>
  `,
  components: {
    Task
  },
  props: {
    tasks: Array,
  },
}
