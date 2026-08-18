/*
 * theme.js — the whole client contract in one small file. Copy it, keep it.
 *
 * Your theme never sees a credential. When launched from Konstruct it runs in
 * a sandboxed iframe and sends each operation to the platform window over
 * postMessage; Konstruct validates the request, makes the API call with the
 * user's session on its own origin, and posts the result back. Standalone
 * (opened directly, no parent), isLaunched() is false — render your welcome
 * or sample-data mode.
 */

const theme = (() => {
  const embedded = window.parent !== window;
  const pending = new Map();
  const streams = new Map();
  let seq = 0;

  window.addEventListener("message", (event) => {
    const m = event.data;
    if (!m) return;
    if (m.type === "theme-rpc-result" && pending.has(m.id)) {
      const { resolve, reject, timer } = pending.get(m.id);
      pending.delete(m.id);
      clearTimeout(timer);
      if (m.ok) {
        resolve(m.data);
      } else {
        reject(Object.assign(new Error(m.error || "request failed"), { status: m.status }));
      }
    } else if (m.type === "theme-stream-event" && streams.has(m.id)) {
      let payload = m.data;
      try {
        payload = JSON.parse(m.data);
      } catch (_) {
        /* raw line */
      }
      streams.get(m.id).onEvent(payload);
    } else if (m.type === "theme-stream-close" && streams.has(m.id)) {
      const s = streams.get(m.id);
      streams.delete(m.id);
      if (s.onClose) s.onClose(m.reason);
    }
  });

  function call(op, ...args) {
    if (!embedded) {
      return Promise.reject(new Error("not launched from Konstruct"));
    }
    return new Promise((resolve, reject) => {
      const id = ++seq;
      const timer = setTimeout(() => {
        if (pending.delete(id)) reject(new Error("theme: request timed out"));
      }, 20000);
      pending.set(id, { resolve, reject, timer });
      // The request carries no secrets, so "*" is safe here; the platform
      // replies with targetOrigin pinned to this theme's origin.
      window.parent.postMessage({ type: "theme-rpc", id, op, args }, "*");
    });
  }

  // Streams: the platform pushes events until you unsubscribe (or it closes
  // you — onClose fires with a reason; reconnect by subscribing again).
  function subscribe(op, args, onEvent, onClose) {
    if (!embedded) return () => {};
    const id = ++seq;
    streams.set(id, { onEvent, onClose });
    window.parent.postMessage({ type: "theme-stream-open", id, op, args }, "*");
    return () => {
      if (streams.delete(id)) {
        window.parent.postMessage({ type: "theme-stream-close", id }, "*");
      }
    };
  }

  return {
    isLaunched: () => embedded,
    // Back-compat alias for themes written against the token handoff.
    hasToken: () => embedded,
    discover: (org) => call("discover", org),
    zones: (org) => call("zones", org),
    createZone: (org, zone) => call("createZone", org, zone),
    deleteZone: (org, name) => call("deleteZone", org, name),
    regions: (org) => call("regions", org),
    deployments: (org, name) => call("deployments", org, name),
    apps: (org) => call("apps", org),
    appRepos: (org) => call("appRepos", org),
    shipApp: (app) => call("shipApp", app),
    updateApp: (org, name, body) => call("updateApp", org, name, body),
    deleteApp: (org, name) => call("deleteApp", org, name),
    redeploy: (org, name) => call("redeploy", org, name),
    buildLogs: (org, name) => call("buildLogs", org, name),
    metrics: (org, name, opts) => call("metrics", org, name, opts),
    quota: (org) => call("quota", org),
    character: (org) => call("character", org),
    saveCharacter: (org, spec) => call("saveCharacter", org, spec),
    logs: (org, name, onLine, onClose) => subscribe("logs", [org, name], onLine, onClose),
    appEvents: (org, onChange, onClose) => subscribe("appEvents", [org], onChange, onClose),
  };
})();
