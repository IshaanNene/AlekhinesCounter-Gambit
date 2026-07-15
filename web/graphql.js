// Minimal GraphQL client: fetch for queries/mutations, and a hand-rolled
// implementation of the graphql-transport-ws protocol for subscriptions.
//
// No dependencies — the protocol is small enough that a client library would
// cost more than it saves: connection_init → connection_ack → subscribe → next*.
// Requests are same-origin; NGINX proxies /graphql and /ws to the gateway.

const HTTP_ENDPOINT = "/graphql";
const WS_ENDPOINT = "/ws";
const WS_PROTOCOL = "graphql-transport-ws";

/** Raised when the server returns GraphQL errors, carrying the first message. */
export class GraphQLError extends Error {
  constructor(errors) {
    const first = errors?.[0]?.message ?? "unknown GraphQL error";
    super(first);
    this.name = "GraphQLError";
    this.errors = errors;
  }
}

/** Execute a query or mutation. Resolves to `data`, throws on errors. */
export async function gql(query, variables = {}) {
  const res = await fetch(HTTP_ENDPOINT, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ query, variables }),
  });
  if (!res.ok) throw new Error(`HTTP ${res.status} from ${HTTP_ENDPOINT}`);
  const body = await res.json();
  if (body.errors?.length) throw new GraphQLError(body.errors);
  return body.data;
}

/**
 * A subscription socket that survives drops.
 *
 * Only one subscription is active at a time (this client watches one game), so
 * `subscribe` replaces any previous one. On disconnect it reconnects with
 * exponential backoff and re-issues the last subscription, which is what makes
 * a live board resilient to a gateway restart or a laptop waking from sleep.
 */
export class Subscriber {
  #socket = null;
  #active = null; // { query, variables, onNext }
  #acked = false;
  #retries = 0;
  #reconnectTimer = null;
  #closedByUs = false;
  #nextId = 1;

  /** @param {(state: "connecting"|"live"|"offline"|"error", detail?: string) => void} onState */
  constructor(onState = () => {}) {
    this.onState = onState;
  }

  subscribe(query, variables, onNext) {
    this.#active = { query, variables, onNext };
    if (this.#acked) this.#send(this.#startMessage());
    else this.#connect();
  }

  close() {
    this.#closedByUs = true;
    clearTimeout(this.#reconnectTimer);
    this.#socket?.close();
    this.#socket = null;
  }

  #startMessage() {
    return {
      id: String(this.#nextId++),
      type: "subscribe",
      payload: { query: this.#active.query, variables: this.#active.variables },
    };
  }

  #connect() {
    if (this.#socket) this.#socket.close();
    this.#closedByUs = false;
    this.#acked = false;
    this.onState("connecting");

    const scheme = location.protocol === "https:" ? "wss:" : "ws:";
    const socket = new WebSocket(`${scheme}//${location.host}${WS_ENDPOINT}`, WS_PROTOCOL);
    this.#socket = socket;

    socket.onopen = () => this.#send({ type: "connection_init", payload: {} });

    socket.onmessage = (event) => {
      let msg;
      try {
        msg = JSON.parse(event.data);
      } catch {
        return;
      }
      switch (msg.type) {
        case "connection_ack":
          this.#acked = true;
          this.#retries = 0;
          this.onState("live");
          if (this.#active) this.#send(this.#startMessage());
          break;
        case "next":
          this.#active?.onNext(msg.payload?.data);
          break;
        case "error":
          this.onState("error", msg.payload?.[0]?.message ?? "subscription error");
          break;
        case "ping":
          this.#send({ type: "pong" });
          break;
        case "complete":
          break;
      }
    };

    socket.onclose = () => {
      this.#acked = false;
      if (this.#closedByUs) return;
      this.onState("offline");
      this.#scheduleReconnect();
    };

    socket.onerror = () => this.onState("error", "connection failed");
  }

  #scheduleReconnect() {
    // Exponential backoff, capped, so a downed gateway is not hammered.
    const delay = Math.min(1000 * 2 ** this.#retries, 15000);
    this.#retries += 1;
    clearTimeout(this.#reconnectTimer);
    this.#reconnectTimer = setTimeout(() => this.#connect(), delay);
  }

  #send(obj) {
    if (this.#socket?.readyState === WebSocket.OPEN) {
      this.#socket.send(JSON.stringify(obj));
    }
  }
}
