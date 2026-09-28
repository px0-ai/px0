// Lazy, same-origin Mermaid rendering for fenced blocks in the Markdown preview.
// Repository Markdown is untrusted: only sanitized text reaches this module, and
// Mermaid's security-sensitive configuration cannot be changed by directives.

const MERMAID_ASSET = "static/vendor/mermaid-12.0.0.min.js";
const MERMAID_SECURE_KEYS = [
  "secure",
  "securityLevel",
  "startOnLoad",
  "maxTextSize",
  "suppressErrorRendering",
  "maxEdges",
  "htmlLabels",
];

let mermaidRuntimePromise = null;
let mermaidRenderID = 0;
const MERMAID_ZOOM_LEVELS = [
  0.5, 0.75, 1, 1.25, 1.5, 1.75, 2, 2.25, 2.5, 2.75, 3,
];
const MERMAID_DEFAULT_ZOOM = 2;

function loadMermaidRuntime() {
  if (globalThis.mermaid) return Promise.resolve(globalThis.mermaid);
  if (mermaidRuntimePromise) return mermaidRuntimePromise;

  mermaidRuntimePromise = new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = new URL(MERMAID_ASSET, document.baseURI || location.href).href;
    script.async = true;
    script.addEventListener(
      "load",
      () => {
        if (globalThis.mermaid) resolve(globalThis.mermaid);
        else reject(new Error("Mermaid runtime did not initialize"));
      },
      { once: true },
    );
    script.addEventListener(
      "error",
      () => reject(new Error("Mermaid runtime could not be loaded")),
      { once: true },
    );
    document.head.appendChild(script);
  }).catch((err) => {
    mermaidRuntimePromise = null;
    throw err;
  });

  return mermaidRuntimePromise;
}

function mermaidColor(styles, name, fallback) {
  return styles.getPropertyValue(name).trim() || fallback;
}

function mermaidConfig() {
  const styles = getComputedStyle(document.documentElement);
  const dark = (styles.colorScheme || "").split(/\s+/).includes("dark");
  return {
    startOnLoad: false,
    securityLevel: "strict",
    secure: MERMAID_SECURE_KEYS,
    suppressErrorRendering: true,
    htmlLabels: false,
    theme: "base",
    themeVariables: {
      darkMode: dark,
      background: mermaidColor(styles, "--bg", dark ? "#0d1117" : "#ffffff"),
      primaryColor: mermaidColor(styles, "--bg2", dark ? "#010409" : "#f7f8fa"),
      primaryTextColor: mermaidColor(
        styles,
        "--fg",
        dark ? "#e6edf3" : "#24292f",
      ),
      primaryBorderColor: mermaidColor(
        styles,
        "--line",
        dark ? "#30363d" : "#d0d7de",
      ),
      secondaryColor: mermaidColor(
        styles,
        "--bg3",
        dark ? "#161b22" : "#edf0f5",
      ),
      secondaryTextColor: mermaidColor(
        styles,
        "--fg",
        dark ? "#e6edf3" : "#24292f",
      ),
      secondaryBorderColor: mermaidColor(
        styles,
        "--line",
        dark ? "#30363d" : "#d0d7de",
      ),
      tertiaryColor: mermaidColor(styles, "--bg", dark ? "#0d1117" : "#ffffff"),
      tertiaryTextColor: mermaidColor(
        styles,
        "--dim",
        dark ? "#8b949e" : "#57606a",
      ),
      tertiaryBorderColor: mermaidColor(
        styles,
        "--line",
        dark ? "#30363d" : "#d0d7de",
      ),
      lineColor: mermaidColor(styles, "--dim", dark ? "#8b949e" : "#57606a"),
      textColor: mermaidColor(styles, "--fg", dark ? "#e6edf3" : "#24292f"),
      mainBkg: mermaidColor(styles, "--bg2", dark ? "#010409" : "#f7f8fa"),
      nodeBorder: mermaidColor(styles, "--line", dark ? "#30363d" : "#d0d7de"),
      clusterBkg: mermaidColor(styles, "--bg", dark ? "#0d1117" : "#ffffff"),
      clusterBorder: mermaidColor(
        styles,
        "--line",
        dark ? "#30363d" : "#d0d7de",
      ),
      edgeLabelBackground: mermaidColor(
        styles,
        "--bg",
        dark ? "#0d1117" : "#ffffff",
      ),
      fontFamily: mermaidColor(styles, "--ui", "sans-serif"),
    },
  };
}

