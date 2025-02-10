export default {
    template: `
        <v-list-item-group>
            <v-list-item v-for="node in nodes" :key="node.id">
                <v-list-item-content @click="selectNode(node.id)">
                    <v-list-item-title>{{ node.hostname }}</v-list-item-title>
                    <v-list-item-subtitle>{{ node.status }}</v-list-item-subtitle>
                    <v-list>
                        <v-list-item v-for="task in node.tasks" :key="task.id">
                            <v-list-item-content>
                                <v-list-item-title>{{ task.name }}</v-list-item-title>
                                <v-list-item-subtitle>{{ task.status }}</v-list-item-subtitle>
                            </v-list-item-content>
                        </v-list-item>
                    </v-list>
                </v-list-item-content>
            </v-list-item>
        </v-list-item-group>
    `,
    props: {
        nodes: Array
    },
    data() {
        return {
            selectedNodeId: null
        };
    },
    methods: {
        selectNode(id) {
            this.selectedNodeId = id;
            console.log('Selected Node ID:', this.selectedNodeId);
        }
    }
}
