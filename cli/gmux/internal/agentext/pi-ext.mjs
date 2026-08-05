// gmux Pi session extension.
//
// The extension is injected by the gmux runner. It reports authoritative Pi
// session/turn state and receives semantic user messages over the runner-owned
// Unix socket. Submitted messages never pass through the PTY.

import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const http = require("http");
const { randomUUID } = require("crypto");

export default function (pi) {
  const sock = process.env.GMUX_SESSION_SOCK;
  if (!sock) return;

  let runtime = null;
  let postQueue = Promise.resolve();

  function post(path, event) {
    postQueue = postQueue.then(() => requestJSON(sock, "POST", path, event, 2000).catch(() => null));
    return postQueue;
  }

  function reportSession(reason, ctx, includeAgentTitle = false) {
    let file, id, cwd, name;
    try {
      const sm = ctx.sessionManager;
      file = sm.getSessionFile();
      id = sm.getSessionId();
      cwd = sm.getCwd();
      if (includeAgentTitle) name = sm.getSessionName();
    } catch {
      return;
    }
    if (!file && !includeAgentTitle) return;
    const event = { op: "session", path: file ? String(file) : "", id, cwd, reason };
    if (includeAgentTitle) event.agent_title = name ? String(name) : "";
    void post("/hook/event", event);
  }

  function reportExplicitTitle(ctx, eventName) {
    let name = eventName;
    if (name === undefined) {
      try { name = ctx.sessionManager.getSessionName(); } catch {}
    }
    void post("/hook/event", { op: "title", name: name ? String(name) : "" });
  }

  function stopReason(messages) {
    for (let i = messages.length - 1; i >= 0; i--) {
      if (messages[i]?.role === "assistant") return messages[i].stopReason;
    }
    return undefined;
  }

  function assistantText(messages) {
    for (let i = messages.length - 1; i >= 0; i--) {
      const message = messages[i];
      if (message?.role !== "assistant") continue;
      if (typeof message.content === "string") return message.content;
      if (Array.isArray(message.content)) {
        return message.content
          .filter((part) => part?.type === "text" && typeof part.text === "string")
          .map((part) => part.text)
          .join("\n");
      }
    }
    return "";
  }

  function normalizeOutcome(reason) {
    return reason === "stop" ? "completed" : reason === "error" ? "error" : "aborted";
  }

  async function reportMessage(rt, request, state, extra = {}) {
    if (rt.stopped || runtime !== rt || !request) return;
    const event = {
      runtime_epoch: rt.epoch,
      request_id: request.request_id,
      state,
      ...extra,
    };
    // Message ACKs are correctness signals, unlike best-effort sidebar
    // metadata. Retry them while this exact Pi runtime remains active.
    while (!rt.stopped && runtime === rt) {
      try {
        await requestJSON(sock, "POST", "/hook/message-event", event, 2000);
        return;
      } catch {
        await delay(100);
      }
    }
  }

  async function deliver(rt, delivery) {
    if (rt.stopped || runtime !== rt) return;
    while (rt.settling && !rt.stopped && runtime === rt) await delay(10);
    if (rt.stopped || runtime !== rt) return;
    if (rt.seen.has(delivery.request_id)) {
      return;
    }
    rt.seen.add(delivery.request_id);
    const request = { ...delivery, state: "dispatching" };
    rt.activeRequests.set(delivery.request_id, request);
    try {
      let idle = false;
      try { idle = rt.ctx.isIdle(); } catch { throw new Error("pi runtime context is stale"); }
      if (idle) {
        rt.lastOutcome = "aborted";
        rt.lastResult = "";
        rt.lastTruncated = false;
        pi.sendUserMessage(delivery.text);
        if (request.state !== "running") {
          request.state = "delivered";
          await reportMessage(rt, request, "delivered");
        }
        // before_agent_start is the acceptance boundary for an idle Pi.
      } else {
        pi.sendUserMessage(delivery.text, { deliverAs: "steer" });
        // The current run is already active, so queue acceptance is enough to
        // make a following gmux wait safe.
        request.state = "running";
        await reportMessage(rt, request, "running");
      }
    } catch (error) {
      await reportMessage(rt, request, "failed", { error: String(error?.message ?? error) });
      rt.activeRequests.delete(delivery.request_id);
    }
  }

  async function consume(rt) {
    while (!rt.stopped && runtime === rt) {
      let delivery;
      try {
        rt.pendingRequest = requestJSON(
          sock,
          "GET",
          `/hook/messages/next?runtime_epoch=${encodeURIComponent(rt.epoch)}`,
          undefined,
          30000,
        );
        delivery = await rt.pendingRequest;
      } catch {
        if (!rt.stopped && runtime === rt) await delay(100);
        continue;
      } finally {
        rt.pendingRequest = null;
      }
      if (delivery) await deliver(rt, delivery);
    }
  }

  pi.on("session_start", async (ev, ctx) => {
    if (runtime) runtime.stopped = true;
    const rt = {
      epoch: randomUUID(),
      ctx,
      stopped: false,
      seen: new Set(),
      activeRequests: new Map(),
      settling: false,
      lastOutcome: "aborted",
      lastResult: "",
      lastTruncated: false,
      pendingRequest: null,
    };
    runtime = rt;
    while (!rt.stopped && runtime === rt) {
      try {
        await requestJSON(sock, "POST", "/hook/event", { op: "runtime", phase: "bind", epoch: rt.epoch }, 2000);
        break;
      } catch {
        await delay(100);
      }
    }
    if (rt.stopped || runtime !== rt) return;
    reportSession(ev?.reason ?? "start", ctx, true);
    void consume(rt);
  });

  pi.on("session_shutdown", async () => {
    const rt = runtime;
    if (!rt) return;
    rt.stopped = true;
    runtime = null;
    await post("/hook/event", { op: "runtime", phase: "unbind", epoch: rt.epoch });
  });

  pi.on("session_info_changed", (ev, ctx) => reportExplicitTitle(ctx, ev?.name));

  pi.on("before_agent_start", async () => {
    const rt = runtime;
    if (!rt) return;
    for (const request of rt.activeRequests.values()) {
      if (request.state === "running") continue;
      request.state = "running";
      await reportMessage(rt, request, "running");
    }
  });

  pi.on("agent_start", () => {
    const rt = runtime;
    if (rt) {
      rt.lastOutcome = "aborted";
      rt.lastResult = "";
      rt.lastTruncated = false;
    }
    void post("/hook/event", { op: "turn", phase: "start" });
  });

  pi.on("agent_end", (ev, ctx) => {
    const messages = ev.messages ?? [];
    const rt = runtime;
    if (rt) {
      rt.lastOutcome = normalizeOutcome(stopReason(messages));
      const result = truncateUtf8(assistantText(messages), 256 * 1024);
      rt.lastResult = result.text;
      rt.lastTruncated = result.truncated;
    }
    reportSession("activity", ctx);
  });

  pi.on("agent_settled", async () => {
    const rt = runtime;
    const outcome = rt?.lastOutcome ?? "aborted";
    if (!rt) {
      await post("/hook/event", { op: "turn", phase: "end", outcome });
      return;
    }
    rt.settling = true;
    const settledRequests = Array.from(rt.activeRequests.values());
    try {
      const state = outcome === "error" ? "failed" : "settled";
      for (const request of settledRequests) {
        await reportMessage(rt, request, state, { outcome, result: rt.lastResult, truncated: rt.lastTruncated });
        rt.activeRequests.delete(request.request_id);
      }
      await post("/hook/event", { op: "turn", phase: "end", outcome });
    } finally {
      rt.settling = false;
    }
  });
}

