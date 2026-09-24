// Typed client for a running px0 server's HTTP API. Zero dependencies: needs
// only a global fetch (Node 18+, Bun, Deno, browsers).
//
// Paths are workspace-relative with forward slashes. Lines are 1-based; LSP
// columns are 0-based UTF-16 offsets (JavaScript string indices).

export type LspState = "off" | "starting" | "indexing" | "ready" | "failed";

export interface LspBrief {
  state: LspState;
  server: string;
  missing?: string; // language with no installed server
}

export interface Meta {
  root: string;
  name: string;
  files: number;
  indexMs: number;
  builtAt: string;
  ready: boolean;
  git: boolean;
  gitChanges: number;
  gitFiles: string[];
  githubToken: boolean;
  lspServers: string[];
  metrics: Metrics;
  version: string;
  basePath: string;
  agent: string;
  agentModel: string;
  agentPinned: boolean;
  pr?: PRMeta;
}

export interface Metrics {
  rssBytes: number;
  cpuUsage: number;
  goroutines: number;
  lspEnabled: boolean;
  lspMemBytes: number;
}

export interface TreeNode {
  name: string;
  path: string;
  dir: boolean;
  size: number;
  ignored?: boolean;
  status?: string; // git status code: M, A, D, U, ...
  staged?: boolean;
  dirty?: boolean;
  yourStatus?: string;
  yourDirty?: boolean;
}

export interface FuzzyResult {
  path: string;
  name: string;
  pos: number[]; // matched byte offsets in path
}

export interface FileChunk {
  path: string;
  lang: string;
  total: number;
  maxCols: number;
  start: number;
  lines: string[]; // syntax-highlighted HTML, one entry per line
  size: number;
  exact: boolean;
  refine: boolean;
  markdown: boolean;
  diffAvailable: boolean;
  lsp: LspBrief;
}

export interface ImageFile {
  path: string;
  image: true;
  size: number;
}

export interface Diff {
  path: string;
  diff: string;
  available: boolean;
  prDiff?: string; // PR mode: merge-base..PR head
  yourDiff?: string; // PR mode: PR head..working tree
}

export interface Gutter {
  path: string;
  available: boolean;
  added: number[];
  modified: number[];
  deleted: number[];
}

export interface Match {
  line: number;
  pre: string;
  mid: string;
  post: string;
  def?: boolean;
}

export interface SearchResult {
  results: { path: string; matches: Match[] }[];
  files: number;
  total: number;
  truncated: boolean;
}

export interface SearchOptions {
  regex?: boolean;
  caseSensitive?: boolean;
  word?: boolean;
  glob?: string;
}

export interface OutlineSymbol {
  name: string;
  kind: string;
  line: number;
  indent: number;
}

export interface DefResult {
  symbol: string;
  defs: (Match & { path: string })[];
  refCount: number;
  lsp: { state: LspState; server: string };
}

export interface NavHit extends Match {
  path: string;
  ext?: boolean; // outside the workspace, e.g. stdlib
}

export interface LspResult {
  state: LspState;
  server: string;
  error?: string;
}

export interface CallNode {
  name: string;
  detail?: string;
  kind: string;
  path: string;
  line: number;
  ext?: boolean;
  sitePath?: string;
  sites?: number[];
  item: string; // opaque; pass back to lsp.expandCalls
}

export interface Hover extends LspResult {
  signature?: string;
  doc?: string;
  empty: boolean;
}

export interface LspSetup {
  enabled: boolean;
  lang: string;
  state: LspState;
  server: string;
  reason?: string;
  servers: {
    name: string;
    options: { cmd: string; auto: boolean; tool: string; hasTool: boolean }[];
    job?: LspInstallJob;
  }[];
}

export interface LspInstallJob {
  server: string;
  cmd: string;
  running: boolean;
  error?: string;
  log: string;
}

export interface GitCommit {
  hash: string;
  subject: string;
  author: string;
  date: string;
}

export interface GitStatus {
  git: boolean;
  gitChanges: number;
  gitFiles: string[];
  statuses: Record<string, string>;
  dirtyDirs?: Record<string, boolean>;
  staged?: Record<string, boolean>;
  yourStatuses?: Record<string, string>;
  yourDirtyDirs?: Record<string, boolean>;
  branch?: string;
  recentCommits?: GitCommit[];
  commitsUrl?: string;
  ahead: number;
  behind: number;
}

