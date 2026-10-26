// Run by TestOpencodePluginProtocol (opencode_plugin_test.go) with node --test.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import path from "node:path";
import test from "node:test";

import tuiPlugin, { classify, PROTOCOL, start } from "./cbus-tui.js";
import serverPlugin, { sessionEnv } from "./cbus-server.js";

const S1 = "ses_ea0b7d986ffeHkMekQg7NOClOy";
const S2 = "ses_e9e8cfe0effePOHiD0hga9x45k";
const M1 = "msg_cbustestaaaaaaaaaaaaaaaa01";

function fakeApi() {
  const handlers = new Map();
  const disposers = [];
  const store = new Map();
  const calls = { promptAsync: 0, message: 0, status: 0 };
  const fail = { promptAsync: null, message: null, statusData: null, statusGate: null, sessionGone: null };
  const api = {
    route: { current: { name: "session", params: { sessionID: S1 } } },
    event: {
      on(type, fn) {
        if (!handlers.has(type)) handlers.set(type, new Set());
        handlers.get(type).add(fn);
        return () => handlers.get(type).delete(fn);
      },
    },
    lifecycle: { onDispose: (fn) => disposers.push(fn) },
    client: {
      session: {
        status: async () => {
          calls.status++;
          if (fail.statusReads) {
            const read = {};
            read.done = new Promise((resolve, reject) => { read.resolve = resolve; read.reject = reject; });
            fail.statusReads.push(read);
            return { data: await read.done };
          }
          if (fail.statusError) throw fail.statusError;
          if (fail.statusGate) await fail.statusGate;
          return { data: fail.statusData ?? {} };
        },
        get: async ({ sessionID }) => {
          if (fail.sessionGone?.has(sessionID)) throw new Error(`Session not found: ${sessionID}`);
          return { data: { id: sessionID } };
        },
        message: async ({ sessionID, messageID }) => {
          calls.message++;
          if (fail.message) throw fail.message;
          const m = store.get(`${sessionID}/${messageID}`);
          if (!m) throw { name: "NotFoundError", data: { message: `Message not found: ${messageID}` } };
          return { data: { info: { id: messageID }, parts: m } };
        },
        promptAsync: async ({ sessionID, messageID, parts }) => {
          calls.promptAsync++;
          if (fail.promptAsync) throw fail.promptAsync;
          store.set(`${sessionID}/${messageID}`, parts);
          return { response: { status: 204 } };
        },
      },
    },
  };
  const fire = (type, properties) => { for (const fn of handlers.get(type) ?? []) fn({ type, properties }); };
  const dispose = () => { for (const fn of disposers) fn(); };
  return { api, store, calls, fail, fire, dispose };
}

function tempDir() {
  return fs.mkdtempSync(path.join(process.platform === "win32" ? process.env.TEMP : "/tmp", "cboc-"));
}

function request(sock, req) {
  return new Promise((resolve, reject) => {
    const c = net.createConnection(sock, () => c.write(typeof req === "string" ? req : JSON.stringify(req) + "\n"));
    let buf = "";
    c.setEncoding("utf8");
    c.on("data", (d) => { buf += d; });
    c.on("end", () => resolve(buf ? JSON.parse(buf) : undefined));
    c.on("error", reject);
  });
}

function watch(sock) {
  const events = [];
  const c = net.createConnection(sock, () => c.write(JSON.stringify({ op: "watch" }) + "\n"));
  let buf = "";
  c.setEncoding("utf8");
  c.on("data", (d) => {
    buf += d;
    let nl;
    while ((nl = buf.indexOf("\n")) >= 0) { events.push(JSON.parse(buf.slice(0, nl))); buf = buf.slice(nl + 1); }
  });
  const until = async (n) => { for (let i = 0; i < 100 && events.length < n; i++) await new Promise((r) => setTimeout(r, 10)); return events; };
  return { events, until, ended: new Promise((r) => c.on("end", r)), close: () => c.destroy() };
}

async function setup(t) {
  const dir = tempDir();
  const f = fakeApi();
  const ep = await start(f.api, { dir, pid: 4242 });
  t.after(() => { f.dispose(); fs.rmSync(dir, { recursive: true, force: true }); });
  return { dir, ep, ...f };
}

