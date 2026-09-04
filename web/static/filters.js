// Shared grammar for every filter list in the navigation drawer.
//
// A row is: [tri-state box] [label] [n/m tally or meta] [Only] [chevron]
// Selection state is always passed in as { on, total, kind }, derived by the
// caller from the flat *Selection arrays - never stored alongside them.

export const FilterSection = {
  name: 'FilterSection',
  emits: ['toggle', 'update:open'],
  props: {
    title: { type: String, required: true },
    state: { type: Object, required: true },
    open: { type: Boolean, default: true },
  },
  computed: {
    tallyClass() {
      return this.state.kind === 'some' ? 'filter-tally filter-tally--partial' : 'filter-tally';
    },
  },
  template: `
    <div class="filter-section">
      <div class="filter-section__header">
        <v-btn :icon="open ? 'mdi-chevron-down' : 'mdi-chevron-right'" :aria-expanded="String(open)"
          :aria-label="(open ? 'Collapse ' : 'Expand ') + title" @click="$emit('update:open', !open)"></v-btn>
        <span class="filter-section__title">{{ title }}</span>
        <span :class="tallyClass">{{ state.on }}/{{ state.total }}</span>
        <v-checkbox-btn :model-value="state.kind === 'all'" :indeterminate="state.kind === 'some'"
          :aria-label="'Toggle all ' + title" @update:model-value="$emit('toggle')"></v-checkbox-btn>
      </div>
      <div v-show="open"><slot /></div>
    </div>
  `,
};

export const FilterRow = {
  name: 'FilterRow',
  emits: ['toggle', 'only', 'update:open'],
  props: {
    title: { type: String, required: true },
    // Parent rows pass `state`; leaf rows pass `selected`.
    state: { type: Object, default: null },
    selected: { type: Boolean, default: false },
    meta: { type: String, default: '' },
    showOnly: { type: Boolean, default: false },
    expandable: { type: Boolean, default: false },
    open: { type: Boolean, default: false },
    indent: { type: Boolean, default: false },
    strong: { type: Boolean, default: false },
  },
  computed: {
    checked() { return this.state ? this.state.kind === 'all' : this.selected; },
    indeterminate() { return this.state ? this.state.kind === 'some' : false; },
    tallyClass() {
      return this.state && this.state.kind === 'some' ? 'filter-tally filter-tally--partial' : 'filter-tally';
    },
  },
  template: `
    <div class="filter-row" :class="{ 'filter-row--indent': indent }">
      <v-checkbox-btn :model-value="checked" :indeterminate="indeterminate"
        :aria-label="(checked ? 'Deselect ' : 'Select ') + title" @update:model-value="$emit('toggle')"></v-checkbox-btn>
      <button type="button" class="filter-row__label" :class="{ 'font-weight-bold': strong }" :title="title"
        @click="$emit('toggle')">{{ title }}</button>
      <span v-if="state" :class="tallyClass">{{ state.on }}/{{ state.total }}</span>
      <span v-else-if="meta" class="filter-row__meta">{{ meta }}</span>
      <v-btn v-if="showOnly" class="filter-row__only" :aria-label="'Show only ' + title"
        @click="$emit('only')">Only</v-btn>
      <slot name="append" />
      <v-btn v-if="expandable" :icon="open ? 'mdi-chevron-down' : 'mdi-chevron-right'" :aria-expanded="String(open)"
        :aria-label="(open ? 'Collapse ' : 'Expand ') + title" @click="$emit('update:open', !open)"></v-btn>
    </div>
  `,
};
