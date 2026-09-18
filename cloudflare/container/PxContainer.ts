import { Container } from "@cloudflare/containers";

interface RepoStatus {
  status: "booting" | "checking-size" | "cloning" | "ready" | "error" | "timeout" | string;
  message: string;
}

/**
 * A single shared instance (env.PX0_CONTAINER.getByName("shared")) hosting
 * many repos at once. The container's own Go supervisor (pxd — see
 * cloudflare/container/pxd) spawns one px0 process per resident repo and
 * evicts the LRU-oldest inactive repo under disk/memory pressure, so this
 * class no longer tracks any per-repo state itself: its job is just to
 * (1) make sure the shared container is running at all, (2) poll the
 * supervisor's per-repo status endpoint until that specific repo is ready
 * — a call which itself triggers provisioning if the repo isn't resident
 * yet — then (3) forward the real request.
 */
export class PxContainer extends Container<Env> {
  defaultPort = 7777;
  sleepAfter: `${number}m` = "5m";

  private starting: Promise<void> | null = null;
  private started = false;

  override async onActivityExpired() {
    this.started = false;
    await this.stop();
  }

  private async ensureStarted() {
    if (this.started) return;
    if (this.starting) {
      await this.starting.catch(() => {});
      return;
    }
    this.starting = (async () => {
      await this.startAndWaitForPorts({
        ports: [7777, 8081],
        startOptions: {
          envVars: { PX0_MAX_REPO_MB: this.env.MAX_REPO_MB || "200" },
        },
      });
      this.started = true;
    })();
    try {
      await this.starting;
    } finally {
      this.starting = null;
    }
  }

  /**
   * Polls GET /status/<owner>/<repo>.json?ref=<ref> on the supervisor
   * (:8081) until that repo is ready or errors. The first call is what
   * causes the supervisor to spawn it if it isn't already resident.
   */
  private async pollRepoReady(owner: string, repo: string, ref: string, timeoutMs = 20000): Promise<RepoStatus> {
    const path = `/status/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}.json?ref=${encodeURIComponent(ref)}`;
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      try {
        const res = await this.containerFetch(new Request(`http://internal${path}`), 8081);
        if (res.ok) {
          const s = (await res.json()) as RepoStatus;
          if (s.status === "ready" || s.status === "error") return s;
        }
      } catch {
        // container/supervisor not accepting connections yet
      }
      await new Promise((r) => setTimeout(r, 300));
    }
    return { status: "timeout", message: "repo did not become ready in time" };
  }

  override async fetch(request: Request): Promise<Response> {
    const owner = request.headers.get("X-Px0-Owner");
    const repo = request.headers.get("X-Px0-Repo");
    const ref = request.headers.get("X-Px0-Ref") || "HEAD";

    if (!owner || !repo) {
      return Response.json({ error: "missing repo context (X-Px0-Owner/X-Px0-Repo)" }, { status: 400 });
    }

    await this.ensureStarted();
    const status = await this.pollRepoReady(owner, repo, ref);

    if (status.status === "error") {
      return Response.json({ error: status.message }, { status: 502 });
    }
    if (status.status !== "ready") {
      return Response.json({ error: status.message || "timed out waiting for repo" }, { status: 504 });
    }

    return this.containerFetch(request, 7777);
  }
}
