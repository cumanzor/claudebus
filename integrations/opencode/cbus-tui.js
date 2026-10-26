// cbus TUI plugin: serves the session this pane shows to the cbus daemon over a
// private Unix socket. The wire format is the OpenCode plugin section of
// docs/architecture/protocol.md. The daemon owns holding, retries and liveness;
// this plugin only reports state and submits when asked.
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";

export const PROTOCOL = 1;
export const PLUGIN_VERSION = "1";

const ROUTE_POLL_MS = 100;
const REFRESH_RETRY_MIN_MS = 250;
const REFRESH_RETRY_MAX_MS = 10_000;
const REQUEST_TIMEOUT_MS = 10_000;
const MAX_REQUEST_BYTES = 1 << 20;
const SOCKET_PATH_MAX = 103;
const SESSION_ID = /^ses_[A-Za-z0-9]{20,64}$/;
const MESSAGE_ID = /^msg_[A-Za-z0-9]{20,64}$/;

export function endpointDir(env = process.env) {
  const base = env.CBUS_DIR || path.join(os.homedir(), ".claude-bus");
  return path.join(base, ".opencode");
}

export default { id: "cbus.tui", tui };

async function tui(api) {
  await start(api, { dir: endpointDir(), pid: process.pid });
}

function errorText(e) {
  if (typeof e === "string") return e;
  return e?.data?.message ?? e?.message ?? (() => { try { return JSON.stringify(e); } catch { return String(e); } })();
}

// Classify an SDK failure. A connection that never opened submitted nothing.
export function classify(e) {
  const text = errorText(e);
  if (/session not found/i.test(text)) return { code: "session-not-found", error: text };
  if (/unable to connect|ECONNREFUSED|fetch failed|connection refused/i.test(text)) return { code: "server-unreachable", error: text };
  if (/message not found|not found/i.test(text)) return { code: "message-not-found", error: text };
  return { code: "failed", error: text };
}