export interface Harness {
  name: string;
  cmd: string;
  installed: boolean;
  path?: string;
  models?: string[];
  model?: string;
}

export interface Harnesses {
  harnesses: Harness[];
  selected: string;
  model: string;
  pinned: boolean;
  settings: string;
}

export interface EditRequest {
  path: string;
  l1: number;
  l2: number;
  instruction: string;
}

export interface AgentJob {
  id: number;
  harness: string;
  path: string;
  lines: string;
  running: boolean;
  error?: string;
  log: string;
  stdout?: string;
  stderr?: string;
  changed: string[];
  ms: number;
  tracked: boolean;
  batchCount?: number;
  items?: EditRequest[];
}

export interface PRMeta {
  number: number;
  title: string;
  author: string;
  base: string;
  head: string;
  state: string;
  merged: boolean;
  mergedAt: string;
  writeAccess: boolean;
  readOnly: boolean;
  draftCount: number;
  diffBaseWarning: string;
  headSHA: string;
  url: string;
}

export interface DraftComment {
  id: number;
  path: string;
  line: number;
  side: "LEFT" | "RIGHT";
  body: string;
}

export interface PRComment {
  id: number;
  kind: string;
  path?: string;
  line?: number;
  side?: string;
  inReplyTo?: number;
  author: string;
  avatarUrl?: string;
  body: string;
  createdAt: string;
  url: string;
}

export interface Session {
  tabs: { path: string }[];
  active: number;
  openDirs: string[];
  drafts?: DraftComment[];
}

export interface Settings {
  settings: Record<string, unknown>;
  defaults?: Record<string, unknown>;
  schema?: unknown;
  raw: string;
  path: string;
}


export type Px0Event =
  | { type: "metrics"; data: Metrics }
  | { type: "git-status"; data: GitStatus };

/**
 * A request px0 answered with a non-2xx status, or (status 0) one that never
 * reached it. Timeouts and aborts reject with the standard DOMException
 * ("TimeoutError" / "AbortError") instead.
 */
export class Px0Error extends Error {
  status: number;
  endpoint: string;
  constructor(status: number, message: string, endpoint: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "Px0Error";
    this.status = status;
    this.endpoint = endpoint;
  }
}

export interface CallOptions {
  signal?: AbortSignal;
  /** ms before the call rejects with a TimeoutError; 0 disables. Defaults to the client's timeout. */
  timeout?: number;
}

export interface LspOptions extends CallOptions {
  /** ms the server waits for a busy language server (server default 10000, max 120000). */
  wait?: number;
}

export interface EventsOptions {
  signal?: AbortSignal;
  /** Include the process metrics event (every 2.5s). Default true. */
  metrics?: boolean;
  /** Reconnect with backoff when an established stream drops. Default true. */
  reconnect?: boolean;
}

export interface Px0Options {
  /** Base URL including any -base-path, e.g. "http://127.0.0.1:7777/rev-1/". Default http://127.0.0.1:7777/ */
  url?: string;
  fetch?: typeof fetch;
  /** Per-call timeout in ms; 0 disables. Default 30000. */
  timeout?: number;
  /**
   * Ask for gzip responses. Default: off for loopback hosts, where compressing
   * costs more than the bytes it saves, on for anything else.
   */
  compress?: boolean;
}

type Query = Record<string, string | number | boolean | undefined>;
type Method = "GET" | "POST";

const sleep = (ms: number, signal?: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    if (signal?.aborted) return reject(signal.reason);
    const onAbort = () => {
      clearTimeout(t);
      reject(signal!.reason);
    };
    const t = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    signal?.addEventListener("abort", onAbort, { once: true });
  });

const timeoutError = (msg: string) => new DOMException(msg, "TimeoutError");

async function toError(res: Response, endpoint: string): Promise<Px0Error> {
  const text = await res.text().catch(() => "");
  let msg = text.trim() || res.statusText;
  try {
    msg = JSON.parse(text).error ?? msg;
  } catch {}
  return new Px0Error(res.status, msg, endpoint);
}

