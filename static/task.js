const { useDate } = Vuetify;

export default {
  template: `
    <v-card border class="d-flex flex-column mb-2" flat rounded="lg">
      <v-card-title v-if="task.service">
        <v-badge :color="taskStatus(task.state)" dot inline floating :title="task.state"></v-badge>
        {{ task.service.name }}
      </v-card-title>
      <v-card-subtitle>
        id: {{ task.id }}
      </v-card-subtitle>
      <v-card-text v-if="task.service" class="mt-n3">
        <v-chip color="primary" class="ma-1 pa-1" label size="x-medium">{{ task.service.mode }}</v-chip>
        <v-spacer />
        <span class="text-medium flex-1-1-100">
          Image: {{ task.service.image.split('@')[0] }}
        </span>
        <v-spacer />
        <span class="text-small-emphasis">
          Created: {{ date.format(task.createdAt, 'keyboardDateTime12h') }}
        </span>
      </v-card-text>
      <!-- <v-card-actions>
        <v-spacer />

        <v-btn class="text-none" color="primary" text="Get Started" />
      </v-card-actions> -->
    </v-card>
    `,
  data() {
    return {
      date: useDate(),
    }
  },
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
  }
}
