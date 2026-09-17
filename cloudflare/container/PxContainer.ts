import { Container } from "@cloudflare/containers";

interface RepoStatus {
  status: "booting" | "checking-size" | "cloning" | "ready" | "error" | "timeout" | string;
  message: string;
}

/**
 * One instance per owner/repo (keyed via env.PX0_CONTAINER.getByName(`${owner}/${repo}`)).
 * Runs the real, unmodified px0 binary (see cloudflare/container/Dockerfile) against a
 * shallow git checkout fetched on demand. See cloudflare/container/entrypoint.sh for why
 * the checkout is `git init` + fetch/checkout rather than `git clone` into a fresh dir.
 */
export class PxContainer extends Container<Env> {
  defaultPort = 7777;
  sleepAfter: `${number}m` = "5m";

  private starting: Promise<void> | null = null;
  private currentKey: string | null = null;

  override async onActivityExpired() {
    this.currentKey = null;
    await this.stop();
  }

  private async ensureRunning(owner: string, repo: string, ref: string, maxRepoMb: number) {
    const key = `${owner}/${repo}@${ref}`;

    if (this.currentKey === key && !this.starting) return;
    if (this.starting) {
      await this.starting.catch(() => {});
      if (this.currentKey === key) return;
    }

    this.starting = (async () => {
      // Switching repo or ref on an already-running instance: the old
      // checkout is stale, tear it down and start fresh.
      if (this.currentKey !== null && this.currentKey !== key) {
        await this.stop();
      }
      await this.startAndWaitForPorts({
        ports: [7777, 8081],
        startOptions: {
          envVars: {
            PX0_OWNER: owner,
            PX0_REPO: repo,
            PX0_REF: ref,
            PX0_MAX_REPO_MB: String(maxRepoMb),
          },
        },
      });
      this.currentKey = key;
    })();

    try {
      await this.starting;
    } finally {
      this.starting = null;
    }
  }

  /** Polls the entrypoint's status server (:8081) until the clone finishes or fails. */
  private async pollRepoReady(timeoutMs = 20000): Promise<RepoStatus> {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      try {
        const res = await this.containerFetch(new Request("http://internal/status.json"), 8081);
        if (res.ok) {
          const s = (await res.json()) as RepoStatus;
          if (s.status === "ready" || s.status === "error") return s;
        }
      } catch {
        // container/status server not accepting connections yet
      }
      await new Promise((r) => setTimeout(r, 300));
    }
    return { status: "timeout", message: "repo did not become ready in time" };
  }

  override async fetch(request: Request): Promise<Response> {
    const owner = request.headers.get("X-Px0-Owner");
    const repo = request.headers.get("X-Px0-Repo");
    const ref = request.headers.get("X-Px0-Ref") || "HEAD";
    const maxRepoMb = Number(request.headers.get("X-Px0-Max-Repo-Mb") || "200");

    if (!owner || !repo) {
      return Response.json({ error: "missing repo context (X-Px0-Owner/X-Px0-Repo)" }, { status: 400 });
    }

    await this.ensureRunning(owner, repo, ref, maxRepoMb);
    const status = await this.pollRepoReady();

    if (status.status === "error") {
      return Response.json({ error: status.message }, { status: 502 });
    }
    if (status.status !== "ready") {
      return Response.json({ error: status.message || "timed out waiting for repo" }, { status: 504 });
    }

    return this.containerFetch(request, 7777);
  }
}