test("plugin modules export exactly one entry point each", () => {
  assert.equal(typeof tuiPlugin.tui, "function");
  assert.equal(tuiPlugin.server, undefined);
  assert.equal(typeof serverPlugin.server, "function");
  assert.equal(serverPlugin.tui, undefined);
});

test("socket is private and hello reports the shown session", async (t) => {
  const { dir, ep } = await setup(t);
  assert.equal(fs.statSync(dir).mode & 0o777, 0o700);
  assert.equal(fs.statSync(ep.path).mode & 0o777, 0o600);
  assert.deepEqual(await request(ep.path, { op: "hello" }), { ok: true, protocol: PROTOCOL, plugin: "1", pid: 4242, sessionID: S1 });
  assert.deepEqual(await request(ep.path, { op: "status" }), { ok: true, sessionID: S1, state: "idle" });
});

test("deliver persists once and a resend with the same ID is not resubmitted", async (t) => {
  const { ep, calls, store } = await setup(t);
  const req = { op: "deliver", sessionID: S1, messageID: M1, text: "hello" };
  assert.deepEqual(await request(ep.path, req), { ok: true, receipt: "persisted", texts: ["hello"] });
  assert.deepEqual(await request(ep.path, req), { ok: true, receipt: "present", texts: ["hello"] });
  assert.equal(calls.promptAsync, 1);
  assert.equal(store.size, 1);
  assert.deepEqual(await request(ep.path, { op: "lookup", sessionID: S1, messageID: M1 }), { ok: true, present: true, texts: ["hello"] });
});

test("deliver refuses a session the pane does not show, without submitting", async (t) => {
  const { ep, api, calls } = await setup(t);
  assert.deepEqual(await request(ep.path, { op: "deliver", sessionID: S2, messageID: M1, text: "x" }),
    { ok: false, code: "session-not-shown", submitted: false, sessionID: S1 });
  api.route.current = { name: "home" };
  assert.deepEqual(await request(ep.path, { op: "status" }), { ok: true, sessionID: null, state: null });
  assert.equal((await request(ep.path, { op: "deliver", sessionID: S1, messageID: M1, text: "x" })).code, "session-not-shown");
  assert.equal(calls.promptAsync, 0);
});

test("submit failures say whether anything could have been stored", async (t) => {
  const { ep, fail } = await setup(t);
  const deliver = (n) => request(ep.path, { op: "deliver", sessionID: S1, messageID: `msg_cbustestaaaaaaaaaaaaaaaa0${n}`, text: "x" });
  fail.promptAsync = new Error("Unable to connect. Is the computer able to access the url?");
  assert.deepEqual(await deliver(2), { ok: false, code: "server-unreachable", error: fail.promptAsync.message, submitted: false });
  fail.promptAsync = new Error(`Session not found: ${S1}`);
  assert.equal((await deliver(3)).submitted, false);
  fail.promptAsync = new Error("socket hang up");
  assert.deepEqual(await deliver(4), { ok: false, code: "failed", error: "socket hang up", submitted: "unknown" });
});

test("a failed lookup before submit never submits", async (t) => {
  const { ep, fail, calls } = await setup(t);
  fail.message = new Error("Unable to connect. Is the computer able to access the url?");
  const r = await request(ep.path, { op: "deliver", sessionID: S1, messageID: M1, text: "x" });
  assert.equal(r.code, "server-unreachable");
  assert.equal(r.submitted, false);
  assert.equal(calls.promptAsync, 0);
});

test("malformed and unknown requests are refused", async (t) => {
  const { ep, calls } = await setup(t);
  assert.equal((await request(ep.path, "not json\n")).code, "bad-request");
  assert.equal((await request(ep.path, { op: "nope" })).code, "bad-request");
  assert.equal((await request(ep.path, { op: "deliver", sessionID: S1, messageID: "msg_x", text: "x" })).code, "bad-request");
  assert.equal((await request(ep.path, { op: "deliver", sessionID: S1, messageID: M1, text: "" })).code, "bad-request");
  assert.equal(calls.promptAsync, 0);
});

