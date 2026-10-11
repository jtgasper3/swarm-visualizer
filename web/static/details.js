import { formatBytes } from './utils.js';
import { closeDetails, returnFocus, showDetails, target } from './state.js';

// Details wraps whatever opens the details of a node, service or task. It
// hands its slot the handler to bind, and the dialog itself is DetailsDialog,
// of which the page has one.
const Details = {
  name: 'Details',
  props: {
    node: Object,
    service: Object,
    task: Object,
  },
  methods: {
    open(event) {
      const from = event?.currentTarget;
      if (this.node) {
        showDetails('node', this.node.ID, from);
      } else if (this.service) {
        showDetails('service', this.service.ID, from);
      } else if (this.task) {
        showDetails('task', this.task.ID, from);
      }
    },
  },
  render() {
    return this.$slots.default?.({ onClick: this.open, 'aria-haspopup': 'dialog' });
  },
};

export default Details;

// One labelled line of a details card. `indent` marks a line that belongs to
// the heading above it.
const DetailRow = {
  name: 'DetailRow',
  props: {
    label: String,
    indent: Boolean,
    divider: { type: Boolean, default: true },
  },
  template: `
    <v-divider v-if="divider" />
    <v-col cols="12">
      <v-row>
        <v-col v-if="indent" cols="1"></v-col>
        <v-col :cols="indent ? 2 : 3"><strong>{{ label }}</strong></v-col>
        <v-col cols="8"><slot /></v-col>
      </v-row>
    </v-col>
  `,
};

// The lines that describe a container, which a service and each of its tasks
// both have. `known` is the list of networks to take names from.
const ContainerSpecRows = {
  name: 'ContainerSpecRows',
  components: { DetailRow },
  props: {
    spec: Object,
    image: String,
    // Keep a long image name on one line, cut at the registry end.
    truncateImage: Boolean,
    attachments: Array,
    known: Array,
    labels: Object,
    showHosts: Boolean,
  },
  methods: {
    networkName(id) {
      return this.known?.find(network => network.Id === id)?.Name ?? id;
    },
    seconds(nanoseconds) {
      return nanoseconds ? nanoseconds / 1e9 + 's' : 'default';
    },
  },
  template: `
    <v-divider />
    <v-col cols="12">
      <v-row>
        <v-col cols="12"><strong>Container Spec</strong></v-col>
      </v-row>
    </v-col>

    <DetailRow label="Image" indent :divider="false">
      <span v-if="truncateImage" style="display: block; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; direction: rtl; text-align: left;">{{ image }}</span>
      <template v-else>{{ image }}</template>
    </DetailRow>

    <DetailRow label="Command/ Args" indent>{{ spec?.Args }}</DetailRow>

    <DetailRow label="Environment Variables" indent>
      <ul>
        <li v-for="env in (spec?.Env ?? [])" :key="env">{{ env }}</li>
      </ul>
    </DetailRow>

    <DetailRow label="Mounts" indent>
      <ul>
        <li v-for="mount in (spec?.Mounts ?? [])" :key="mount.Target">{{ mount.Type }}: {{ mount.Target }}<span v-if="mount.ReadOnly"> (ReadOnly)</span></li>
      </ul>
    </DetailRow>

    <DetailRow label="Configs" indent>
      <ul>
        <li v-for="config in (spec?.Configs ?? [])" :key="config.ConfigID">{{ config.ConfigName }}</li>
      </ul>
    </DetailRow>

    <DetailRow label="Secrets" indent>
      <ul>
        <li v-for="secret in (spec?.Secrets ?? [])" :key="secret.SecretID">{{ secret.SecretName }}</li>
      </ul>
    </DetailRow>

    <DetailRow label="Networks" indent>
      <ul>
        <li v-for="net in (attachments ?? [])" :key="net.Target">{{ networkName(net.Target) }} ({{ net.Target }})<span v-if="net.Aliases?.length">: {{ net.Aliases.join(', ') }}</span></li>
      </ul>
    </DetailRow>

    <DetailRow label="Healthcheck" indent>
      <div v-if="spec?.Healthcheck">
        <strong>Healthcheck:</strong>
        <ul>
          <li v-for="cmd in spec.Healthcheck.Test" :key="cmd">{{ cmd }}</li>
        </ul>
        <div>Interval: {{ seconds(spec.Healthcheck.Interval) }}</div>
        <div>Timeout: {{ seconds(spec.Healthcheck.Timeout) }}</div>
        <div>Retries: {{ spec.Healthcheck.Retries ?? 'default' }}</div>
      </div>
    </DetailRow>

    <DetailRow v-if="showHosts" label="Hosts" indent>
      <div v-if="spec?.Hosts">
        <strong>Hosts:</strong>
        <ul>
          <li v-for="host in spec.Hosts" :key="host">{{ host }}</li>
        </ul>
      </div>
    </DetailRow>

    <DetailRow label="Labels" indent>
      <ul>
        <li v-for="(value, key) in (labels ?? {})" :key="key">{{ key }}: {{ value }}</li>
      </ul>
    </DetailRow>
  `,
};