// Server-sent events parser. Scans each chunk once and carries only the
// unterminated tail line between chunks, so cost stays linear in bytes
// received however the transport splits them.
async function* parseSSE(body: ReadableStream<Uint8Array>, signal?: AbortSignal): AsyncGenerator<Px0Event> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  const stop = () => void reader.cancel().catch(() => {});
  signal?.addEventListener("abort", stop, { once: true });
  let tail = "";
  let type = "";
  let data = "";
  try {
    for (;;) {
      const r = await reader.read();
      if (r.done) return;
      const value = decoder.decode(r.value, { stream: true });
      let start = 0;
      let nl: number;
      while ((nl = value.indexOf("\n", start)) >= 0) {
        let line = tail + value.slice(start, nl);
        tail = "";
        start = nl + 1;
        if (line.endsWith("\r")) line = line.slice(0, -1);
        if (line === "") {
          if (data) yield { type: type || "message", data: JSON.parse(data) } as Px0Event;
          type = data = "";
        } else if (line.startsWith("data:")) {
          data += (data ? "\n" : "") + line.slice(line.charCodeAt(5) === 32 ? 6 : 5);
        } else if (line.startsWith("event:")) {
          type = line.slice(line.charCodeAt(6) === 32 ? 7 : 6);
        }
      }
      tail += value.slice(start);
    }
  } finally {
    signal?.removeEventListener("abort", stop);
    // Closes the connection when the consumer stops early (break/return).
    await reader.cancel().catch(() => {});
  }
}

const isLoopback = (host: string) =>
  host === "localhost" || host === "[::1]" || /^127\.\d+\.\d+\.\d+$/.test(host);

export class Px0 {
  /** Normalized base URL, always ending in "/". */
  readonly url: string;
  readonly timeout: number;
  private readonly api: string;
  private readonly f: typeof fetch;
  private readonly getHeaders: Record<string, string>;
  private readonly postHeaders: Record<string, string>;

  constructor(opts: Px0Options = {}) {
    const base = new URL(opts.url ?? "http://127.0.0.1:7777/");
    if (base.protocol !== "http:" && base.protocol !== "https:") throw new TypeError(`px0 url must be http(s): ${base.href}`);
    if (!base.pathname.endsWith("/")) base.pathname += "/";
    base.search = base.hash = "";
    this.url = base.href;
    this.api = this.url + "api/";
    this.timeout = opts.timeout ?? 30_000;
    this.f = opts.fetch ?? globalThis.fetch.bind(globalThis);
    // Browsers drop both headers and set their own; Node, Bun and Deno send them.
    this.getHeaders = (opts.compress ?? !isLoopback(base.hostname)) ? {} : { "Accept-Encoding": "identity" };
    this.postHeaders = {
      ...this.getHeaders,
      "Content-Type": "application/json",
      // Mutating endpoints refuse any Origin but px0's own (CSRF guard).
      Origin: base.origin,
    };
  }

  // ---------------------------------------------------------------- transport

  /**
   * One call against /api/<path>. The timeout and signal cover the request
   * and parse(): for a body parser that is the whole response, for a
   * passthrough only until headers arrive.
   */
  private async call<T>(
    method: Method,
    path: string,
    query: Query | undefined,
    body: unknown,
    o: CallOptions | undefined,
    parse: (res: Response) => Promise<T>,
  ): Promise<T> {
    const user = o?.signal;
    user?.throwIfAborted();
    const ms = o?.timeout ?? this.timeout;
    const ac = new AbortController();
    const onAbort = () => ac.abort(user!.reason);
    user?.addEventListener("abort", onAbort, { once: true });
    const timer = ms > 0 ? setTimeout(() => ac.abort(timeoutError(`px0 ${path}: no answer after ${ms}ms`)), ms) : undefined;
    try {
      let url = this.api + path;
      if (query) {
        const qs = new URLSearchParams();
        for (const k in query) {
          const v = query[k];
          if (v !== undefined && v !== false) qs.set(k, v === true ? "1" : String(v));
        }
        const s = qs.toString();
        if (s) url += "?" + s;
      }
      let res: Response;
      try {
        res = await this.f(url, {
          method,
          headers: method === "POST" ? this.postHeaders : this.getHeaders,
          body: method === "POST" ? JSON.stringify(body ?? {}) : undefined,
          signal: ac.signal,
        });
      } catch (e) {
        if (ac.signal.aborted) throw ac.signal.reason;
        throw new Px0Error(0, `px0 unreachable at ${this.url}`, path, { cause: e });
      }
      if (!res.ok) throw await toError(res, path);
      return await parse(res);
    } catch (e) {
      // Body reads report aborts with their own error; surface ours.
      throw ac.signal.aborted ? ac.signal.reason : e;
    } finally {
      clearTimeout(timer);
      user?.removeEventListener("abort", onAbort);
    }
  }

