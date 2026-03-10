import { formatBytes } from './utils.js';

export default {
  name: 'Details',
  template: `
    <v-dialog
      v-model="dialog"
      fullscreen
      scrollable
      :aria-label="title + ' Details'"
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
              aria-label="Close details"
            ></v-btn>
            <v-toolbar-title>{{ title }} Details</v-toolbar-title>
        </v-toolbar>
      
        <v-card
          v-if="node"
          class="mx-auto"
          style="overflow: initial; z-index: initial"
          max-width="700"
          prepend-icon="mdi-server"
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
                       <span v-if="node.ManagerStatus?.Leader">(leader)</span>
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

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>State</strong>
                    </v-col>

                    <v-col cols="8">{{ node.Status.State }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Address</strong>
                    </v-col>

                    <v-col cols="8">{{ node.Status.Addr }}</v-col>
                  </v-row>
                </v-col>

                <template v-if="node.ManagerStatus">
                  <v-divider />

                  <v-col cols="12">
                    <v-row>
                      <v-col cols="3">
                        <strong>Manager</strong>
                      </v-col>

                      <v-col cols="8">{{ node.ManagerStatus.Reachability }} — {{ node.ManagerStatus.Addr }}</v-col>
                    </v-row>
                  </v-col>
                </template>

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

                    <v-col cols="8">{{ (node.Description.Resources?.NanoCPUs ?? 0) / 1e9 }} vCPUs / {{ formatBytes(node.Description.Resources?.MemoryBytes) }}</v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Labels</strong>
                    </v-col>

                    <v-col cols="8">
                      <ul>
                        <li v-for="label in Object.entries(node.Spec.Labels ?? {}).map(([key, value]) => key + '=' + value)" :key="label">{{ label }}</li>
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

                    <v-col cols="8">{{ node.Description.Engine?.EngineVersion }}</v-col>
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
          prepend-icon="mdi-application-cog"
          rounded="lg"
          :subtitle="'Task: ' + task.ID"
          :title="task.service?.Spec.Name"
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
                      <v-btn color="primary" density="compact" slim variant="text" v-bind="props" class="text-none" aria-label="Service Details">{{ task.ServiceID }}</v-btn>
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
                  <v-col cols="8">
                    {{ task.Status.State }}
                    <span v-if="task.DesiredState && task.DesiredState !== task.Status.State"> (desired: {{ task.DesiredState }})</span>
                    <div v-if="task.Status.Message" class="text-medium-emphasis text-body-2">{{ task.Status.Message }}</div>
                    <div v-if="task.Status.Err" class="text-error text-body-2">{{ task.Status.Err }}</div>
                  </v-col>
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
                  <v-col cols="3">
                    <strong>Container ID</strong>
                  </v-col>
                  <v-col cols="8">{{ task.Status.ContainerStatus?.ContainerID }}</v-col>
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
                    <strong>Command/ Args</strong>
                  </v-col>

                  <v-col cols="8">
                    {{ task.Spec.ContainerSpec.Args }}
                  </v-col>
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
                    <ul>
                      <li v-for="env in (task.Spec.ContainerSpec?.Env ?? [])" :key="env">{{ env }}</li>
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
                    <ul>
                      <li v-for="mount in (task.Spec.ContainerSpec?.Mounts ?? [])" :key="mount.Target">{{ mount.Type }}: {{ mount.Target }}<span v-if="mount.ReadOnly"> (ReadOnly)</span></li>
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
                    <ul>
                      <li v-for="config in (task.Spec.ContainerSpec?.Configs ?? [])" :key="config.ConfigID">{{ config.ConfigName }}</li>
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
                    <ul>
                      <li v-for="secret in (task.Spec.ContainerSpec?.Secrets ?? [])" :key="secret.SecretID">{{ secret.SecretName }}</li>
                    </ul>
                  </v-col>
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="1"></v-col>
                  <v-col cols="2">
                    <strong>Networks</strong>
                  </v-col>
                  <v-col cols="8">
                    <ul>
                      <li v-for="net in (task.Spec.Networks ?? [])" :key="net.Target">{{ task.service?.networks?.find(n => n.Id === net.Target)?.Name ?? net.Target }} ({{ net.Target }})<span v-if="net.Aliases?.length">: {{ net.Aliases.join(', ') }}</span></li>
                    </ul>
                  </v-col>
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="1"></v-col>
                  <v-col cols="2">
                    <strong>Healthcheck</strong>
                  </v-col>
                  <v-col cols="8">
                    <div v-if="task.Spec.ContainerSpec?.Healthcheck">
                      <strong>Healthcheck:</strong>
                      <ul>
                        <li v-for="cmd in task.Spec.ContainerSpec.Healthcheck.Test" :key="cmd">{{ cmd }}</li>
                      </ul>
                      <div>Interval: {{ task.Spec.ContainerSpec.Healthcheck.Interval ? task.Spec.ContainerSpec.Healthcheck.Interval / 1e9 + 's' : 'default' }}</div>
                      <div>Timeout: {{ task.Spec.ContainerSpec.Healthcheck.Timeout ? task.Spec.ContainerSpec.Healthcheck.Timeout / 1e9 + 's' : 'default' }}</div>
                      <div>Retries: {{ task.Spec.ContainerSpec.Healthcheck.Retries ?? 'default' }}</div>
                    </div>
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
                    <ul>
                      <li v-for="(value, key) in (task.Spec.ContainerSpec?.Labels ?? {})" :key="key">{{ key }}: {{ value }}</li>
                    </ul>
                  </v-col>
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="3">
                    <strong>Resources</strong>
                  </v-col>
                  <v-col cols="8">
                    <div>Reservations: {{ (task.Spec.Resources?.Reservations?.NanoCPUs ?? 0) / 1e9 }} vCPUs / {{ formatBytes(task.Spec.Resources?.Reservations?.MemoryBytes) }}</div>
                    <div>Limits: {{ (task.Spec.Resources?.Limits?.NanoCPUs ?? 0) / 1e9 }} vCPUs / {{ formatBytes(task.Spec.Resources?.Limits?.MemoryBytes) }}</div>
                  </v-col>
                </v-row>
              </v-col>

              <v-divider />

              <v-col cols="12">
                <v-row>
                  <v-col cols="3">
                    <strong>Restart Policy</strong>
                  </v-col>
                  <v-col cols="8" v-if="task.Spec.RestartPolicy">
                    <div>Condition: {{ task.Spec.RestartPolicy.Condition }}</div>
                    <div v-if="task.Spec.RestartPolicy.Delay">Delay: {{ task.Spec.RestartPolicy.Delay / 1e9 }}s</div>
                    <div v-if="task.Spec.RestartPolicy.MaxAttempts">Max Attempts: {{ task.Spec.RestartPolicy.MaxAttempts }}</div>
                  </v-col>
                  <v-col cols="8" v-else>—</v-col>
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
          prepend-icon="mdi-application-cog"
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
                    <v-col cols="3">
                      <strong>Published Ports</strong>
                    </v-col>
                    <v-col cols="8">
                      <ul>
                        <li v-for="port in (service.Endpoint?.Ports ?? service.Spec.EndpointSpec?.Ports ?? [])" :key="port.PublishedPort">
                          {{ port.Protocol }}: {{ port.PublishedPort }} → {{ port.TargetPort }}
                          <span v-if="port.PublishMode"> ({{ port.PublishMode }})</span>
                        </li>
                      </ul>
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

                    <v-col cols="8" style="overflow: hidden;"><span style="display: block; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; direction: rtl; text-align: left;">{{ service.Spec.TaskTemplate.ContainerSpec.Image.split('@')[0] }}</span></v-col>
                  </v-row>
                </v-col>
                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="1"></v-col>
                    <v-col cols="2">
                      <strong>Command/ Args</strong>
                    </v-col>

                    <v-col cols="8">
                      {{ service.Spec.TaskTemplate.ContainerSpec.Args }}
                    </v-col>
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
                      <ul>
                        <li v-for="env in (service.Spec.TaskTemplate.ContainerSpec?.Env ?? [])" :key="env">{{ env }}</li>
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
                      <ul>
                        <li v-for="mount in (service.Spec.TaskTemplate.ContainerSpec?.Mounts ?? [])" :key="mount.Target">{{ mount.Type }}: {{ mount.Target }}<span v-if="mount.ReadOnly"> (ReadOnly)</span></li>
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
                      <ul>
                        <li v-for="config in (service.Spec.TaskTemplate.ContainerSpec?.Configs ?? [])" :key="config.ConfigID">{{ config.ConfigName }}</li>
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
                      <ul>
                        <li v-for="secret in (service.Spec.TaskTemplate.ContainerSpec?.Secrets ?? [])" :key="secret.SecretID">{{ secret.SecretName }}</li>
                      </ul>
                    </v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="1"></v-col>
                    <v-col cols="2">
                      <strong>Networks</strong>
                    </v-col>
                    <v-col cols="8">
                      <ul>
                        <li v-for="net in (service.Spec.TaskTemplate.Networks ?? [])" :key="net.Target">{{ service.networks?.find(n => n.Id === net.Target)?.Name ?? net.Target }} ({{ net.Target }})<span v-if="net.Aliases?.length">: {{ net.Aliases.join(', ') }}</span></li>
                      </ul>
                    </v-col>
                  </v-row>
                </v-col>

                <v-divider />
                <v-col cols="12">
                  <v-row>
                    <v-col cols="1"></v-col>
                    <v-col cols="2">
                      <strong>Healthcheck</strong>
                    </v-col>
                    <v-col cols="8">
                      <div v-if="service.Spec.TaskTemplate.ContainerSpec?.Healthcheck">
                        <strong>Healthcheck:</strong>
                        <ul>
                          <li v-for="cmd in service.Spec.TaskTemplate.ContainerSpec.Healthcheck.Test" :key="cmd">{{ cmd }}</li>
                        </ul>
                        <div>Interval: {{ service.Spec.TaskTemplate.ContainerSpec.Healthcheck.Interval ? service.Spec.TaskTemplate.ContainerSpec.Healthcheck.Interval / 1e9 + 's' : 'default' }}</div>
                        <div>Timeout: {{ service.Spec.TaskTemplate.ContainerSpec.Healthcheck.Timeout ? service.Spec.TaskTemplate.ContainerSpec.Healthcheck.Timeout / 1e9 + 's' : 'default' }}</div>
                        <div>Retries: {{ service.Spec.TaskTemplate.ContainerSpec.Healthcheck.Retries ?? 'default' }}</div>
                      </div>
                    </v-col>
                  </v-row>
                </v-col>

                <v-divider />
                <v-col cols="12">
                  <v-row>
                    <v-col cols="1"></v-col>
                    <v-col cols="2">
                      <strong>Hosts</strong>
                    </v-col>
                    <v-col cols="8">
                      <div v-if="service.Spec.TaskTemplate.ContainerSpec?.Hosts">
                        <strong>Hosts:</strong>
                        <ul>
                          <li v-for="host in service.Spec.TaskTemplate.ContainerSpec.Hosts" :key="host">{{ host }}</li>
                        </ul>
                      </div>
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
                      <ul>
                        <li v-for="(value, key) in (service.Spec.Labels ?? {})" :key="key">{{ key }}: {{ value }}</li>
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
                      <strong>Placement Constraints</strong>
                    </v-col>

                    <v-col cols="8">
                      <ul>
                        <li v-for="constraint in (service.Spec.Placement?.Constraints ?? [])" :key="constraint">{{ constraint }}</li>
                      </ul>
                    </v-col>
                  </v-row>
                </v-col>
              
                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Resources</strong>
                    </v-col>
                    <v-col cols="8">
                      <div>Reservations: {{ (service.Spec.TaskTemplate.Resources?.Reservations?.NanoCPUs ?? 0) / 1e9 }} vCPUs / {{ formatBytes(service.Spec.TaskTemplate.Resources?.Reservations?.MemoryBytes) }}</div>
                      <div>Limits: {{ (service.Spec.TaskTemplate.Resources?.Limits?.NanoCPUs ?? 0) / 1e9 }} vCPUs / {{ formatBytes(service.Spec.TaskTemplate.Resources?.Limits?.MemoryBytes) }}</div>
                    </v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Restart Policy</strong>
                    </v-col>
                    <v-col cols="8" v-if="service.Spec.TaskTemplate.RestartPolicy">
                      <div>Condition: {{ service.Spec.TaskTemplate.RestartPolicy.Condition }}</div>
                      <div v-if="service.Spec.TaskTemplate.RestartPolicy.Delay">Delay: {{ service.Spec.TaskTemplate.RestartPolicy.Delay / 1e9 }}s</div>
                      <div v-if="service.Spec.TaskTemplate.RestartPolicy.MaxAttempts">Max Attempts: {{ service.Spec.TaskTemplate.RestartPolicy.MaxAttempts }}</div>
                    </v-col>
                  </v-row>
                </v-col>

                <v-divider />

                <v-col cols="12">
                  <v-row>
                    <v-col cols="3">
                      <strong>Update Config</strong>
                    </v-col>
                    <v-col cols="8" v-if="service.Spec.UpdateConfig">
                      <div>Parallelism: {{ service.Spec.UpdateConfig.Parallelism }}</div>
                      <div v-if="service.Spec.UpdateConfig.Delay">Delay: {{ service.Spec.UpdateConfig.Delay / 1e9 }}s</div>
                      <div>Failure Action: {{ service.Spec.UpdateConfig.FailureAction }}</div>
                      <div>Order: {{ service.Spec.UpdateConfig.Order }}</div>
                    </v-col>
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
