// Unit tests run anywhere; the e2e suite needs a px0 binary:
//   PX0_BIN=../px0 node --test test.ts
import { describe, test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn, execFileSync, type ChildProcess } from "node:child_process";
import { mkdtempSync, writeFileSync, chmodSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Px0, Px0Error, type Px0Event } from "./index.ts";

// ---------------------------------------------------------------- fakes

type Call = { url: string; init: RequestInit };

function fakeFetch(handler: (c: Call, n: number) => Response | Promise<Response>) {
  const calls: Call[] = [];
  const f = async (url: string | URL | Request, init: RequestInit = {}) => {
    const c = { url: String(url), init };
    calls.push(c);
    return handler(c, calls.length);
  };
  return Object.assign(f as typeof fetch, { calls });
}

const enc = new TextEncoder();

/** SSE body delivering chunks as given; stays open unless end is set. */
function sseBody(chunks: string[], o: { end?: boolean; onCancel?: () => void } = {}) {
  let i = 0;
  const body = new ReadableStream<Uint8Array>({
    pull(c) {
      if (i < chunks.length) c.enqueue(enc.encode(chunks[i++]));
      else if (o.end) c.close();
      else return new Promise(() => {}); // idle connection
    },
    cancel: o.onCancel,
  });
  return new Response(body, { headers: { "Content-Type": "text/event-stream" } });
}

const hang = (c: Call) =>
  new Promise<Response>((_, reject) => c.init.signal!.addEventListener("abort", () => reject(c.init.signal!.reason)));

async function take(it: AsyncIterable<Px0Event>, n: number) {
  const out: Px0Event[] = [];
  for await (const ev of it) {
    out.push(ev);
    if (out.length === n) break;
  }
  return out;
}

// ---------------------------------------------------------------- unit