  private get<T>(path: string, query?: Query, o?: CallOptions): Promise<T> {
    return this.call("GET", path, query, undefined, o, (r) => r.json());
  }

  private post<T>(path: string, query?: Query, body?: unknown, o?: CallOptions): Promise<T> {
    return this.call("POST", path, query, body, o, (r) => r.json());
  }

  private lspGet<T>(path: string, query: Query, o: LspOptions = {}): Promise<T> {
    // Give the client at least as long as the server was told to wait.
    const timeout = o.timeout ?? (this.timeout && o.wait ? Math.max(this.timeout, o.wait + 5_000) : this.timeout);
    return this.get(path, { ...query, wait: o.wait }, { signal: o.signal, timeout });
  }

  /**
   * Escape hatch for anything not wrapped. Throws Px0Error on non-2xx. The
   * timeout covers only until headers arrive; o.signal is not linked to the body.
   */
  request(method: Method, path: string, o: CallOptions & { query?: Query; body?: unknown } = {}): Promise<Response> {
    return this.call(method, path, o.query, o.body, o, async (r) => r);
  }

  // ---------------------------------------------------------------- workspace

  meta = (o?: CallOptions) => this.get<Meta>("meta", undefined, o);
  metrics = (o?: CallOptions) => this.get<Metrics>("metrics", undefined, o);

  /**
   * Resolve once px0 answers and has finished indexing. Tolerates a server
   * that is still starting (connection refused), so it can follow a spawn.
   */
  ready = async (o: CallOptions = {}): Promise<Meta> => {
    const ms = o.timeout ?? this.timeout;
    const deadline = ms > 0 ? Date.now() + ms : Infinity;
    for (let delay = 25; ; delay = Math.min(delay * 2, 500)) {
      try {
        const m = await this.meta({ signal: o.signal, timeout: deadline === Infinity ? 0 : Math.max(1, deadline - Date.now()) });
        if (m.ready) return m;
      } catch (e) {
        if (!(e instanceof Px0Error && e.status === 0)) throw e;
      }
      if (Date.now() + delay > deadline) throw timeoutError(`px0 at ${this.url} not ready after ${ms}ms`);
      await sleep(delay, o.signal);
    }
  };

  /** Direct children of dir ("" = root). */
  tree = async (dir = "", o?: CallOptions) =>
    (await this.get<{ children: TreeNode[] }>("tree", { dir }, o)).children;

  /** Fuzzy file finder. limit ≤ 500, default 100. */
  find = async (q: string, o: CallOptions & { limit?: number } = {}) =>
    (await this.get<{ results: FuzzyResult[] }>("find", { q, limit: o.limit }, o)).results;

  /** Highlighted window of a file: lines [start, start+count), count default 1000. Images return {image: true}. */
  file = (path: string, o: CallOptions & { start?: number; count?: number } = {}) =>
    this.get<FileChunk | ImageFile>("file", { path, start: o.start, count: o.count }, o);

  /** Release server-side caches for a file opened with file(). */
  close = (path: string, o?: CallOptions) => this.get<{ ok: true; path: string }>("close", { path }, o);

  /** File bytes as-is, streaming. Timeout covers until headers. */
  raw = (path: string, o?: CallOptions) => this.request("GET", "raw", { ...o, query: { path } });

  /** Plain-text contents; cheaper than file() when highlighting isn't needed. */
  text = (path: string, o?: CallOptions) => this.call("GET", "raw", { path }, undefined, o, (r) => r.text());

  markdown = async (path: string, o?: CallOptions) =>
    (await this.get<{ html: string }>("markdown", { path }, o)).html;
  diff = (path: string, o?: CallOptions) => this.get<Diff>("diff", { path }, o);
  gutter = (path: string, o?: CallOptions) => this.get<Gutter>("gutter", { path }, o);

  /** Workspace search. Aborting the call also stops the search on the server. */
  search = (q: string, o: SearchOptions & CallOptions = {}) =>
    this.get<SearchResult>("search", { q, re: o.regex, case: o.caseSensitive, word: o.word, glob: o.glob }, o);

  /** Fast regex-based outline (no language server needed). */
  outline = async (path: string, o?: CallOptions) =>
    (await this.get<{ symbols: OutlineSymbol[] }>("outline", { path }, o)).symbols;

