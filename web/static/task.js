// https://htmlcolorcodes.com/color-chart/
const taskColors = [ '#039be5', '#3949ab', '#8e24aa', '#e53935', '#fb8c00', '#fdd835', '#7cb342', '#00897b', '#00acc1', '#6d4c41', ];

export default {
  template: `
    <v-card
      :style="'border-color: ' + calcColor(task.service.index) + '; border-width: 4px'"
      flat
      rounded="lg"
      class="d-flex flex-column mb-2"
    >
      <v-card-item>
        <v-card-title v-if="task.service">
          <v-badge :color="taskStatus(task.state)" dot inline floating :title="task.state"></v-badge>
          {{ task.service.name }}
        </v-card-title>
        <v-card-subtitle>
          Task id: {{ task.id }} <br />
          Container id: {{ task.containerId.substring(0, 12) }} <br />
        </v-card-subtitle>
      </v-card-item>

      <v-card-text v-if="task.service" class="mt-n2">
        <v-chip color="primary" class="ma-1 pa-1" label size="x-medium">{{ task.service.mode }}</v-chip>
        <v-spacer />
        <span class="text-medium flex-1-1-100">
          Image: {{ task.service.image.split('@')[0] }}
        </span>
        <v-spacer />
        <span class="text-small-emphasis">
          Created: {{ this.$vuetify.date.format(task.createdAt, 'keyboardDateTime12h') }}
        </span>
      </v-card-text>
      <!-- <v-card-actions>
        <v-spacer />

        <v-btn class="text-none" color="primary" text="Get Started" />
      </v-card-actions> -->
    </v-card>
    `,
  props: {
    task: Object,
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
