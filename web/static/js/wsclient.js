// wsclient.js — thin wrapper around the game WebSocket connection (Section 4).
class ShiftSocket {
  constructor() {
    this.handlers = {};
    this.connected = false;
    this.queue = [];
    this._connect();
  }

  _connect() {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    this.ws = new WebSocket(`${proto}//${location.host}/ws`);
    this.ws.onopen = () => {
      this.connected = true;
      this.queue.forEach(msg => this.ws.send(msg));
      this.queue = [];
      this._emit("_open", {});
    };
    this.ws.onclose = () => {
      this.connected = false;
      this._emit("_close", {});
      setTimeout(() => this._connect(), 2000);
    };
    this.ws.onerror = () => {};
    this.ws.onmessage = (evt) => {
      let msg;
      try { msg = JSON.parse(evt.data); } catch (e) { return; }
      this._emit(msg.type, msg);
    };
  }

  on(type, fn) {
    (this.handlers[type] = this.handlers[type] || []).push(fn);
    return this;
  }

  _emit(type, msg) {
    (this.handlers[type] || []).forEach(fn => fn(msg));
    (this.handlers["*"] || []).forEach(fn => fn(msg));
  }

  send(type, data = {}) {
    const payload = JSON.stringify({ type, data });
    if (this.connected) this.ws.send(payload);
    else this.queue.push(payload);
  }
}

const shiftSocket = new ShiftSocket();