test("watch streams a snapshot, shown-session status, route, deletion, reconnect and dispose", async (t) => {
  const { ep, api, fire, dispose } = await setup(t);
  const w = watch(ep.path);
  await w.until(1);
  fire("session.status", { sessionID: S2, status: { type: "busy" } });
  fire("session.status", { sessionID: S1, status: { type: "busy" } });
  fire("session.status", { sessionID: S1, status: { type: "busy" } });
  fire("session.status", { sessionID: S1, status: { type: "idle" } });
  fire("session.idle", { sessionID: S1 });
  api.route.current = { name: "home" };
  await w.until(4);
  fire("session.deleted", { info: { id: S2 } });
  fire("server.connected", {});
  dispose();
  await w.ended;
  assert.deepEqual(w.events, [
    { ok: true, event: "snapshot", sessionID: S1, state: "idle" },
    { event: "status", sessionID: S1, state: "busy" },
    { event: "status", sessionID: S1, state: "idle" },
    { event: "route", sessionID: null, state: null },
    { event: "session.deleted", sessionID: S2 },
    { event: "server.connected" },
    { event: "dispose" },
  ]);
  assert.equal(fs.existsSync(ep.path), false);
});

test("a stale socket is replaced but a live one is left alone", async (t) => {
  const dir = tempDir();
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const sock = path.join(dir, "4242.sock");
  const code = `require("net").createServer().listen(${JSON.stringify(sock)}, () => process.kill(process.pid, "SIGKILL"))`;
  spawnSync(process.execPath, ["-e", code]);
  assert.ok(fs.existsSync(sock), "fixture left a stale socket");
  const first = fakeApi();
  const ep = await start(first.api, { dir, pid: 4242 });
  assert.ok(ep, "stale socket was not replaced");
  const second = fakeApi();
  assert.equal(await start(second.api, { dir, pid: 4242 }), undefined);
  assert.match(fs.readFileSync(path.join(dir, "4242.error"), "utf8"), /another live endpoint/);
  assert.equal((await request(ep.path, { op: "hello" })).sessionID, S1);
  first.dispose();
});

test("dispose removes only the socket this instance bound", async (t) => {
  const dir = tempDir();
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const first = fakeApi();
  const a = await start(first.api, { dir, pid: 4242 });
  fs.rmSync(a.path);
  const second = fakeApi();
  const b = await start(second.api, { dir, pid: 4242 });
  first.dispose();
  assert.ok(fs.existsSync(b.path), "disposing the first instance removed the second one's socket");
  assert.equal((await request(b.path, { op: "hello" })).ok, true);
  second.dispose();
});

test("a connection that closes without a request releases its timer", async (t) => {
  const { ep } = await setup(t);
  const timers = () => process.getActiveResourcesInfo().filter((r) => r === "Timeout").length;
  const before = timers();
  await new Promise((resolve) => {
    const c = net.createConnection(ep.path, () => c.end());
    c.on("close", resolve);
  });
  await new Promise((r) => setImmediate(r));
  await new Promise((r) => setTimeout(r, 20));
  assert.equal(timers(), before, "the request timer outlived its connection");
});

test("an over-long socket path is reported, not bound", async (t) => {
  const dir = path.join(tempDir(), "x".repeat(120));
  t.after(() => fs.rmSync(path.dirname(dir), { recursive: true, force: true }));
  assert.equal(await start(fakeApi().api, { dir, pid: 4242 }), undefined);
  assert.match(fs.readFileSync(path.join(dir, "4242.error"), "utf8"), /exceeds 103 bytes/);
});

test("shell env carries only the hook's own session", () => {
  assert.deepEqual(sessionEnv({ sessionID: S1, cwd: "/w", callID: "c" }),
    { CLAUDE_CODE_SESSION_ID: "", CODEX_THREAD_ID: "", GROK_SESSION_ID: "", CBUS_SESSION_ID: S1 });
  assert.equal(sessionEnv({ cwd: "/w" }).CBUS_SESSION_ID, "");
  assert.equal(sessionEnv({ sessionID: "ses_bad" }).CBUS_SESSION_ID, "");
  assert.equal(sessionEnv({ sessionID: S1 }).CLAUDE_CODE_SESSION_ID, "");
});

test("classify recognizes the failure texts OpenCode 1.19.3 produced", () => {
  assert.equal(classify(new Error(`Session not found: ${S1}`)).code, "session-not-found");
  assert.equal(classify(new Error("Unable to connect. Is the computer able to access the url?")).code, "server-unreachable");
  assert.equal(classify({ data: { message: `Message not found: ${M1}` } }).code, "message-not-found");
  assert.equal(classify(new Error("boom")).code, "failed");
});

