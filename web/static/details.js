import { formatBytes } from './utils.js';

export default {
  name: 'Details',
  template: `
    <v-dialog
      v-model="dialog"
      fullscreen
      scrollable
    >
      <template v-slot:activator="{ props: activatorProps }">
        <slot v-bind="activatorProps" />
      </template>
    
      <v-card
        flat
        rounded="lg"
        class="d-flex flex-column mb-2"
      >
        <v-toolbar>
            <v-btn
              icon="mdi-close"
              @click="dialog = false"
            ></v-btn>
            <v-toolbar-title>{{ title }} Details</v-toolbar-title>
        </v-toolbar>
      
        <v-card
          v-if="node"
          class="mx-auto"
          style="overflow: initial; z-index: initial"
          max-width="700"
          prepend-icon="mdi-earth"
          rounded="lg"
          :subtitle="'ID: ' + node.ID"
          :title="node.Description.Hostname"
        >
          <template #text>
            <v-container fluid>
              <v-row>
                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Hostname</strong>
                    </v-col>

                    <v-col cols="8">{{ node.Description.Hostname }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Role</strong>
                    </v-col>

                    <v-col cols="8">
                      {{ node.Spec.Role }} 
                       <span v-if="node.ManagerStatus.Leader">(leader)</span>
                    </v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Availability</strong>
                    </v-col>

                    <v-col cols="8">{{ node.Spec.Availability }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12" v-if="node.Description.Platform">
                  <v-row>
                    <v-col cols="3">
                      <strong>Platform</strong>
                    </v-col>

                    <v-col cols="8">{{ node.Description.Platform.OS }} on {{ node.Description.Platform.Architecture }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Resources</strong>
                    </v-col>

                    <v-col cols="8">{{ node.Description.Resources.NanoCPUs / 1e9 }} vCPUs / {{ formatBytes(node.Description.Resources.MemoryBytes) }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Labels</strong>
                    </v-col>

                    <v-col cols="8">
                      <ul v-for="label in Object.entries(node.Spec.Labels).map(([key, value]) => key + '=' + value)" :key="label">
                        <li>{{ label }}</li>
                      </ul>
                    </v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Engine Version</strong>
                    </v-col>

                    <v-col cols="8">{{ node.Description.Engine.EngineVersion }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Created</strong>
                    </v-col>

                    <v-col cols="8">{{ new Date(node.CreatedAt).toLocaleString() }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Updated</strong>
                    </v-col>

                    <v-col cols="8">{{ new Date(node.UpdatedAt).toLocaleString() }}</v-col>
                  </v-row>
                </v-col>
              </v-row>
            </v-container>
          </template>
        </v-card>
        <v-card
          v-if="task"
          class="mx-auto"
          style="overflow: initial; z-index: initial"
          max-width="700"
          prepend-icon="mdi-earth"
          rounded="lg"
          :subtitle="'Task: ' + task.ID"
          :title="task.service.Spec.Name"
        >
          <template #text>
            <v-container fluid>
            <v-row>
             <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="3">
                    <strong>Service ID</strong>
                  </v-col>
                  <v-col cols="8">
                    <Details :service="task.service" v-slot="props">
                      <a class="text-primary" href="javascript:void(0)" v-bind="props" aria-label="Service Details">{{ task.ServiceID }}</a>
                    </Details>
                   </v-col>
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="3">
                    <strong>Node ID</strong>
                  </v-col>
                  <v-col cols="8">{{ task.NodeID }}</v-col>
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="3">
                    <strong>Status</strong>
                  </v-col>
                  <v-col cols="8">{{ task.Status.State }}</v-col>
                </v-row>
              </v-col>

              <v-divider />

             <v-col cols="12">
                <v-row>
                  <v-col cols="3">
                    <strong>Slot</strong>
                  </v-col>
                  <v-col cols="8">{{ task.Slot }}</v-col>
                </v-row>
              </v-col>


              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="12"><strong>Container Spec</strong></v-col>
                </v-row>
                
                <v-row>
                  <v-col cols="1"></v-col>
                  <v-col cols="2">
                    <strong>Image</strong>
                  </v-col>
                  <v-col cols="8">{{ task.Spec.ContainerSpec.Image }}</v-col>
                  
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="1"></v-col>
                  <v-col cols="2">
                    <strong>Environment Variables</strong>
                  </v-col>
                  <v-col cols="8">
                    <ul v-for="env in task.Spec.ContainerSpec.Env" :key="env">
                      <li>{{ env }}</li>
                    </ul>
                  </v-col>
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="1"></v-col>
                  <v-col cols="2">
                    <strong>Mounts</strong>
                  </v-col>
                  <v-col cols="8">
                    <ul v-for="mount in task.Spec.ContainerSpec.Mounts" :key="mount.Target">
                      <li>{{ mount.Type }}: {{ mount.Target }} (ReadOnly: {{ mount.ReadOnly }})</li>
                    </ul>
                  </v-col>
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="1"></v-col>
                  <v-col cols="2">
                    <strong>Configs</strong>
                  </v-col>
                  <v-col cols="8">
                    <ul v-for="config in task.Spec.ContainerSpec.Configs" :key="config.ConfigID">
                      <li>{{ config.ConfigName }}</li>
                    </ul>
                  </v-col>
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="1"></v-col>
                  <v-col cols="2">
                    <strong>Secrets</strong>
                  </v-col>
                  <v-col cols="8">
                    <ul v-for="secret in task.Spec.ContainerSpec.Secrets" :key="secret.SecretID">
                      <li>{{ secret.SecretName }}</li>
                    </ul>
                  </v-col>
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="1"></v-col>
                  <v-col cols="2">
                    <strong>Labels</strong>
                  </v-col>
                  <v-col cols="8">
                    <ul v-for="(value, key) in task.Spec.ContainerSpec.Labels" :key="key">
                      <li>{{ key }}: {{ value }}</li>
                    </ul>
                  </v-col>
                </v-row>
              </v-col>
              
              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="3">
                    <strong>Created</strong>
                  </v-col>
                  <v-col cols="8">{{ new Date(task.CreatedAt).toLocaleString() }}</v-col>
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="3">
                    <strong>Updated</strong>
                  </v-col>
                  <v-col cols="8">{{ new Date(task.UpdatedAt).toLocaleString() }}</v-col>
                </v-row>
              </v-col>

              </v-row>
            </v-container>
          </template>
        </v-card>
        <v-card
          v-if="service"
          class="mx-auto"
          max-width="700"
          style="overflow: initial; z-index: initial"
          prepend-icon="mdi-earth"
          rounded="lg"
          :subtitle="'Service: ' + service.ID"
          :title="service?.Spec?.Name ? service.Spec.Name : service.Name"
        >
          <template #text>
            <v-container fluid>
              <v-row>
                <v-divider />
                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>ID</strong>
                    </v-col>
                    <v-col cols="8">
                      {{ service.ID }}
                     </v-col>
                  </v-row>
                </v-col>
              
                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="12"><strong>Container Spec</strong></v-col>
                  </v-row>
                  <v-row>
                    <v-col cols="1"></v-col>
                    <v-col cols="2">
                      <strong>Image</strong>
                    </v-col>

                    <v-col cols="8">{{ service.Spec.TaskTemplate.ContainerSpec.Image }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="1"></v-col>
                    <v-col cols="2">
                      <strong>Environment</strong>
                    </v-col>

                    <v-col cols="8">
                      <ul v-for="env in service.Spec.TaskTemplate.ContainerSpec.Env" :key="env">
                        <li>{{ env }}</li>
                      </ul>
                    </v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="1"></v-col>
                    <v-col cols="2">
                      <strong>Mounts</strong>
                    </v-col>
                    <v-col cols="8">
                      <ul v-for="mount in service.Spec.TaskTemplate.ContainerSpec.Mounts" :key="mount.Target">
                        <li>{{ mount.Type }}: {{ mount.Target }} (ReadOnly: {{ mount.ReadOnly }})</li>
                      </ul>
                    </v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="1"></v-col>
                    <v-col cols="2">
                      <strong>Configs</strong>
                    </v-col>
                    <v-col cols="8">
                      <ul v-for="config in service.Spec.TaskTemplate.ContainerSpec.Configs" :key="config.ConfigID">
                        <li>{{ config.ConfigName }}</li>
                      </ul>
                    </v-col>
                  </v-row>
                </v-col>
              
                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="1"></v-col>
                    <v-col cols="2">
                      <strong>Secrets</strong>
                    </v-col>
                    <v-col cols="8">
                      <ul v-for="secret in service.Spec.TaskTemplate.ContainerSpec.Secrets" :key="secret.SecretID">
                        <li>{{ secret.SecretName }}</li>
                      </ul>
                    </v-col>
                  </v-row>
                </v-col>
              
                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="1"></v-col>
                    <v-col cols="2">
                      <strong>Labels</strong>
                    </v-col>
                    <v-col cols="8">
                      <ul v-for="(value, key) in service.Spec.Labels" :key="key">
                        <li>{{ key }}: {{ value }}</li>
                      </ul>
                    </v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Mode</strong>
                    </v-col>

                    <v-col cols="8">{{ service.Spec.Mode.Replicated ? 'Replicated' : 'Global' }} {{ service.Spec.Mode.Replicated ? '(' + service.Spec.Mode.Replicated.Replicas + ')' : '' }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Resources</strong>
                    </v-col>

                    <v-col cols="8">{{ service.Spec.TaskTemplate.Resources.Reservations.NanoCPUs / 1e9 }} vCPUs / {{ formatBytes(service.Spec.TaskTemplate.Resources.Reservations.MemoryBytes) }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Created</strong>
                    </v-col>

                    <v-col cols="8">{{ new Date(service.CreatedAt).toLocaleString() }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Updated</strong>
                    </v-col>

                    <v-col cols="8">{{ new Date(service.UpdatedAt).toLocaleString() }}</v-col>
                  </v-row>
                </v-col>
              </v-row>
            </v-container>
          </template>
        </v-card>
      </v-card>
    </v-dialog>
  `,
  data() {
    return {
      dialog: false,
      formatBytes: formatBytes, // make the utility function available in the template
    }
  },
  props: {
    node: Object,
    service: Object,
    task: Object,
  },
  computed: {
    title: function() {
      if (this.node) {
        return `${this.node.Description.Hostname} Node`;
      } else if (this.service) {
        return `${this.service.Spec.Name} Service`;
      } else if (this.task) {
        return `${this.task.service.Spec.Name}.${this.task.Slot ?? this.task.ID.substring(0, 6)} Task`;
      } else {
        return '';
      }
    }
  },
  methods: {
  }
}