describe("unit", () => {
  test("SSE parsing survives arbitrary chunk splits and CRLF", async () => {
    const wire = 'event: metrics\ndata: {"rssBytes":1}\n\n: ping\n\nevent: git-status\r\ndata: {"gitChanges":2,"s":"é"}\r\n\r\n';
    const bytes = enc.encode(wire);
    // Split at every offset, including mid-UTF-8 sequence.
    for (let cut = 1; cut < bytes.length; cut++) {
      const f = fakeFetch(() => {
        const body = new ReadableStream<Uint8Array>({
          start(c) {
            c.enqueue(bytes.slice(0, cut));
            c.enqueue(bytes.slice(cut));
          },
        });
        return new Response(body);
      });
      const evs = await take(new Px0({ fetch: f }).events(), 2);
      assert.deepEqual(evs, [
        { type: "metrics", data: { rssBytes: 1 } },
        { type: "git-status", data: { gitChanges: 2, s: "é" } },
      ]);
    }
  });

  test("breaking out of events() closes the connection", async () => {
    let cancelled = false;
    const f = fakeFetch(() => sseBody(["event: metrics\ndata: {}\n\n"], { onCancel: () => void (cancelled = true) }));
    await take(new Px0({ fetch: f }).events(), 1);
    assert.equal(cancelled, true);
  });

  test("aborting events() ends the loop and closes the connection", async () => {
    let cancelled = false;
    const f = fakeFetch(() => sseBody(["event: metrics\ndata: {}\n\n"], { onCancel: () => void (cancelled = true) }));
    const ac = new AbortController();
    const seen: string[] = [];
    setTimeout(() => ac.abort(), 20); // abort while idle-waiting on the socket
    for await (const ev of new Px0({ fetch: f }).events({ signal: ac.signal })) seen.push(ev.type);
    assert.deepEqual(seen, ["metrics"]);
    assert.equal(cancelled, true);
  });

  test("events() reconnects after a drop, not after a failed first connect", async () => {
    const f = fakeFetch((_, n) => {
      if (n === 2) throw new TypeError("fetch failed"); // server briefly down
      return sseBody([`event: git-status\ndata: {"gitChanges":${n}}\n\n`], { end: n === 1 });
    });
    const evs = await take(new Px0({ fetch: f }).events(), 2);
    assert.deepEqual(evs.map((e) => (e.data as { gitChanges: number }).gitChanges), [1, 3]);

    const down = fakeFetch(() => {
      throw new TypeError("fetch failed");
    });
    await assert.rejects(take(new Px0({ fetch: down }).events(), 1), (e: Px0Error) => e.status === 0);
    assert.equal(down.calls.length, 1);
  });

  test("events({metrics: false}) uses the git-only stream", async () => {
    const f = fakeFetch(() => sseBody(["event: git-status\ndata: {}\n\n"]));
    await take(new Px0({ fetch: f }).events({ metrics: false }), 1);
    assert.match(f.calls[0].url, /\/api\/git\/stream$/);
  });

  test("timeouts reject with TimeoutError", async () => {
    const px0 = new Px0({ fetch: fakeFetch(hang), timeout: 20 });
    await assert.rejects(px0.meta(), { name: "TimeoutError" });
    await assert.rejects(px0.meta({ timeout: 5 }), { name: "TimeoutError" });
  });

  test("LSP calls outlast the server-side wait", async () => {
    let aborted = false;
    const f = fakeFetch(async (c) => {
      c.init.signal!.addEventListener("abort", () => void (aborted = true));
      await new Promise((r) => setTimeout(r, 40));
      return Response.json({ hits: [], state: "ready", server: "x" });
    });
    await new Px0({ fetch: f, timeout: 10 }).lsp.definition("a.go", 1, 0, { wait: 100 });
    assert.equal(aborted, false);
    assert.match(f.calls[0].url, /wait=100/);
  });

  test("abort signal cancels in-flight calls", async () => {
    const ac = new AbortController();
    const p = new Px0({ fetch: fakeFetch(hang) }).search("x", { signal: ac.signal });
    ac.abort();
    await assert.rejects(p, { name: "AbortError" });
  });

  test("unreachable server is Px0Error status 0 with cause", async () => {
    const f = fakeFetch(() => {
      throw new TypeError("fetch failed", { cause: new Error("ECONNREFUSED") });
    });
    await assert.rejects(new Px0({ fetch: f }).meta(), (e: Px0Error) => e.status === 0 && e.cause instanceof TypeError);
  });

  test("HTTP errors carry status, server message and endpoint", async () => {
    const f = fakeFetch(() => Response.json({ error: "bad path" }, { status: 400 }));
    await assert.rejects(
      new Px0({ fetch: f }).file("../x"),
      (e: Px0Error) => e.status === 400 && e.message === "bad path" && e.endpoint === "file",
    );
    const plain = fakeFetch(() => new Response("404 page not found\n", { status: 404 }));
    await assert.rejects(new Px0({ fetch: plain }).meta(), { message: "404 page not found" });
  });

  test("headers: identity on loopback, Origin on writes, base path kept", async () => {
    const f = fakeFetch(() => Response.json({ ok: true }));
    await new Px0({ fetch: f, url: "http://localhost:7777/rev-1" }).git.stage("a b.go");
    const { url, init } = f.calls[0];
    assert.equal(url, "http://localhost:7777/rev-1/api/git/stage");
    assert.deepEqual(init.headers, {
      "Accept-Encoding": "identity",
      "Content-Type": "application/json",
      Origin: "http://localhost:7777",
    });
    assert.equal(init.body, '{"path":"a b.go"}');

    await new Px0({ fetch: f, url: "https://px0.example.com/" }).meta();
    assert.deepEqual(f.calls[1].init.headers, {});
  });

  test("query encoding skips unset and false, encodes true as 1", async () => {
    const f = fakeFetch(() => Response.json({ results: [], files: 0, total: 0, truncated: false }));
    await new Px0({ fetch: f }).search("a&b c", { regex: true, word: false });
    assert.equal(f.calls[0].url, "http://127.0.0.1:7777/api/search?q=a%26b+c&re=1");
  });

  test("ready() waits through connection refused and unready index", async () => {
    const f = fakeFetch((_, n) => {
      if (n === 1) throw new TypeError("fetch failed");
      return Response.json({ ready: n >= 3 });
    });
    assert.equal((await new Px0({ fetch: f }).ready()).ready, true);
    assert.equal(f.calls.length, 3);
  });

  test("agent.wait polls until the job stops", async () => {
    const f = fakeFetch((_, n) => Response.json({ id: 7, running: n < 3, changed: [] }));
    const j = await new Px0({ fetch: f }).agent.wait(7);
    assert.equal(j.running, false);
    assert.equal(f.calls.length, 3);
  });
});

