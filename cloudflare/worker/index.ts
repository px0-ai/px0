import { PxContainer } from "../container/PxContainer";

export { PxContainer };

interface RepoRef {
  owner: string;
  repo: string;
  ref: string;
}

const THEME_FILES = [
  "catppuccin-latte.css",
  "catppuccin-mocha.css",
  "dark.css",
  "dracula.css",
  "github-dark.css",
  "gruvbox-dark.css",
  "gruvbox-light.css",
  "light.css",
  "monokai.css",
  "nord.css",
  "one-dark.css",
  "rose-pine.css",
  "solarized-dark.css",
  "solarized-light.css",
];

export default {
  async fetch(request, env, ctx): Promise<Response> {
    const url = new URL(request.url);

    // Static assets (px0's own web/ directory) never need repo context or a
    // running container — identical bytes for every repo, every time.
    if (url.pathname === "/static/themes.css") {
      return serveThemesCss(request, env);
    }
    if (url.pathname.startsWith("/static/")) {
      const assetUrl = new URL(url.pathname.slice("/static".length) || "/", url);
      return env.ASSETS.fetch(new Request(assetUrl, request));
    }

    if (url.pathname === "/") {
      return handleLanding(request, env);
    }

    // Bare API/other px0 routes (no owner/repo in the path) arrive here when
    // px0's own frontend does `fetch('/api/...')` with an absolute path from
    // a page that was loaded at /owner/repo. Recover the repo from Referer.
    if (isPx0Route(url.pathname)) {
      const ref = parseRepoFromReferer(request.headers.get("Referer"), url);
      if (!ref) {
        return Response.json(
          { error: "could not determine which repo this request belongs to (missing/unparseable Referer)" },
          { status: 400 },
        );
      }
      return routeToContainer(request, env, ctx, ref);
    }

    // Otherwise: /owner/repo(/anything) — the initial page load. Serve
    // px0's own index.html verbatim; it never needs the container itself,
    // only the /api/* calls it makes afterwards do.
    const parsed = parseOwnerRepo(url.pathname);
    if (!parsed) {
      return new Response("Not found", { status: 404 });
    }
    return serveIndexHtml(request, env);
  },
} satisfies ExportedHandler<Env>;

function isPx0Route(pathname: string): boolean {
  return pathname === "/api" || pathname.startsWith("/api/");
}

function parseOwnerRepo(pathname: string): { owner: string; repo: string } | null {
  const parts = pathname.split("/").filter(Boolean);
  if (parts.length < 2) return null;
  return { owner: parts[0], repo: parts[1] };
}

function parseRepoFromReferer(referer: string | null, currentUrl: URL): RepoRef | null {
  if (!referer) return null;
  let refUrl: URL;
  try {
    refUrl = new URL(referer);
  } catch {
    return null;
  }
  if (refUrl.origin !== currentUrl.origin) return null;
  const parsed = parseOwnerRepo(refUrl.pathname);
  if (!parsed) return null;
  const ref = refUrl.searchParams.get("ref") || "HEAD";
  return { owner: parsed.owner, repo: parsed.repo, ref };
}

async function serveIndexHtml(request: Request, env: Env): Promise<Response> {
  const assetUrl = new URL("/index.html", request.url);
  const res = await env.ASSETS.fetch(new Request(assetUrl, request));
  const out = new Response(res.body, res);
  // Same-origin fetch() calls made by this page (fetch('/api/...')) need to
  // carry the full path (including ?ref=) as Referer so the Worker can
  // recover repo context on those prefix-less requests. Pin the policy
  // explicitly rather than relying on the browser's default.
  out.headers.set("Referrer-Policy", "same-origin");
  return out;
}

async function serveThemesCss(request: Request, env: Env): Promise<Response> {
  // Mirrors px0's own handleThemes: concatenate web/themes/*.css in
  // alphanumeric order (see server.go).
  const parts = await Promise.all(
    THEME_FILES.map(async (name) => {
      const assetUrl = new URL(`/themes/${name}`, request.url);
      const res = await env.ASSETS.fetch(new Request(assetUrl, request));
      return res.ok ? res.text() : "";
    }),
  );
  return new Response(parts.join("\n"), {
    headers: { "content-type": "text/css; charset=utf-8" },
  });
}

