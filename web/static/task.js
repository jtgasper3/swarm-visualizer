import { formatBytes } from './utils.js';

export default {
  template: `
    <v-card
      :style="calcStyle(task.service.index)"
      flat
      rounded="lg"
      class="d-flex flex-column mb-2"
    >
      <v-card-item density="compact" class="pa-1">
        <v-card-title v-if="task.service" class="text-subtitle-1 font-weight-bold">
          <v-badge :color="taskStatus(task.Status.State)" dot inline floating :title="task.Status.State"></v-badge>
          <span :title="task.service.Spec.Name">{{ task.service.Spec.Name }}</span>
        </v-card-title>
        <v-card-subtitle class="pa-0"><v-chip color="primary" class="ml-1 pa-1" label size="x-medium" density="compact" slim>{{ mode }}</v-chip></v-card-subtitle>
      </v-card-item>

      <v-card-text v-if="task.service" class="mt-n2 pa-1 pb-0">
        <v-list density="compact" lines="false" class="pt-0" v-model:opened="opened">
          <v-list-item class="pa-0" min-height="12">
            <template #title><span class="text-body-2">Image: <span :title="task.service.Spec.TaskTemplate.ContainerSpec.Image.split('@')[0]">{{ task.service.Spec.TaskTemplate.ContainerSpec.Image.split('@')[0] }}</span></span></template>
          </v-list-item>
          <v-list-item class="pa-0" min-height="12">
            <template #title><span class="text-body-2">Container id: <span :title="task.Status.ContainerStatus.ContainerID.substring(0, 12)">{{ task.Status.ContainerStatus.ContainerID.substring(0, 12) }}</span></span></template>
          </v-list-item>

          <v-expand-transition>
            <div v-if="open">
              <v-list-item class="pa-0" min-height="12">
                <template #title><span class="text-body-2">Task id: <span :title="task.ID">{{ task.ID }}</span></span></template>
              </v-list-item>
              <v-list-item class="pa-0" min-height="12" v-if="mode === 'replicated'">
                <template #title><span class="text-body-2">Slot: {{ task.Slot }} of {{ service.Spec.Mode.Replicated.Replicas }}</span></template>
              </v-list-item>

              <div v-if="service.networks && service.networks.length > 0">
                <v-list-item class="pa-0" min-height="12">
                  <v-list-group value="networks">
                    <template v-slot:activator="{ props }">
                      <v-list-item class="pa-0" min-height="12" v-bind="props">
                        <template #title><span class="text-body-2">Networks</span></template>
                      </v-list-item>
                    </template>              
                  
                    <v-list-item v-for="network in service.networks" :key="network.Id" min-height="12" class="pa-0">
                      <template #title><i class="mdi mdi-circle-small"></i><span class="text-body-2">{{ network.Name }}</span></template>
                    </v-list-item>
                  </v-list-group>
                </v-list-item>
              </div>

              <v-list-item class="pa-0" min-height="12">
                <template #title><span class="text-body-2">Reservations and Limits</span></template>
              </v-list-item>
              <v-list-item class="pt-0 pb-0" min-height="12">
                <template #title><span class="text-body-2">CPU Cores: {{ (task.service.Spec.TaskTemplate.Resources.Reservations?.NanoCPUs ?? 0) / 1e9 }} / {{ (task.service.Spec.TaskTemplate.Resources.Limits?.NanoCPUs ?? 0) / 1e9 }}</span></template>
              </v-list-item>
              <v-list-item class="pt-0 pb-0" min-height="12">
                <template #title><span class="text-body-2">Memory: {{ formatBytes(task.service.Spec.TaskTemplate.Resources.Reservations?.MemoryBytes) }} / {{ formatBytes(task.service.Spec.TaskTemplate.Resources.Limits?.MemoryBytes) }}</span></template>
              </v-list-item>
            </div>
          </v-expand-transition>
          <v-list-item class="pa-0" min-height="12">
            <template #title><span class="text-body-2">Created: {{ this.$vuetify.date.format(task.CreatedAt, 'keyboardDateTime12h') }}</span></template>
          </v-list-item>
        </v-list>
      </v-card-text>

      <v-card-actions class="pa-0 mt-n2" style="min-height: 12px;">
        <!-- <v-btn append-icon="mdi-chevron-right" class="text-none" slim text="Download receipt" /> -->
        <v-spacer />

        <v-btn density="comfortable" :icon="open ? 'mdi-chevron-up' : 'mdi-chevron-down'" @click="open = !open" />
      </v-card-actions>
    </v-card>
    `,
  data() {
    return {
      open: false,
      opened: ['networks'],
      formatBytes: formatBytes, // make the utility function available in the template
    }
  },
  props: {
    task: Object,
    service: Object,
  },
  computed: {
    mode() {
      if (this.service.Spec.Mode.Replicated) {
        return 'replicated';
      } else if (this.service.Spec.Mode.Global) {
        return 'global';
      } else {
        return 'unknown';
      }
    }
  },
  methods: {
    taskStatus(status) {
      switch (status) {
        case 'running': return 'success'; break
        default: return 'error'; break
      }
    },
    calcStyle(index) {
      // https://htmlcolorcodes.com/color-chart/
      const taskColors = [ '#039be5', '#3949ab', '#FF80F2', '#e53935', '#fb8c00', '#8e24aa', '#fdd835', '#80FF8D', '#00897b', '#00acc1', '#6d4c41', '#80B3FF', '#CC80FF', '#7cb342',];
      const borderStyles = ['solid', 'dashed', 'dotted', 'groove', 'inset', 'double'];
      
      const style = borderStyles[Math.trunc(index / taskColors.length) % borderStyles.length];

      return {
        'border-color': taskColors[index % taskColors.length],
        'border-style': style,
        'border-width': '3px'
      }
    }
  }
}
