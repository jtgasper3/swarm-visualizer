export default {
  name: 'WebSocket',
  template: `<slot name="icon" :state="state"></slot>`,
  data() {
    return {
      ws: null,
      reconnectAttempts: 0,
      maxReconnectInterval: 30000, // 30 seconds
      connected: false,
    }
  },
  computed: {
    state() {
      if (this.connected) return 'connected';
      if (this.reconnectAttempts > 0) return 'reconnecting';
      return 'connecting';
    }
  },
  watch: {
    state(newState) {
      this.$emit('state-change', newState);
    }
  },
  emits: ['update', 'not-authorized', 'state-change'],
  mounted() {
    this.connectWebSocket();
  },
  methods: {
    connectWebSocket() {
      const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
      this.ws = new WebSocket(proto + '://' + window.location.host + window.location.pathname + 'ws');

      this.ws.onopen = () => {
        console.log('WebSocket connection established');
        this.connected = true;
        this.reconnectAttempts = 0;
      };

      this.ws.onmessage = (event) => {
        const data = event.data;
        if (typeof(data) === 'string' && data.startsWith('401-Unauthorized')) {
          this.$emit('not-authorized');
          return;
        }

        try {
          const json = JSON.parse(data);
          this.$emit('update', json);
        } catch (e) {
          console.error('Failed to parse WebSocket message:', e);
        }
      };

      this.ws.onclose = () => {
        console.log('WebSocket connection closed');
        this.connected = false;
        this.reconnectWebSocket();
      };

      this.ws.onerror = (error) => {
        console.error('WebSocket error:', error);
      };
    },
    reconnectWebSocket() {
      this.reconnectAttempts++;
      const reconnectInterval = Math.min(1000 * Math.pow(2, this.reconnectAttempts), this.maxReconnectInterval);
      console.log(`Reconnecting in ${reconnectInterval / 1000} seconds...`);
      setTimeout(() => {
        this.connectWebSocket();
      }, reconnectInterval);
    },
  },
  beforeDestroy() {
    if (this.ws) {
      this.ws.close();
    }
  }
}