// Reservations and limits, as a service's task template or a task's own spec
// states them.
const ResourceLines = {
  name: 'ResourceLines',
  props: { resources: Object },
  data() {
    return { formatBytes };
  },
  template: `
    <div>Reservations: {{ (resources?.Reservations?.NanoCPUs ?? 0) / 1e9 }} vCPUs / {{ formatBytes(resources?.Reservations?.MemoryBytes) }}</div>
    <div>Limits: {{ (resources?.Limits?.NanoCPUs ?? 0) / 1e9 }} vCPUs / {{ formatBytes(resources?.Limits?.MemoryBytes) }}</div>
  `,
};

const RestartPolicyLines = {
  name: 'RestartPolicyLines',
  props: { policy: Object },
  template: `
    <div>Condition: {{ policy.Condition }}</div>
    <div v-if="policy.Delay">Delay: {{ policy.Delay / 1e9 }}s</div>
    <div v-if="policy.MaxAttempts">Max Attempts: {{ policy.MaxAttempts }}</div>
  `,
};

// DetailsDialog shows the details of whichever node, service or task the
// address names. There is one for the page, rather than one behind every
// button that can open it: a link to it then works whether or not the card it
// belongs to is on screen, and going from a task to its service is a step that
// Back undoes.
export const DetailsDialog = {
  name: 'DetailsDialog',
  components: {
    ContainerSpecRows,
    DetailRow,
    Details,
    ResourceLines,
    RestartPolicyLines,
  },
  props: {
    nodes: Array,
    services: Array,
    tasks: Array,
  },
  data() {
    return { formatBytes };
  },
  computed: {
    node() {
      return this.find('node', this.nodes);
    },
    service() {
      return this.find('service', this.services);
    },
    task() {
      return this.find('task', this.tasks);
    },
    open() {
      return Boolean(this.node || this.service || this.task);
    },
    title() {
      if (this.node) {
        return `${this.node.Description.Hostname} Node`;
      } else if (this.service) {
        return `${this.service.Spec.Name} Service`;
      } else if (this.task) {
        return `${this.task.service?.Spec.Name ?? this.task.ServiceID}.${this.task.Slot ?? this.task.ID.substring(0, 6)} Task`;
      } else {
        return '';
      }
    },
  },
  watch: {
    // However the dialog came to close: its button, Escape, or Back.
    open(isOpen) {
      if (!isOpen) this.$nextTick(returnFocus);
    },
  },
  methods: {
    // The target stays in the address while the thing it names is missing: a
    // link followed before the first snapshot arrives opens once it does.
    find(kind, items) {
      const wanted = target.value;
      return wanted?.kind === kind ? (items ?? []).find(item => item.ID === wanted.id) : undefined;
    },
    close() {
      closeDetails();
    },
    date(value) {
      return new Date(value).toLocaleString();
    },
  },
  template: `
    <v-dialog
      :model-value="open"
      @update:model-value="close"
      fullscreen
      scrollable
      :aria-label="title + ' Details'"
    >
      <v-card
        flat
        rounded="lg"
        class="d-flex flex-column mb-2"
      >
        <v-toolbar>
            <v-btn
              icon="mdi-close"
              @click="close"
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
                <DetailRow label="Hostname">{{ node.Description.Hostname }}</DetailRow>
                <DetailRow label="Role">
                  {{ node.Spec.Role }}
                   <span v-if="node.ManagerStatus?.Leader">(leader)</span>
                </DetailRow>
                <DetailRow label="Availability">{{ node.Spec.Availability }}</DetailRow>
                <DetailRow label="State">{{ node.Status.State }}</DetailRow>
                <DetailRow label="Address">{{ node.Status.Addr }}</DetailRow>
                <DetailRow v-if="node.ManagerStatus" label="Manager">{{ node.ManagerStatus.Reachability }} — {{ node.ManagerStatus.Addr }}</DetailRow>
                <DetailRow v-if="node.Description.Platform" label="Platform">{{ node.Description.Platform.OS }} on {{ node.Description.Platform.Architecture }}</DetailRow>
                <DetailRow label="Resources">{{ (node.Description.Resources?.NanoCPUs ?? 0) / 1e9 }} vCPUs / {{ formatBytes(node.Description.Resources?.MemoryBytes) }}</DetailRow>
                <DetailRow label="Labels">
                  <ul>
                    <li v-for="label in Object.entries(node.Spec.Labels ?? {}).map(([key, value]) => key + '=' + value)" :key="label">{{ label }}</li>
                  </ul>
                </DetailRow>
                <DetailRow label="Engine Version">{{ node.Description.Engine?.EngineVersion }}</DetailRow>
                <DetailRow label="Created">{{ date(node.CreatedAt) }}</DetailRow>
                <DetailRow label="Updated">{{ date(node.UpdatedAt) }}</DetailRow>
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
                <DetailRow label="Service ID">
                  <Details :service="task.service" v-slot="props">
                    <v-btn color="primary" density="compact" slim variant="text" v-bind="props" class="text-none" aria-label="Service Details">{{ task.ServiceID }}</v-btn>
                  </Details>
                </DetailRow>
                <DetailRow label="Node ID">{{ task.NodeID }}</DetailRow>
                <DetailRow label="Status">
                  {{ task.Status.State }}
                  <span v-if="task.DesiredState && task.DesiredState !== task.Status.State"> (desired: {{ task.DesiredState }})</span>
                  <div v-if="task.Status.Message" class="text-medium-emphasis text-body-2">{{ task.Status.Message }}</div>
                  <div v-if="task.Status.Err" class="text-error text-body-2">{{ task.Status.Err }}</div>
                </DetailRow>
                <DetailRow label="Slot">{{ task.Slot }}</DetailRow>
                <DetailRow label="Container ID">{{ task.Status.ContainerStatus?.ContainerID }}</DetailRow>

                <ContainerSpecRows
                  :spec="task.Spec.ContainerSpec"
                  :image="task.Spec.ContainerSpec?.Image"
                  :attachments="task.Spec.Networks"
                  :known="task.service?.networks"
                  :labels="task.Spec.ContainerSpec?.Labels"
                />

                <DetailRow label="Resources"><ResourceLines :resources="task.Spec.Resources" /></DetailRow>
                <DetailRow label="Restart Policy">
                  <RestartPolicyLines v-if="task.Spec.RestartPolicy" :policy="task.Spec.RestartPolicy" />
                  <template v-else>—</template>
                </DetailRow>
                <DetailRow label="Created">{{ date(task.CreatedAt) }}</DetailRow>
                <DetailRow label="Updated">{{ date(task.UpdatedAt) }}</DetailRow>
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
                <DetailRow label="ID">{{ service.ID }}</DetailRow>
                <DetailRow label="Published Ports">
                  <ul>
                    <li v-for="port in (service.Endpoint?.Ports ?? service.Spec.EndpointSpec?.Ports ?? [])" :key="port.PublishedPort">
                      {{ port.Protocol }}: {{ port.PublishedPort }} → {{ port.TargetPort }}
                      <span v-if="port.PublishMode"> ({{ port.PublishMode }})</span>
                    </li>
                  </ul>
                </DetailRow>

                <ContainerSpecRows
                  :spec="service.Spec.TaskTemplate.ContainerSpec"
                  :image="service.Spec.TaskTemplate.ContainerSpec?.Image?.split('@')[0]"
                  truncate-image
                  :attachments="service.Spec.TaskTemplate.Networks"
                  :known="service.networks"
                  :labels="service.Spec.Labels"
                  show-hosts
                />

                <DetailRow label="Mode">{{ service.Spec.Mode.Replicated ? 'Replicated' : 'Global' }} {{ service.Spec.Mode.Replicated ? '(' + service.Spec.Mode.Replicated.Replicas + ')' : '' }}</DetailRow>
                <DetailRow label="Placement Constraints">
                  <ul>
                    <li v-for="constraint in (service.Spec.TaskTemplate.Placement?.Constraints ?? [])" :key="constraint">{{ constraint }}</li>
                  </ul>
                </DetailRow>
                <DetailRow label="Resources"><ResourceLines :resources="service.Spec.TaskTemplate.Resources" /></DetailRow>
                <DetailRow label="Restart Policy">
                  <RestartPolicyLines v-if="service.Spec.TaskTemplate.RestartPolicy" :policy="service.Spec.TaskTemplate.RestartPolicy" />
                </DetailRow>
                <DetailRow label="Update Config">
                  <template v-if="service.Spec.UpdateConfig">
                    <div>Parallelism: {{ service.Spec.UpdateConfig.Parallelism }}</div>
                    <div v-if="service.Spec.UpdateConfig.Delay">Delay: {{ service.Spec.UpdateConfig.Delay / 1e9 }}s</div>
                    <div>Failure Action: {{ service.Spec.UpdateConfig.FailureAction }}</div>
                    <div>Order: {{ service.Spec.UpdateConfig.Order }}</div>
                  </template>
                </DetailRow>
                <DetailRow label="Created">{{ date(service.CreatedAt) }}</DetailRow>
                <DetailRow label="Updated">{{ date(service.UpdatedAt) }}</DetailRow>
              </v-row>
            </v-container>
          </template>
        </v-card>
      </v-card>
    </v-dialog>
  `,
};
