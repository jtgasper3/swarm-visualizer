import Details from './details.js';
import { now } from './state.js';
import { accentStyle, formatBytes, formatDuration } from './utils.js';

export default {
  name: 'Task',
  template: `
    <v-card
      :style="accent"
      flat
      rounded="lg"
      class="d-flex flex-column mb-2"
    >
      <v-card-item density="compact" class="pa-1">
        <v-card-title v-if="task.service" class="text-subtitle-1 font-weight-bold">
          <v-badge :color="taskStatus(task.Status.State)" dot inline floating :title="task.Status.State" :aria-label="'Task status: ' + task.Status.State"></v-badge>
          <span :title="task.service.Spec.Name">{{ task.service.Spec.Name }}</span>
        </v-card-title>
        <v-card-subtitle class="pa-0">
          <v-chip color="primary" class="ml-1 pa-1" label size="x-medium" density="compact" slim>{{ mode }}</v-chip>
          <span v-if="since" class="ml-1" :title="'Since ' + sinceExact">{{ task.Status.State }} for {{ since }}</span>
        </v-card-subtitle>
      </v-card-item>

      <v-card-text v-if="task.service" class="mt-n2 pa-1 pb-0">
        <v-list density="compact" lines="false" class="pt-0" v-model:opened="opened">
          <v-list-item class="pa-0" min-height="12">
            <template #title><span class="text-body-2">Image: <span :title="image">{{ image.split('/').pop() }}</span></span></template>
          </v-list-item>
          <v-list-item class="pa-0" min-height="12">
            <template #title><span class="text-body-2">Container id: <span :title="task.Status.ContainerStatus?.ContainerID?.substring(0, 12)">{{ task.Status.ContainerStatus?.ContainerID?.substring(0, 12) }}</span></span></template>
          </v-list-item>
          <v-list-item class="pa-0" min-height="12" v-if="!task.NodeID && (task.Status.Err || task.Status.Message)">
            <template #title><span class="text-body-2" style="white-space: normal; word-break: break-word;">{{ task.Status.Err || task.Status.Message }}</span></template>
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
                      <v-list-item fluid class="pa-0" min-height="12" v-bind="props">
                        <template #title><span class="text-body-2">Networks</span></template>
                      </v-list-item>
                    </template>              
                  
                    <v-list-item v-for="network in service.networks" :key="network.Id" min-height="12" fluid class="pa-0">
                      <template #title><i class="mdi mdi-circle-small"></i><span class="text-body-2">{{ network.Name }}</span></template>
                    </v-list-item>
                  </v-list-group>
                </v-list-item>
              </div>

              <div v-if="task.Status.Err || task.Status.ContainerStatus?.ExitCode">
                <v-list-subheader class="pa-0" style="min-height: 12px">Exit Info</v-list-subheader>
                <v-list-item class="pt-0 pb-0" min-height="12" v-if="task.Status.ContainerStatus?.ExitCode != null">
                  <template #title><span class="text-body-2">Exit code: {{ task.Status.ContainerStatus.ExitCode }}</span></template>
                </v-list-item>
                <v-list-item class="pt-0 pb-0" min-height="12" v-if="task.Status.Err">
                  <template #title><span class="text-body-2" style="white-space: normal; word-break: break-word;">Error: {{ task.Status.Err }}</span></template>
                </v-list-item>
              </div>

              <v-list-subheader class="pa-0" style="min-height: 12px">Reservations and Limits</v-list-subheader>
              <v-list-item class="pt-0 pb-0" min-height="12">
                <template #title><span class="text-body-2">CPU Cores: {{ (task.Spec.Resources?.Reservations?.NanoCPUs ?? 0) / 1e9 }} / {{ (task.Spec.Resources?.Limits?.NanoCPUs ?? 0) / 1e9 }}</span></template>
              </v-list-item>
              <v-list-item class="pt-0 pb-0" min-height="12">
                <template #title><span class="text-body-2">Memory: {{ formatBytes(task.Spec.Resources?.Reservations?.MemoryBytes) }} / {{ formatBytes(task.Spec.Resources?.Limits?.MemoryBytes) }}</span></template>
              </v-list-item>
            </div>
          </v-expand-transition>
          <v-list-item class="pa-0" min-height="12">
            <template #title><span class="text-body-2">Created: {{ this.$vuetify.date.format(task.CreatedAt, 'keyboardDateTime12h') }}</span></template>
          </v-list-item>
        </v-list>
      </v-card-text>

      <v-card-actions class="pa-0 mt-n2" style="min-height: 12px;">
        <v-btn density="comfortable" :icon="open ? 'mdi-chevron-up' : 'mdi-chevron-down'" @click="open = !open" :aria-label="open ? 'Collapse task details' : 'Expand task details'" :aria-expanded="open.toString()" />
        <v-spacer />
        <Details :task="task" v-slot="props">
            <v-btn append-icon="mdi-chevron-right" class="text-none" slim text="Full Details"
              v-bind="props"></v-btn> 
        </Details>
      </v-card-actions>
    </v-card>
  `,
  components: {
    Details
  },
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
    // The image this task runs, without its digest, and so not necessarily the
    // one its service names now: while an update rolls out, the tasks not yet
    // replaced are still on the old one. A task that does not run a container
    // (the plugin runtime) has no ContainerSpec and so no image.
    image() {
      return this.task.Spec?.ContainerSpec?.Image?.split('@')[0] ?? '';
    },
    // The border that marks this task as its service's.
    accent() {
      return this.task.service?.accent === undefined ? {} : accentStyle(this.task.service.accent);
    },
    // How long the task has been in its present state.
    since() {
      // A redacted timestamp arrives as the year 1, not as nothing.
      const entered = Date.parse(this.task.Status?.Timestamp);
      return entered > 0 ? formatDuration(now.value - entered) : '';
    },
    sinceExact() {
      return new Date(this.task.Status.Timestamp).toLocaleString();
    },
    mode() {
      if (!this.service) return 'unknown';
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
        case 'running': return 'success';
        case 'starting':
        case 'preparing':
        case 'ready':
        case 'assigned':
        case 'accepted':
        case 'pending':
        case 'new': return 'info';
        case 'complete': return 'warning';
        case 'shutdown': return 'secondary';
        default: return 'error';
      }
    },
  }
}