test("a reconnected server's status replaces the cached busy state", async (t) => {
  const { ep, fire, fail, calls } = await setup(t);
  fire("session.status", { sessionID: S1, status: { type: "busy" } });
  assert.equal((await request(ep.path, { op: "status" })).state, "busy");
  let release;
  fail.statusGate = new Promise((r) => { release = r; });
  fail.statusData = {}; // the restarted server lost its in-memory status
  fire("server.connected", {});
  assert.equal((await request(ep.path, { op: "status" })).state, "unknown", "delivery must hold while the refresh runs");
  release();
  for (let i = 0; i < 50 && (await request(ep.path, { op: "status" })).state !== "idle"; i++) await new Promise((r) => setTimeout(r, 10));
  assert.equal((await request(ep.path, { op: "status" })).state, "idle");
  assert.equal(calls.status, 2, "the status map was not re-read on reconnect");
});

test("a status event that races the refresh wins over the stale read", async (t) => {
  const { ep, fire, fail } = await setup(t);
  let release;
  fail.statusGate = new Promise((r) => { release = r; });
  fail.statusData = {};
  fire("server.connected", {});
  fire("session.status", { sessionID: S1, status: { type: "busy" } });
  release();
  for (let i = 0; i < 50 && (await request(ep.path, { op: "status" })).state === "unknown"; i++) await new Promise((r) => setTimeout(r, 10));
  assert.equal((await request(ep.path, { op: "status" })).state, "busy");
});

test("session reports whether the session itself exists", async (t) => {
  const { ep, fail } = await setup(t);
  assert.deepEqual(await request(ep.path, { op: "session", sessionID: S1 }), { ok: true, exists: true });
  fail.sessionGone = new Set([S1]);
  assert.deepEqual(await request(ep.path, { op: "session", sessionID: S1 }), { ok: true, exists: false });
  assert.equal((await request(ep.path, { op: "session", sessionID: "ses_x" })).code, "bad-request");
});

test("a private bind name too long for the OS is reported, not thrown", async (t) => {
  const root = tempDir();
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  // public <pid>.sock fits the limit, the private .<pid>.<tag>.sock does not
  const dir = path.join(root, "d".repeat(103 - root.length - "/4242.sock".length - 1));
  assert.ok(Buffer.byteLength(path.join(dir, "4242.sock")) <= 103);
  assert.equal(await start(fakeApi().api, { dir, pid: 4242 }), undefined);
  assert.match(fs.readFileSync(path.join(dir, "4242.error"), "utf8"), /exceeds 103 bytes/);
});

const settle = () => new Promise((r) => setTimeout(r, 20));

test("a failed refresh keeps delivery held and is retried", async (t) => {
  const { ep, fire, fail, calls } = await setup(t);
  fire("session.status", { sessionID: S1, status: { type: "busy" } });
  fail.statusError = new Error("Unable to connect. Is the computer able to access the url?");
  fire("server.connected", {});
  await settle();
  assert.equal((await request(ep.path, { op: "status" })).state, "unknown", "a failed read exposed the cached state");
  const before = calls.status;
  fail.statusError = null;
  fail.statusData = {};
  for (let i = 0; i < 100 && (await request(ep.path, { op: "status" })).state !== "idle"; i++) await settle();
  assert.equal((await request(ep.path, { op: "status" })).state, "idle");
  assert.ok(calls.status > before, "the failed refresh was not retried");
});

test("only the newest refresh applies", async (t) => {
  const { ep, fire, fail } = await setup(t);
  fail.statusReads = [];
  fire("server.connected", {}); // A
  fire("server.connected", {}); // B
  await settle();
  const [a, b] = fail.statusReads;
  b.resolve({ [S1]: { type: "busy" } });
  await settle();
  fire("server.connected", {}); // C
  await settle();
  a.resolve({}); // A is older than C and must not apply
  await settle();
  assert.equal((await request(ep.path, { op: "status" })).state, "unknown", "an old read ended the newest refresh");
  fail.statusReads[2].resolve({ [S1]: { type: "busy" } });
  await settle();
  assert.equal((await request(ep.path, { op: "status" })).state, "busy");
});
