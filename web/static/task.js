// https://htmlcolorcodes.com/color-chart/
const taskColors = [ '#039be5', '#3949ab', '#8e24aa', '#e53935', '#fb8c00', '#fdd835', '#7cb342', '#00897b', '#00acc1', '#6d4c41', ];

import { formatBytes } from './utils.js';

export default {
  template: `
    <v-card
      :style="'border-color: ' + calcColor(task.service.index) + '; border-width: 4px'"
      flat
      rounded="lg"
      class="d-flex flex-column mb-2"
    >
      <v-card-item density="compact" class="pa-1">
        <v-card-title v-if="task.service" class="text-subtitle-1 font-weight-bold">
          <v-badge :color="taskStatus(task.state)" dot inline floating :title="task.state"></v-badge>
          <span :title="task.service.name">{{ task.service.name }}</span>
        </v-card-title>
        <v-card-subtitle class="pa-0"><v-chip color="primary" class="ml-1 pa-1" label size="x-medium" density="compact" slim>{{ task.service.mode }}</v-chip></v-card-subtitle>
      </v-card-item>

      <v-card-text v-if="task.service" class="mt-n2 pa-1 pb-0">
        <v-list density="compact" lines="false" class="pt-0" v-model:opened="opened">
          <v-list-item class="pa-0" min-height="12">
            <template #title><span class="text-body-2">Image: <span :title="task.service.image.split('@')[0]">{{ task.service.image.split('@')[0] }}</span></span></template>
          </v-list-item>
          <v-list-item class="pa-0" min-height="12">
            <template #title><span class="text-body-2">Container id: <span :title="task.containerId.substring(0, 12)">{{ task.containerId.substring(0, 12) }}</span></span></template>
          </v-list-item>

          <v-expand-transition>
            <div v-if="open">
              <v-list-item class="pa-0" min-height="12">
                <template #title><span class="text-body-2">Task id: <span :title="task.id">{{ task.id }}</span></span></template>
              </v-list-item>
              <v-list-item class="pa-0" min-height="12" v-if="task.service.mode === 'replicated'">
                <template #title><span class="text-body-2">Slot: {{ task.slot }} of {{ service.replicas }}</span></template>
              </v-list-item>

              <div v-if="service.networks && service.networks.length > 0">
                <v-list-item class="pa-0" min-height="12">
                  <v-list-group value="networks">
                    <template v-slot:activator="{ props }">
                      <v-list-item class="pa-0" min-height="12" v-bind="props">
                        <template #title><span class="text-body-2">Networks</span></template>
                      </v-list-item>
                    </template>              
                  
                    <v-list-item v-for="network in service.networks" :key="network.id" min-height="12" class="pa-0">
                      <template #title><i class="mdi mdi-circle-small"></i><span class="text-body-2">{{ network.name }}</span></template>
                    </v-list-item>
                  </v-list-group>
                </v-list-item>
              </div>

              <v-list-item class="pa-0" min-height="12">
                <template #title><span class="text-body-2">Reservations and Limits</span></template>
              </v-list-item>
              <v-list-item class="pt-0 pb-0" min-height="12">
                <template #title><span class="text-body-2">CPU: {{ task.service.reservationsCpu }} / {{task.service.limitsCpu }}</span></template>
              </v-list-item>
              <v-list-item class="pt-0 pb-0" min-height="12">
                <template #title><span class="text-body-2">Memory: {{ formatBytes(task.service.reservationsMemory) }} / {{ formatBytes(task.service.limitsMemory) }}</span></template>
              </v-list-item>
            </div>
          </v-expand-transition>
          <v-list-item class="pa-0" min-height="12">
            <template #title><span class="text-body-2">Created: {{ this.$vuetify.date.format(task.createdAt, 'keyboardDateTime12h') }}</span></template>
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
  methods: {
    taskStatus(status) {
      switch (status) {
        case 'running': return 'success'; break
        default: return 'error'; break
      }
    },
    calcColor(index) {
      return taskColors[index % taskColors.length]
    }
  }
}
