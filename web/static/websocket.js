export default {
  template: `<span />`,
  data() {
    return {
      ws: null,
      reconnectAttempts: 0,
      maxReconnectInterval: 30000 // 30 seconds
    }
  },
  mounted() {
    this.connectWebSocket();
  },
  methods: {
    connectWebSocket() {
      const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
      this.ws = new WebSocket(proto + '://' + window.location.host + window.location.pathname + 'ws');

      this.ws.onopen = () => {
        console.log('WebSocket connection established');
        this.reconnectAttempts = 0;
      };

      this.ws.onmessage = (event) => {
        const data = event.data;
        if (typeof(data) === 'string' && data.startsWith('401-Unauthorized')) {
          this.$emit('not-authorized');
        }

        const json = JSON.parse(event.data);
        this.$emit('update', json);
      };

      this.ws.onclose = () => {
        console.log('WebSocket connection closed');
        this.reconnectWebSocket();
      };

      this.ws.onerror = (error) => {
        console.error('WebSocket error:', error);
      };
    },
    reconnectWebSocket() {
      let reconnectInterval = Math.min(1000 * Math.pow(2, this.reconnectAttempts), this.maxReconnectInterval);

      setTimeout(() => {
        console.log(`Reconnecting in ${reconnectInterval / 1000} seconds...`);
        this.connectWebSocket();
      }, reconnectInterval);

      this.reconnectAttempts++;
    },
  },
  beforeDestroy() {
    if (this.ws) {
      this.ws.close();
    }
  }
}
  