function mermaidErrorMessage(err) {
  const text = String(
    err && err.message ? err.message : err || "Unknown rendering error",
  );
  return text.split("\n", 1)[0].slice(0, 180);
}

function mermaidZoomButton(label, icon) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "md-mermaid-zoom";
  button.title = label;
  button.setAttribute("aria-label", label);
  button.innerHTML = `<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true"><path d="${icon}"/></svg>`;
  return button;
}

function mermaidZoomControls(canvas) {
  const controls = document.createElement("div");
  controls.className = "md-mermaid-controls";
  controls.setAttribute("role", "group");
  controls.setAttribute("aria-label", "Diagram zoom controls");

  const zoomOut = mermaidZoomButton("Zoom out diagram", "M3 8h10");
  const zoomIn = mermaidZoomButton("Zoom in diagram", "M3 8h10M8 3v10");
  let zoomIndex = MERMAID_DEFAULT_ZOOM;

  const applyZoom = () => {
    canvas.style.setProperty("--mermaid-scale", MERMAID_ZOOM_LEVELS[zoomIndex]);
    zoomOut.disabled = zoomIndex === 0;
    zoomIn.disabled = zoomIndex === MERMAID_ZOOM_LEVELS.length - 1;
  };
  zoomOut.addEventListener("click", () => {
    if (zoomIndex > 0) zoomIndex--;
    applyZoom();
  });
  zoomIn.addEventListener("click", () => {
    if (zoomIndex < MERMAID_ZOOM_LEVELS.length - 1) zoomIndex++;
    applyZoom();
  });

  controls.append(zoomOut, zoomIn);
  applyZoom();
  return controls;
}

function mermaidOutputFor(wrap) {
  let output = wrap.querySelector(":scope > .md-mermaid");
  if (!output) {
    output = document.createElement("div");
    output.className = "md-mermaid";
    wrap.prepend(output);
  }
  return output;
}

/** Render every Mermaid fence under root. Returns false when the draw became stale. */
export async function renderMermaidBlocks(root, current = () => true) {
  const wraps = [...root.querySelectorAll(".md-pre[data-lang]")].filter(
    (wrap) => wrap.dataset.lang.toLowerCase() === "mermaid",
  );
  if (!wraps.length) return true;

  let runtime;
  try {
    runtime = await loadMermaidRuntime();
  } catch (err) {
    if (!current()) return false;
    for (const wrap of wraps) {
      const output = mermaidOutputFor(wrap);
      output.className = "md-mermaid md-mermaid-error";
      output.removeAttribute("role");
      output.textContent = mermaidErrorMessage(err);
    }
    return true;
  }
  if (!current()) return false;

  runtime.initialize(mermaidConfig());
  for (const wrap of wraps) {
    if (!current()) return false;
    const pre = wrap.querySelector(":scope > pre");
    if (!pre) continue;
    const source = (pre.querySelector("code") || pre).textContent || "";
    const output = mermaidOutputFor(wrap);
    const id = `px0-mermaid-${++mermaidRenderID}`;
    output.className = "md-mermaid";
    output.setAttribute("aria-busy", "true");
    pre.hidden = false;
    wrap.classList.remove("md-mermaid-ready");

    try {
      const result = await runtime.render(id, source);
      if (!current()) return false;
      const canvas = document.createElement("div");
      canvas.className = "md-mermaid-canvas";
      canvas.setAttribute("role", "img");
      canvas.setAttribute("aria-label", "Mermaid diagram");
      canvas.innerHTML = result.svg;
      output.replaceChildren(canvas, mermaidZoomControls(canvas));
      output.removeAttribute("aria-busy");
      pre.hidden = true;
      wrap.classList.add("md-mermaid-ready");
    } catch (err) {
      if (!current()) return false;
      output.className = "md-mermaid md-mermaid-error";
      output.removeAttribute("role");
      output.removeAttribute("aria-label");
      output.removeAttribute("aria-busy");
      output.textContent =
        "Unable to render Mermaid diagram: " + mermaidErrorMessage(err);
    } finally {
      document.getElementById("d" + id)?.remove();
    }
  }
  return true;
}