  /** Heuristic go-to-definition by whole-word search. */
  def = (sym: string, o: CallOptions & { path?: string } = {}) => this.get<DefResult>("def", { sym, path: o.path }, o);

  reindex = (o?: CallOptions) =>
    this.get<{ files: number; indexMs: number; gitChanges: number; gitFiles: string[] }>("reindex", undefined, o);

  settings = (o?: CallOptions) => this.get<Settings>("settings", undefined, o);
  /** Merge keys into settings, or pass {raw: "<json>"} to replace the file. */
  updateSettings = (patch: Record<string, unknown>, o?: CallOptions) =>
    this.post<Settings & { ok: true }>("settings", undefined, patch, o);

  session = (o?: CallOptions) => this.get<Session>("session", undefined, o);
  updateSession = (patch: Partial<Pick<Session, "tabs" | "active" | "openDirs">>, o?: CallOptions) =>
    this.post<Session>("session", undefined, patch, o);

  /**
   * Live git-status (sent on connect, then on every change) and metrics
   * events. Ends when o.signal aborts; breaking out of the loop closes the
   * connection. Dropped streams reconnect with backoff and resend git-status;
   * a failed first connect throws.
   */
  async *events(o: EventsOptions = {}): AsyncGenerator<Px0Event> {
    const path = o.metrics === false ? "git/stream" : "stream";
    const { signal } = o;
    for (let backoff = 250, first = true; ; first = false) {
      try {
        const res = await this.call("GET", path, undefined, undefined, { signal }, async (r) => r);
        backoff = 250;
        yield* parseSSE(res.body!, signal);
      } catch (e) {
        if (signal?.aborted) return;
        const permanent = e instanceof Px0Error && e.status >= 400 && e.status < 500;
        if (first || permanent || o.reconnect === false) throw e;
      }
      if (signal?.aborted || o.reconnect === false) return;
      try {
        await sleep(backoff, signal);
      } catch {
        return;
      }
      backoff = Math.min(backoff * 2, 5_000);
    }
  }

  // ---------------------------------------------------------------- lsp

  lsp = {
    definition: (path: string, line: number, col: number, o?: LspOptions) =>
      this.lspGet<LspResult & { hits: NavHit[] }>("lsp/def", { path, line, col }, o),
    references: (path: string, line: number, col: number, o?: LspOptions) =>
      this.lspGet<LspResult & { hits: NavHit[] }>("lsp/refs", { path, line, col }, o),
    hover: (path: string, line: number, col: number, o?: LspOptions) =>
      this.lspGet<Hover>("lsp/hover", { path, line, col }, o),
    symbols: (path: string, o?: LspOptions) => this.lspGet<LspResult & { symbols: OutlineSymbol[] }>("lsp/symbols", { path }, o),
    /** Resolve the function at a position into call-hierarchy roots. */
    calls: (path: string, line: number, col: number, o?: LspOptions) =>
      this.lspGet<LspResult & { nodes: CallNode[] }>("lsp/calls", { path, line, col }, o),
    /** Expand a node into callers (default) or callees. path = file the trail started in. */
    expandCalls: (path: string, item: string, o: LspOptions & { dir?: "in" | "out" } = {}) =>
      this.lspGet<LspResult & { nodes: CallNode[] }>("lsp/calls", { path, item, dir: o.dir }, o),
    /** Start the server for this file type; wait ms for it to come up. */
    warm: (path: string, o?: LspOptions) => this.lspGet<LspBrief>("lsp/warm", { path }, o),
    setup: (path: string, o?: CallOptions) => this.get<LspSetup>("lsp/setup", { path }, o),
    install: (server: string, o: CallOptions & { option?: number } = {}) =>
      this.post<LspInstallJob>("lsp/install", { server, option: o.option ?? 0 }, undefined, o),
    start: (path: string, o?: CallOptions) => this.post<LspBrief>("lsp/start", { path }, undefined, o),
  };

  // ---------------------------------------------------------------- git