async function routeToContainer(
  request: Request,
  env: Env,
  ctx: ExecutionContext,
  { owner, repo, ref }: RepoRef,
): Promise<Response> {
  const url = new URL(request.url);
  const cache = caches.default;
  // Synthetic key: the real request path (e.g. /api/tree) has no owner/repo
  // in it, so build one that does to keep different repos' cache entries apart.
  const cacheKeyUrl = new URL(`/__cache/${owner}/${repo}/${encodeURIComponent(ref)}${url.pathname}${url.search}`, url);
  const cacheKey = new Request(cacheKeyUrl.toString(), { method: "GET" });

  // NOTE: every request here shares the same real URL (e.g. /api/tree) no
  // matter which repo it's for — we deliberately never put owner/repo in
  // the path (that's the whole point of the Referer-based scheme). That
  // means the *browser's own* HTTP cache must never be allowed to treat
  // these as cacheable-by-URL, or it will silently serve one repo's
  // response to a different repo's page. All caching below happens only
  // in our own edge cache (keyed correctly, by the synthetic cacheKeyUrl),
  // and every response handed back to the client is explicitly no-store.
  const isCacheable = request.method === "GET";
  if (isCacheable) {
    const cached = await cache.match(cacheKey);
    if (cached) {
      const out = new Response(cached.body, cached);
      out.headers.set("Cache-Control", "no-store");
      return out;
    }
  }

  const fwd = new Request(request);
  fwd.headers.set("X-Px0-Owner", owner);
  fwd.headers.set("X-Px0-Repo", repo);
  fwd.headers.set("X-Px0-Ref", ref);
  fwd.headers.set("X-Px0-Max-Repo-Mb", env.MAX_REPO_MB || "200");
  // px0 gzips its own responses when it sees Accept-Encoding; Cloudflare's
  // edge already compresses the response to the real browser, so let px0
  // serve plain and avoid an extra (and, through the container proxy,
  // observed-corrupting) compression layer in between.
  fwd.headers.delete("Accept-Encoding");

  const stub = env.PX0_CONTAINER.getByName(`${owner}/${repo}`);
  const resp = await stub.fetch(fwd);

  if (isCacheable && resp.ok) {
    const forCache = resp.clone();
    const cacheHeaders = new Headers(forCache.headers);
    cacheHeaders.set("Cache-Control", "public, max-age=300");
    ctx.waitUntil(
      cache.put(cacheKey, new Response(forCache.body, { status: forCache.status, headers: cacheHeaders })),
    );

    const clientHeaders = new Headers(resp.headers);
    clientHeaders.set("Cache-Control", "no-store");
    return new Response(resp.body, { status: resp.status, headers: clientHeaders });
  }
  return resp;
}

async function handleLanding(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url);
  if (request.method === "GET" && url.searchParams.has("go")) {
    const input = url.searchParams.get("go") || "";
    const m =
      input.match(/github\.com\/([^/\s]+)\/([^/\s#?]+)/i) || input.match(/^([^/\s]+)\/([^/\s#?]+)$/);
    if (m) {
      const owner = m[1];
      const repo = m[2].replace(/\.git$/, "");
      return Response.redirect(new URL(`/${owner}/${repo}`, url).toString(), 302);
    }
    return new Response("Could not parse an owner/repo from that input.", { status: 400 });
  }
  return new Response(LANDING_HTML, { headers: { "content-type": "text/html; charset=utf-8" } });
}

const LANDING_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>px0-cf</title>
<style>
  body { background:#0b0d10; color:#e6e6e6; font:16px/1.5 -apple-system,BlinkMacSystemFont,sans-serif;
         display:flex; align-items:center; justify-content:center; min-height:100vh; margin:0; }
  form { display:flex; gap:.5rem; width:min(90vw,560px); }
  input { flex:1; padding:.75rem 1rem; border-radius:8px; border:1px solid #333; background:#16191d; color:inherit; font-size:1rem; }
  button { padding:.75rem 1.25rem; border-radius:8px; border:0; background:#4f7cff; color:#fff; font-size:1rem; cursor:pointer; }
  .wrap { text-align:center; }
  h1 { font-weight:600; margin-bottom:1.5rem; }
</style>
</head>
<body>
  <div class="wrap">
    <h1>Paste a GitHub repo</h1>
    <form action="/" method="get">
      <input name="go" placeholder="owner/repo or a github.com URL" autofocus>
      <button type="submit">Browse</button>
    </form>
  </div>
</body>
</html>`;