// ---------------------------------------------------------------- e2e

const bin = process.env.PX0_BIN ?? new URL("../px0", import.meta.url).pathname;

describe("e2e", { skip: !existsSync(bin) && `no px0 binary at ${bin} (set PX0_BIN)` }, () => {
  const port = 20000 + Math.floor(Math.random() * 20000);
  const root = mkdtempSync(join(tmpdir(), "px0-sdk-"));
  const px0 = new Px0({ url: `http://127.0.0.1:${port}/` });
  let proc: ChildProcess;

  before(async () => {
    writeFileSync(join(root, "a.js"), "function hello() {\n  return 1;\n}\n");
    const git = (...a: string[]) => execFileSync("git", ["-C", root, "-c", "user.email=t@t", "-c", "user.name=t", ...a]);
    git("init", "-q");
    git("add", ".");
    git("commit", "-qm", "init");
    // Stand-in harness: appends a line to a.js wherever it was asked to edit.
    const harness = join(root, "..", `fake-harness-${port}.sh`);
    writeFileSync(harness, "#!/bin/sh\necho '// edited' >> a.js\n");
    chmodSync(harness, 0o755);
    proc = spawn(bin, ["-port", String(port), "-no-open", "-no-lsp", "-no-telemetry", "-quiet", "-agent", `${harness} {prompt}`, root], {
      // Keep settings/session writes out of the real home.
      env: {
        ...process.env,
        HOME: mkdtempSync(join(tmpdir(), "px0-home-")),
        GIT_AUTHOR_NAME: "t",
        GIT_AUTHOR_EMAIL: "t@t",
        GIT_COMMITTER_NAME: "t",
        GIT_COMMITTER_EMAIL: "t@t",
      },
      stdio: "ignore",
    });
    await px0.ready({ timeout: 10_000 });
  });

  after(() => proc?.kill());

  test("reads", async () => {
    assert.equal((await px0.meta()).git, true);
    assert.deepEqual((await px0.tree()).map((n) => n.name), ["a.js"]);
    assert.equal((await px0.find("aj"))[0].path, "a.js");
    const f = await px0.file("a.js", { count: 2 });
    assert.ok(!("image" in f) && f.total >= 3 && f.lines.length === 2);
    assert.match(await px0.text("a.js"), /return 1/);
    assert.equal((await px0.search("return")).total, 1);
    assert.equal((await px0.outline("a.js"))[0].name, "hello");
    assert.equal((await px0.def("hello")).defs[0].path, "a.js");
  });

  test("errors carry status and server message", async () => {
    await assert.rejects(px0.file("../etc/passwd"), (e: Px0Error) => e.status === 400 && e.message === "bad path");
  });

  test("agent edit, wait, then git sees it", async () => {
    const job = await px0.agent.edit({ path: "a.js", l1: 1, l2: 3, instruction: "add a comment" });
    const done = await px0.agent.wait(job.id);
    assert.equal(done.error ?? "", "");
    assert.match(readFileSync(join(root, "a.js"), "utf8"), /edited/);
    assert.equal((await px0.diff("a.js")).available, true);

    await px0.git.stage("a.js");
    await px0.git.commit("sdk test");
    assert.equal((await px0.git.log({ limit: 1 })).commits[0].subject, "sdk test");
  });

  test("events stream", async () => {
    const seen = new Set<string>();
    for await (const ev of px0.events()) {
      seen.add(ev.type);
      if (seen.has("metrics") && seen.has("git-status")) break;
    }
    const [git] = await take(px0.events({ metrics: false }), 1);
    assert.equal(git.type, "git-status");
  });
});