export async function start(api, { dir, pid }) {
  const sock = path.join(dir, `${pid}.sock`);
  // Node unlinks a Unix socket's listen path on close. Listening on a private
  // name and renaming it into place means close never removes a later
  // instance's socket at the public path. Both names must fit the OS limit.
  const listenPath = path.join(dir, `.${pid}.${Math.random().toString(36).slice(2, 8)}.sock`);
  for (const p of [sock, listenPath]) {
    if (Buffer.byteLength(p) > SOCKET_PATH_MAX) {
      writeError(dir, pid, `socket path exceeds ${SOCKET_PATH_MAX} bytes: ${p}`);
      return undefined;
    }
  }
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  fs.chmodSync(dir, 0o700);
  if (!(await claimPath(sock))) {
    writeError(dir, pid, `another live endpoint answers at ${sock}`);
    return undefined;
  }

  const statuses = new Map();
  const watchers = new Set();
  let disposed = false;

  const shown = () => {
    const r = api.route.current;
    return r?.name === "session" && typeof r.params?.sessionID === "string" ? r.params.sessionID : null;
  };
  // An absent status entry means idle; nothing is known while a refresh runs.
  const stateOf = (sid) => (!sid ? null : refreshing ? "unknown" : statuses.get(sid) ?? "idle");
  const emit = (ev) => {
    const line = JSON.stringify(ev) + "\n";
    for (const w of watchers) w.write(line);
  };
  // OpenCode repeats status events; watchers see only changes for the shown session.
  let reported = null;
  const reportState = (sid, state) => {
    const key = `${sid}:${state}`;
    if (key === reported) return;
    reported = key;
    emit({ event: "status", sessionID: sid, state });
  };

  // A reconnected server may have lost its in-memory status, so the map is
  // replaced from a fresh read. Until a read lands every state is unknown,
  // which keeps delivery held; a failed read is retried, only the newest
  // read applies, and events that arrive meanwhile win over it.
  let refreshing = false;
  let generation = 0;
  let racing = new Map();
  let retryTimer;
  let retryDelay = REFRESH_RETRY_MIN_MS;
  async function refreshStatuses() {
    const mine = ++generation;
    refreshing = true;
    racing = new Map();
    clearTimeout(retryTimer);
    let fresh = new Map();
    try {
      const r = await api.client.session.status(undefined, { throwOnError: true });
      for (const [sid, s] of Object.entries(r?.data ?? {})) fresh.set(sid, s?.type ?? "idle");
    } catch {
      fresh = null;
    }
    if (mine !== generation || disposed) return;
    if (!fresh) {
      retryTimer = setTimeout(refreshStatuses, retryDelay);
      retryTimer.unref?.();
      retryDelay = Math.min(retryDelay * 2, REFRESH_RETRY_MAX_MS);
      return;
    }
    retryDelay = REFRESH_RETRY_MIN_MS;
    for (const [sid, type] of racing) fresh.set(sid, type);
    statuses.clear();
    for (const [sid, type] of fresh) statuses.set(sid, type);
    refreshing = false;
    const sid = shown();
    if (sid) reportState(sid, stateOf(sid));
  }
  const setStatus = (sid, type) => {
    statuses.set(sid, type);
    if (refreshing) racing.set(sid, type);
  };
  await refreshStatuses();

  let lastShown = shown();
  const poll = setInterval(() => {
    const now = shown();
    if (now !== lastShown) {
      lastShown = now;
      reported = now ? `${now}:${stateOf(now)}` : null;
      emit({ event: "route", sessionID: now, state: stateOf(now) });
    }
  }, ROUTE_POLL_MS);
  poll.unref?.();

  const unsubscribe = [
    api.event.on("session.status", (e) => {
      const sid = e?.properties?.sessionID;
      if (typeof sid !== "string") return;
      const type = e.properties.status?.type ?? "idle";
      setStatus(sid, type);
      if (sid === shown() && !refreshing) reportState(sid, type);
    }),
    api.event.on("session.idle", (e) => {
      const sid = e?.properties?.sessionID;
      if (typeof sid !== "string") return;
      setStatus(sid, "idle");
      if (sid === shown() && !refreshing) reportState(sid, "idle");
    }),
    api.event.on("session.deleted", (e) => {
      const sid = e?.properties?.sessionID ?? e?.properties?.info?.id;
      if (typeof sid !== "string") return;
      statuses.delete(sid);
      emit({ event: "session.deleted", sessionID: sid });
    }),
    api.event.on("server.connected", () => {
      emit({ event: "server.connected" });
      refreshStatuses();
    }),
  ];

  async function lookup(sessionID, messageID) {
    try {
      const r = await api.client.session.message({ sessionID, messageID }, { throwOnError: true });
      const parts = r?.data?.parts ?? [];
      return { present: true, texts: parts.filter((p) => p?.type === "text").map((p) => p.text) };
    } catch (e) {
      const c = classify(e);
      if (c.code === "message-not-found") return { present: false };
      return { failure: c };
    }
  }

  async function handle(req, conn) {
    switch (req?.op) {
      case "hello":
        return { ok: true, protocol: PROTOCOL, plugin: PLUGIN_VERSION, pid, sessionID: shown() };
      case "status":
        return { ok: true, sessionID: shown(), state: stateOf(shown()) };
      case "watch":
        reported = shown() ? `${shown()}:${stateOf(shown())}` : null;
        watchers.add(conn);
        conn.on("close", () => watchers.delete(conn));
        return { ok: true, event: "snapshot", sessionID: shown(), state: stateOf(shown()) };
      case "lookup": {
        if (!SESSION_ID.test(req.sessionID ?? "") || !MESSAGE_ID.test(req.messageID ?? "")) return { ok: false, code: "bad-request", error: "lookup needs sessionID and messageID" };
        const r = await lookup(req.sessionID, req.messageID);
        return r.failure ? { ok: false, ...r.failure } : { ok: true, ...r };
      }
      case "session": {
        if (!SESSION_ID.test(req.sessionID ?? "")) return { ok: false, code: "bad-request", error: "session needs sessionID" };
        try {
          await api.client.session.get({ sessionID: req.sessionID }, { throwOnError: true });
          return { ok: true, exists: true };
        } catch (e) {
          const c = classify(e);
          if (c.code === "session-not-found") return { ok: true, exists: false };
          return { ok: false, ...c };
        }
      }
      case "deliver":
        return deliver(req);
      default:
        return { ok: false, code: "bad-request", error: `unknown op ${JSON.stringify(req?.op)}` };
    }
  }

  async function deliver(req) {
    if (!SESSION_ID.test(req.sessionID ?? "") || !MESSAGE_ID.test(req.messageID ?? "") || typeof req.text !== "string" || req.text === "") {
      return { ok: false, code: "bad-request", submitted: false, error: "deliver needs sessionID, messageID and text" };
    }
    if (req.sessionID !== shown()) {
      return { ok: false, code: "session-not-shown", submitted: false, sessionID: shown() };
    }
    const prior = await lookup(req.sessionID, req.messageID);
    if (prior.failure) return { ok: false, ...prior.failure, submitted: false };
    if (prior.present) return { ok: true, receipt: "present", texts: prior.texts };
    try {
      await api.client.session.promptAsync(
        { sessionID: req.sessionID, messageID: req.messageID, parts: [{ type: "text", text: req.text }] },
        { throwOnError: true },
      );
    } catch (e) {
      const c = classify(e);
      // only a refused connection or a missing session proves nothing was stored
      const submitted = c.code === "server-unreachable" || c.code === "session-not-found" ? false : "unknown";
      return { ok: false, ...c, submitted };
    }
    const after = await lookup(req.sessionID, req.messageID);
    if (after.present) return { ok: true, receipt: "persisted", texts: after.texts };
    return { ok: true, receipt: "submitted-unconfirmed" };
  }

  const server = net.createServer((conn) => {
    let buf = "";
    let answered = false;
    const timer = setTimeout(() => conn.destroy(), REQUEST_TIMEOUT_MS);
    timer.unref?.();
    conn.setEncoding("utf8");
    conn.on("error", () => {});
    conn.on("close", () => clearTimeout(timer));
    conn.on("data", async (chunk) => {
      if (answered) return;
      buf += chunk;
      if (buf.length > MAX_REQUEST_BYTES) return conn.destroy();
      const nl = buf.indexOf("\n");
      if (nl < 0) return;
      answered = true;
      clearTimeout(timer);
      let req;
      try { req = JSON.parse(buf.slice(0, nl)); } catch { req = undefined; }
      let res;
      try { res = await handle(req, conn); } catch (e) { res = { ok: false, code: "failed", error: errorText(e) }; }
      if (res?.event === "snapshot") conn.write(JSON.stringify(res) + "\n");
      else conn.end(JSON.stringify(res) + "\n");
    });
  });
  let bound;
  try {
    await new Promise((resolve, reject) => {
      server.once("error", reject);
      server.listen(listenPath, () => { server.off("error", reject); resolve(); });
    });
    fs.chmodSync(listenPath, 0o600);
    fs.renameSync(listenPath, sock);
    bound = fs.statSync(sock).ino;
  } catch (e) {
    server.close();
    clearInterval(poll);
    for (const u of unsubscribe) u?.();
    try { fs.rmSync(listenPath, { force: true }); } catch {}
    writeError(dir, pid, `could not bind ${sock}: ${errorText(e)}`);
    return undefined;
  }

  const close = () => {
    if (disposed) return;
    disposed = true;
    clearInterval(poll);
    clearTimeout(retryTimer);
    for (const u of unsubscribe) u?.();
    emit({ event: "dispose" });
    for (const w of watchers) w.end();
    server.close();
    try { if (fs.statSync(sock).ino === bound) fs.rmSync(sock); } catch {}
  };
  api.lifecycle.onDispose(close);
  return { path: sock, close };
}

// Unlink a leftover path only when nothing answers there.
function claimPath(sock) {
  return new Promise((resolve) => {
    if (!fs.existsSync(sock)) return resolve(true);
    const probe = net.createConnection(sock);
    probe.once("connect", () => { probe.destroy(); resolve(false); });
    probe.once("error", (e) => {
      if (e.code === "ECONNREFUSED" || e.code === "ENOENT") {
        try { fs.rmSync(sock, { force: true }); } catch {}
        resolve(true);
      } else resolve(false);
    });
  });
}

function writeError(dir, pid, message) {
  try {
    fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
    fs.writeFileSync(path.join(dir, `${pid}.error`), message + "\n", { mode: 0o600 });
  } catch {}
}