function truncateUtf8(text, maxBytes) {
  const bytes = Buffer.from(text, "utf8");
  if (bytes.length <= maxBytes) return { text, truncated: false };
  let end = maxBytes;
  while (end > 0 && (bytes[end] & 0xc0) === 0x80) end--;
  return { text: bytes.subarray(0, end).toString("utf8"), truncated: true };
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function requestJSON(socketPath, method, path, value, timeoutMs) {
  return new Promise((resolve, reject) => {
    try {
      const body = value === undefined ? null : Buffer.from(JSON.stringify(value), "utf8");
      const req = http.request({
        socketPath,
        path,
        method,
        headers: body ? { "content-type": "application/json", "content-length": body.length } : undefined,
      });
      req.on("response", (res) => {
        const chunks = [];
        res.on("data", (chunk) => chunks.push(chunk));
        res.on("end", () => {
          const raw = Buffer.concat(chunks).toString("utf8");
          if ((res.statusCode ?? 500) >= 300) {
            reject(new Error(raw.trim() || `HTTP ${res.statusCode}`));
            return;
          }
          if (!raw.trim()) {
            resolve(null);
            return;
          }
          try { resolve(JSON.parse(raw)); } catch (error) { reject(error); }
        });
      });
      req.on("error", reject);
      req.setTimeout(timeoutMs, () => req.destroy(new Error("request timed out")));
      if (body) req.end(body); else req.end();
    } catch (error) {
      reject(error);
    }
  });
}