  git = {
    status: (o?: CallOptions) => this.get<GitStatus>("git/refresh", undefined, o),
    log: (o: CallOptions & { limit?: number } = {}) =>
      this.get<{ commits: GitCommit[]; commitsUrl: string }>("git/log", { limit: o.limit }, o),
    stage: (path: string, o?: CallOptions) => this.post<{ ok: true }>("git/stage", undefined, { path }, o),
    unstage: (path: string, o?: CallOptions) => this.post<{ ok: true }>("git/unstage", undefined, { path }, o),
    commit: (message: string, o?: CallOptions) => this.post<{ ok: true }>("git/commit", undefined, { message }, o),
    /** Fast-forward only. Throws Px0Error 409 if diverged or dirty. Talks to the remote: default timeout 5 min. */
    pull: (o?: CallOptions) => this.post<{ ok: true; message: string }>("git/pull", undefined, undefined, { timeout: 300_000, ...o }),
    /** Talks to the remote: default timeout 5 min. */
    push: (o?: CallOptions) => this.post<{ ok: true }>("git/push", undefined, undefined, { timeout: 300_000, ...o }),
    /** Ask the selected harness for a commit message; stages everything if nothing is staged. Await with agent.wait(). */
    commitMessage: (o?: CallOptions) => this.post<AgentJob>("git/commit-message", undefined, undefined, o),
  };

  // ---------------------------------------------------------------- agent

  agent = {
    harnesses: (o?: CallOptions) => this.get<Harnesses>("agent/harnesses", undefined, o),
    select: (name: string, o: CallOptions & { model?: string } = {}) =>
      this.post<Harnesses>("agent/select", { name, model: o.model }, undefined, o),
    /**
     * Dispatch the harness at line range(s). Ranges overlapping a running job
     * are refused (409), as are dirty files unless force is set.
     */
    edit: (edits: EditRequest | EditRequest[], o: CallOptions & { force?: boolean } = {}) =>
      this.post<AgentJob>("agent/batch", undefined, { edits: Array.isArray(edits) ? edits : [edits], force: !!o.force }, o),
    /** Job snapshot, or null if unknown; omit id for the most recent. */
    job: async (id?: number, o?: CallOptions) => {
      const j = await this.get<AgentJob | { idle: true }>("agent/job", { id }, o);
      return "idle" in j ? null : j;
    },
    cancel: async (id: number, o?: CallOptions) =>
      (await this.post<{ cancelled: boolean }>("agent/cancel", { id }, undefined, o)).cancelled,
    /**
     * Resolve with the job once it stops running; check .error. Polls every
     * 50ms at first, backing off to 500ms, so quick jobs are seen almost at
     * once and long ones cost 2 requests/s. o.timeout bounds each poll, not the wait.
     */
    wait: async (id: number, o: CallOptions = {}): Promise<AgentJob> => {
      for (let delay = 50; ; delay = Math.min(delay * 1.5, 500)) {
        const j = await this.agent.job(id, o);
        if (!j) throw new Px0Error(404, `agent job ${id} not found`, "agent/job");
        if (!j.running) return j;
        await sleep(delay, o.signal);
      }
    },
  };

  // ---------------------------------------------------------------- pr (only when launched as `px0 <pr-url>`)

  pr = {
    meta: (o?: CallOptions) => this.get<PRMeta>("pr/meta", undefined, o),
    /** Comments already posted on the forge. */
    comments: (o?: CallOptions) =>
      this.get<{ issueComments: PRComment[]; reviewComments: PRComment[] }>("pr/existing-comments", undefined, o),
    drafts: async (o?: CallOptions) => (await this.get<{ comments: DraftComment[] }>("pr/comments", undefined, o)).comments,
    addDraft: (c: { path: string; line: number; body: string; side?: "LEFT" | "RIGHT" }, o?: CallOptions) =>
      this.post<DraftComment>("pr/comments", undefined, c, o),
    deleteDraft: (id: number, o?: CallOptions) => this.post<{ ok: true }>("pr/comments/delete", { id }, undefined, o),
    /** Submit all drafts as one review. */
    submit: (event: "APPROVE" | "REQUEST_CHANGES" | "COMMENT", o: CallOptions & { body?: string } = {}) =>
      this.post<{ ok: true }>("pr/submit", undefined, { event, body: o.body ?? "" }, o),
    /** Post a top-level comment immediately. */
    comment: (body: string, o?: CallOptions) => this.post<PRComment>("pr/comments/issue", undefined, { body }, o),
    /** Reply to an inline review comment immediately. */
    reply: (commentId: number, body: string, o?: CallOptions) =>
      this.post<PRComment>("pr/comments/review-reply", undefined, { commentId, body }, o),
    /** Spawn a new px0 process reviewing another PR URL. */
    launch: (target: string, o?: CallOptions) => this.post<{ ok: true }>("pr/launch", undefined, { target }, o),
  };
}
