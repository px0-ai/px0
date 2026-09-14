(() => {
  // web/src/state.js
  var $ = (s, r = document) => r.querySelector(s);
  var $$ = (s, r = document) => [...r.querySelectorAll(s)];
  var esc = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
  var request = async (method, path, params) => {
    const u = new URL(path, location.origin);
    for (const [k, v] of Object.entries(params || {}))
      if (v !== undefined && v !== "")
        u.searchParams.set(k, v);
    const r = await fetch(u, { method });
    const j = await r.json();
    if (j.error)
      throw new Error(j.error);
    return j;
  };
  var api = (path, params) => request("GET", path, params);
  var apiPost = (path, params) => request("POST", path, params);
  var debounce = (fn, ms) => {
    let t;
    return (...a) => {
      clearTimeout(t);
      t = setTimeout(() => fn(...a), ms);
    };
  };
  var isMac = /mac|iphone|ipad/i.test(navigator.userAgentData?.platform || navigator.platform || "");
  var MOD = isMac ? "metaKey" : "ctrlKey";
  var MAC_KEYS = { Mod: "⌘", Ctrl: "⌃", Alt: "⌥", Shift: "⇧", Enter: "↩", Left: "←", Right: "→", Up: "↑", Down: "↓" };
  var PC_KEYS = { Mod: "Ctrl", Left: "←", Right: "→", Up: "↑", Down: "↓" };
  var keyParts = (combo) => {
    const c = combo.includes("|") ? combo.split("|")[isMac ? 1 : 0] : combo;
    return c ? c.split("+").map((k) => (isMac ? MAC_KEYS : PC_KEYS)[k] || k) : [];
  };
  var keyLabel = (combo) => {
    const parts = keyParts(combo);
    if (!isMac)
      return parts.join("+");
    const key = parts.pop() || "";
    return parts.join("") + (parts.length && /^[a-z]{2,}$/i.test(key) ? " " : "") + key;
  };
  var keyCaps = (combo) => keyParts(combo).map((k) => "<kbd>" + esc(k) + "</kbd>").join("");
  var withKeys = (text) => text.replace(/\{([^}]+)\}/g, (_, combo) => keyLabel(combo));
  function applyKeyLabels(root = document) {
    for (const el of $$("[data-keys]", root))
      el.textContent = keyLabel(el.dataset.keys);
    for (const el of $$("[data-caps]", root))
      el.innerHTML = keyCaps(el.dataset.caps);
    for (const el of $$('[title*="{"]', root))
      el.title = withKeys(el.title);
  }
  var LH = 20;
  var CHUNK = 1000;
  var OVERSCAN = 24;
  var S2 = {
    meta: null,
    tabs: [],
    active: -1,
    hist: [],
    histIdx: -1,
    find: null,
    occ: null,
    selAll: null,
    lastWord: "",
    at: null,
    link: null,
    hover: null,
    hoverAnchor: null,
    lsp: { servers: [], state: "off", server: "" },
    gen: 0,
    chW: 7.8,
    wrap: true,
    lineNumbers: true,
    mdPreview: true
  };
  var doc_ = () => S2.active >= 0 ? S2.tabs[S2.active] : null;

  // web/src/ui.js
  var vp = $("#viewport");
  var sizer = $("#sizer");
  var rowsEl = $("#rows");
  var editor = $("#editor");
  var toastEl = $("#toast");
  var toastTimer = 0;
  function showToast(accentText, text) {
    if (!toastEl)
      return;
    toastEl.innerHTML = (accentText ? '<span class="toast-accent">' + esc(accentText) + "</span> " : "") + esc(text);
    toastEl.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
      toastEl.hidden = true;
    }, 2200);
  }
  async function copyToClipboard(text, notify = "Copied to clipboard") {
    try {
      await navigator.clipboard.writeText(text);
      showToast("✓", notify);
    } catch {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try {
        document.execCommand("copy");
        showToast("✓", notify);
      } catch (err) {
        showToast("!", "Failed to copy to clipboard");
      }
      document.body.removeChild(ta);
    }
  }

  // web/src/renderer.js
  function measure() {
    const m = $("#measure");
    m.textContent = "x".repeat(100);
    S2.chW = m.getBoundingClientRect().width / 100 || 7.8;
  }
  function layout() {
    const d = doc_();
    if (!d)
      return;
    const digits = String(d.total).length;
    editor.style.setProperty("--gw", digits);
    const gutter = S2.lineNumbers ? digits * S2.chW + 30 : 16;
    const w = S2.wrap ? vp.clientWidth : Math.max(vp.clientWidth, gutter + (d.maxCols + 4) * S2.chW);
    sizer.style.height = d.total * LH + Math.max(120, vp.clientHeight * 0.5) + "px";
    sizer.style.width = w + "px";
    rowsEl.style.width = w + "px";
  }
  function toggleWordWrap(forced) {
    S2.wrap = typeof forced === "boolean" ? forced : !S2.wrap;
    document.body.classList.toggle("word-wrap", S2.wrap);
    try {
      localStorage.setItem("px0.wrap", S2.wrap ? "true" : "false");
    } catch {}
    updateEditorOptionControls();
    layout();
    render();
  }
  function toggleLineNumbers(forced) {
    S2.lineNumbers = typeof forced === "boolean" ? forced : !S2.lineNumbers;
    document.body.classList.toggle("hide-lines", !S2.lineNumbers);
    try {
      localStorage.setItem("px0.lineNumbers", S2.lineNumbers ? "true" : "false");
    } catch {}
    updateEditorOptionControls();
    layout();
    render();
  }
  function updateEditorOptionControls() {
    const wrapBtn = $('[data-action="wrap"]');
    if (wrapBtn)
      wrapBtn.classList.toggle("active", !!S2.wrap);
    const linesBtn = $('[data-action="line-numbers"]');
    if (linesBtn)
      linesBtn.classList.toggle("active", !!S2.lineNumbers);
  }
  var raf = 0;
  function render() {
    if (raf)
      return;
    raf = requestAnimationFrame(() => {
      raf = 0;
      paint();
    });
  }
  function paint() {
    const d = doc_();
    if (!d) {
      const c = $("#caret");
      if (c)
        c.hidden = true;
      return;
    }
    const top = vp.scrollTop;
    const first = Math.max(0, Math.floor(top / LH) - OVERSCAN);
    const count = Math.ceil(vp.clientHeight / LH) + OVERSCAN * 2;
    const last = Math.min(d.total, first + count);
    ensureChunks(d, first, last);
    let html = "";
    const gut = d.gutter || null;
    for (let i = first;i < last; i++) {
      const n = i + 1;
      const body = d.lines[i];
      let rc = "row", gc = "g";
      if (n === d.cur)
        rc += " cur";
      if (gut) {
        const m = gut.marks.get(n);
        if (m)
          gc += m === "add" ? " gut-add" : " gut-mod";
        if (gut.dels.has(n))
          rc += " gut-del";
      }
      html += '<div class="' + rc + '" data-l="' + n + '">' + '<div class="' + gc + '">' + n + '</div><div class="c">' + (body === undefined ? "" : body) + "</div></div>";
    }
    const sel = saveSelection();
    rowsEl.style.transform = "translateY(" + first * LH + "px)";
    rowsEl.innerHTML = html;
    rowsEl.classList.toggle("all", S2.selAll === d);
    decorate(first, last);
    if (sel)
      restoreSelection(sel);
    placeCaret();
  }
  var caretKey = "";
  function placeCaret() {
    const el = $("#caret");
    if (!el)
      return null;
    const d = doc_();
    const row = d && rowFor(d.cur);
    if (!row) {
      el.hidden = true;
      return null;
    }
    const code = $(".c", row);
    const col = Math.max(0, Math.min(d.col || 0, code.textContent.length));
    const [node, off] = toPoint({ line: d.cur, col });
    const base = sizer.getBoundingClientRect();
    let x, y;
    if (node.nodeType === 3) {
      const r = document.createRange();
      r.setStart(node, off);
      r.collapse(true);
      const rect = r.getClientRects()[0] || r.getBoundingClientRect();
      x = rect.left;
      y = S2.wrap ? rect.top - (LH - rect.height) / 2 : row.getBoundingClientRect().top;
    } else {
      const cr = code.getBoundingClientRect();
      x = cr.left + parseFloat(getComputedStyle(code).paddingLeft || "0");
      y = cr.top;
    }
    const g = $(".g", row);
    if (g && S2.lineNumbers && x < g.getBoundingClientRect().right - 1) {
      el.hidden = true;
      return null;
    }
    el.style.transform = "translate(" + (x - base.left) + "px," + (y - base.top) + "px)";
    el.hidden = false;
    const key = d.path + ":" + d.cur + ":" + col;
    if (key !== caretKey) {
      caretKey = key;
      el.classList.remove("blink");
      el.offsetWidth;
      el.classList.add("blink");
    }
    return x - base.left;
  }
  function saveSelection() {
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed || !sel.rangeCount)
      return null;
    if (!rowsEl.contains(sel.getRangeAt(0).commonAncestorContainer))
      return null;
    const a = toPos(sel.anchorNode, sel.anchorOffset);
    const f = toPos(sel.focusNode, sel.focusOffset);
    return a && f ? { a, f } : null;
  }
  function restoreSelection({ a, f }) {
    const pa = toPoint(a), pf = toPoint(f);
    if (pa && pf)
      window.getSelection().setBaseAndExtent(pa[0], pa[1], pf[0], pf[1]);
  }
  function toPos(node, off) {
    if (node === rowsEl) {
      const row = rowsEl.children[off] || rowsEl.lastElementChild;
      if (!row)
        return null;
      const atEnd = !rowsEl.children[off];
      return { line: +row.dataset.l, col: atEnd ? $(".c", row).textContent.length : 0 };
    }
    const el = node.nodeType === 1 ? node : node.parentElement;
    const row = el && el.closest(".row");
    if (!row || !rowsEl.contains(row))
      return null;
    const code = $(".c", row);
    const r = document.createRange();
    r.selectNodeContents(code);
    const cmp = r.comparePoint(node, off);
    if (cmp < 0)
      return { line: +row.dataset.l, col: 0 };
    if (cmp > 0)
      return { line: +row.dataset.l, col: code.textContent.length };
    r.setEnd(node, off);
    return { line: +row.dataset.l, col: r.toString().length };
  }
  function toPoint({ line, col }) {
    const row = rowFor(line);
    if (!row)
      return null;
    const code = $(".c", row);
    const walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT);
    let at = 0;
    for (let n = walker.nextNode();n; n = walker.nextNode()) {
      const len = n.nodeValue.length;
      if (col <= at + len)
        return [n, col - at];
      at += len;
    }
    return [code, code.childNodes.length];
  }
  function decorate(first, last) {
    const d = doc_();
    if (S2.occ) {
      for (const row of rowsEl.children)
        markNodes($(".c", row), S2.occ, true, "occ");
    }
    if (S2.link) {
      const row = rowFor(S2.link.line);
      if (row)
        wrapRange($(".c", row), S2.link.col, S2.link.col + S2.link.word.length, "link");
    }
    if (S2.find && S2.find.hits.length) {
      const byLine = S2.find.byLine;
      const act = S2.find.hits[S2.find.active];
      for (const row of rowsEl.children) {
        const n = +row.dataset.l;
        if (!byLine.has(n))
          continue;
        const marks = markNodes($(".c", row), S2.find.q, S2.find.ci, "mark");
        if (act && act.line === n && marks[act.n])
          marks[act.n].classList.add("on");
      }
    }
  }
  function markNodes(el, needle, caseSensitive, cls) {
    if (!el || !needle)
      return [];
    const out = [];
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    const texts = [];
    for (let n = walker.nextNode();n; n = walker.nextNode())
      texts.push(n);
    for (const node of texts) {
      const raw = node.nodeValue;
      const hay = caseSensitive ? raw : raw.toLowerCase();
      const nd = caseSensitive ? needle : needle.toLowerCase();
      let i = hay.indexOf(nd), at = 0;
      if (i < 0)
        continue;
      const frag = document.createDocumentFragment();
      while (i >= 0) {
        if (i > at)
          frag.appendChild(document.createTextNode(raw.slice(at, i)));
        const mk = document.createElement(cls === "mark" ? "mark" : "span");
        if (cls !== "mark")
          mk.className = cls;
        mk.textContent = raw.slice(i, i + nd.length);
        frag.appendChild(mk);
        out.push(mk);
        at = i + nd.length;
        i = hay.indexOf(nd, at);
      }
      if (at < raw.length)
        frag.appendChild(document.createTextNode(raw.slice(at)));
      node.parentNode.replaceChild(frag, node);
    }
    return out;
  }
  function wrapRange(el, from, to, cls) {
    if (!el || to <= from)
      return null;
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    const nodes = [];
    for (let n = walker.nextNode();n; n = walker.nextNode())
      nodes.push(n);
    let at = 0, out = null;
    for (const node of nodes) {
      const len = node.nodeValue.length;
      const s = Math.max(from, at), e = Math.min(to, at + len);
      if (s < e) {
        const a = s - at, b = e - at;
        const span = document.createElement("span");
        span.className = cls;
        span.textContent = node.nodeValue.slice(a, b);
        const frag = document.createDocumentFragment();
        if (a > 0)
          frag.appendChild(document.createTextNode(node.nodeValue.slice(0, a)));
        frag.appendChild(span);
        if (b < len)
          frag.appendChild(document.createTextNode(node.nodeValue.slice(b)));
        node.parentNode.replaceChild(frag, node);
        out = out || span;
      }
      at += len;
      if (at >= to)
        break;
    }
    return out;
  }
  function rowFor(line) {
    for (const r of rowsEl.children)
      if (+r.dataset.l === line)
        return r;
    return null;
  }
  function ensureChunks(d, first, last) {
    const c0 = Math.floor(first / CHUNK), c1 = Math.floor(Math.max(first, last - 1) / CHUNK);
    for (let c = c0;c <= c1; c++) {
      if (d.chunks.has(c) || d.pending.has(c))
        continue;
      d.pending.add(c);
      const gen = d.gen;
      api("/api/file", { path: d.path, start: c * CHUNK, count: CHUNK }).then((j) => {
        if (gen !== d.gen)
          return;
        for (let i = 0;i < j.lines.length; i++)
          d.lines[j.start + i] = j.lines[i];
        d.chunks.add(c);
        d.pending.delete(c);
        if (doc_() === d)
          render();
        if (j.refine)
          refineChunk(d, c);
      }).catch(() => d.pending.delete(c));
    }
  }
  function refineChunk(d, c, delay = 800, tries = 0) {
    if (tries === 0) {
      if (d.refining.has(c))
        return;
      d.refining.add(c);
    }
    setTimeout(async () => {
      if (!S2.tabs.includes(d) || tries > 6) {
        d.refining.delete(c);
        return;
      }
      let j;
      try {
        j = await api("/api/file", { path: d.path, start: c * CHUNK, count: CHUNK });
      } catch {
        d.refining.delete(c);
        return;
      }
      if (!S2.tabs.includes(d)) {
        d.refining.delete(c);
        return;
      }
      if (!j.exact) {
        refineChunk(d, c, Math.min(delay * 1.6, 5000), tries + 1);
        return;
      }
      d.refining.delete(c);
      let changed = false;
      for (let i = 0;i < j.lines.length; i++) {
        if (d.lines[j.start + i] !== j.lines[i]) {
          d.lines[j.start + i] = j.lines[i];
          changed = true;
        }
      }
      if (changed && doc_() === d)
        render();
    }, delay);
  }
  function initRenderer() {
    vp.addEventListener("scroll", render, { passive: true });
    new ResizeObserver(() => {
      layout();
      render();
    }).observe(editor);
  }

  // web/src/history.js
  function pushHistory(path, line) {
    const top = S2.hist[S2.histIdx];
    if (top && top.path === path && Math.abs(top.line - line) < 2)
      return;
    S2.hist = S2.hist.slice(0, S2.histIdx + 1);
    S2.hist.push({ path, line });
    if (S2.hist.length > 120)
      S2.hist.shift();
    S2.histIdx = S2.hist.length - 1;
  }
  function go(delta) {
    const i = S2.histIdx + delta;
    if (i < 0 || i >= S2.hist.length)
      return;
    S2.histIdx = i;
    const h = S2.hist[i];
    openFile(h.path, { line: h.line, push: false });
  }

  // web/src/outline.js
  async function loadOutline() {
    const d = doc_();
    const el = $("#outline");
    if (!d) {
      if (el)
        el.innerHTML = '<div class="hint">No file open.</div>';
      return;
    }
    if (!d.outline) {
      try {
        d.outline = (await api("/api/outline", { path: d.path })).symbols || [];
      } catch {
        d.outline = [];
      }
    }
    drawOutline();
    upgradeOutline(d);
  }
  async function upgradeOutline(d) {
    if (d.outlineLSP || S2.lsp.state === "off" || S2.lsp.state === "failed")
      return;
    d.outlineLSP = true;
    let j;
    try {
      j = await api("/api/lsp/symbols", { path: d.path, wait: 20000 });
    } catch {
      d.outlineLSP = false;
      return;
    }
    setLspState(j);
    if (!j.symbols || !j.symbols.length) {
      d.outlineLSP = false;
      return;
    }
    d.outline = j.symbols;
    d.outlineSource = j.server;
    if (doc_() === d && $("#panel-outline")?.classList.contains("active"))
      drawOutline();
  }
  function drawOutline() {
    const d = doc_();
    const el = $("#outline");
    const rel = $("#right-symbols-list");
    if (!d || !d.outline) {
      if (el)
        el.innerHTML = '<div class="hint">No symbols found.</div>';
      if (rel)
        rel.innerHTML = '<div class="hint">No symbols found.</div>';
      return;
    }
    const f = ($("#outline-filter")?.value || "").toLowerCase();
    const rf = ($("#right-symbols-filter")?.value || "").toLowerCase();
    const syms = f ? d.outline.filter((s) => s.name.toLowerCase().includes(f)) : d.outline;
    const rsyms = rf ? d.outline.filter((s) => s.name.toLowerCase().includes(rf)) : d.outline;
    const renderSymHtml = (items) => {
      if (!items.length)
        return '<div class="hint">No symbols found.</div>';
      const base = Math.min(...items.map((s) => s.indent));
      return (d.outlineSource ? '<div class="hint"><span class="src">' + esc(d.outlineSource) + "</span> · " + items.length + " symbols</div>" : "") + items.map((s) => '<div class="sym" data-n="' + s.line + '" style="padding-left:' + (10 + Math.min(s.indent - base, 16) * 5) + 'px" title="Jump to ' + esc(s.name) + " at line " + s.line + '">' + '<span class="kd" data-k="' + esc(s.kind) + '">' + esc(kindLabel(s.kind)) + "</span>" + '<span class="sn">' + esc(s.name) + '</span><span class="sl">' + s.line + "</span></div>").join("");
    };
    if (el)
      el.innerHTML = renderSymHtml(syms);
    if (rel)
      rel.innerHTML = renderSymHtml(rsyms);
  }
  var KIND_LABEL = {
    func: "fn",
    method: "fn",
    fn: "fn",
    def: "fn",
    defp: "fn",
    defmacro: "mac",
    class: "cls",
    struct: "str",
    interface: "int",
    trait: "trt",
    impl: "impl",
    type: "typ",
    typealias: "typ",
    enum: "enm",
    record: "rec",
    object: "obj",
    const: "cst",
    var: "var",
    let: "var",
    val: "var",
    module: "mod",
    mod: "mod",
    namespace: "ns",
    defmodule: "mod",
    package: "pkg",
    macro: "mac",
    extension: "ext",
    protocol: "int",
    union: "uni",
    heading: "h",
    sym: "·"
  };
  function kindLabel(k) {
    return KIND_LABEL[k] || k.slice(0, 3);
  }
  function initOutline() {
    $("#outline")?.addEventListener("click", (e) => {
      const s = e.target.closest(".sym");
      if (!s)
        return;
      $$(".sym.sel").forEach((x) => x.classList.remove("sel"));
      s.classList.add("sel");
      const d = doc_();
      if (!d)
        return;
      d.cur = +s.dataset.n;
      centerLine(d.cur);
      render();
      updateStatus();
      pushHistory(d.path, d.cur);
    });
    $("#outline-filter")?.addEventListener("input", drawOutline);
  }

  // web/src/tree.js
  var treeEl = $("#tree");
  var openDirs = new Set;
  var GIT_STATUS = {
    M: ["git-M", "modified"],
    A: ["git-A", "added"],
    D: ["git-D", "deleted"],
    U: ["git-untracked", "untracked"],
    R: ["git-R", "renamed"],
    C: ["git-A", "copied"],
    "!": ["git-M", "unmerged"]
  };
  async function drawTree(dir, container, depth) {
    let j;
    try {
      j = await api("/api/tree", { dir });
    } catch {
      return;
    }
    container.innerHTML = j.children.map((c) => {
      const pad = 8 + depth * 12;
      const ig = c.ignored ? " ignored" : "";
      const note = c.ignored ? " (ignored by .gitignore, not searched)" : "";
      if (c.dir) {
        const dc = c.dirty ? " dirty" : "";
        return '<div class="tw"><div class="tr dir' + ig + dc + '" data-dir="' + esc(c.path) + '" style="padding-left:' + pad + 'px" title="Folder: ' + esc(c.path) + note + '">' + '<span class="ar"></span><span class="nm">' + esc(c.name) + "</span></div>" + '<div class="kids" data-kids="' + esc(c.path) + '"></div></div>';
      }
      const g = GIT_STATUS[c.status];
      const gc = g ? " dirty " + g[0] : "";
      const badge = g ? '<span class="gs" title="git: ' + g[1] + '">' + esc(c.status) + "</span>" : "";
      return '<div class="tr file' + ig + gc + '" data-file="' + esc(c.path) + '" style="padding-left:' + (pad + 12) + 'px" title="Open ' + esc(c.path) + note + '">' + '<span class="ic" data-t="' + fileKind(c.name) + '"></span><span class="nm">' + esc(c.name) + "</span>" + badge + "</div>";
    }).join("");
  }
  var FILE_KIND = {
    go: "code",
    js: "code",
    mjs: "code",
    cjs: "code",
    ts: "code",
    tsx: "code",
    jsx: "code",
    py: "code",
    rb: "code",
    rs: "code",
    java: "code",
    kt: "code",
    c: "code",
    h: "code",
    cc: "code",
    cpp: "code",
    hpp: "code",
    cs: "code",
    php: "code",
    swift: "code",
    lua: "code",
    ex: "code",
    exs: "code",
    scala: "code",
    dart: "code",
    sh: "code",
    bash: "code",
    zsh: "code",
    sql: "code",
    json: "data",
    yaml: "data",
    yml: "data",
    toml: "data",
    ini: "data",
    xml: "data",
    csv: "data",
    env: "data",
    lock: "data",
    mod: "data",
    sum: "data",
    md: "doc",
    markdown: "doc",
    txt: "doc",
    rst: "doc",
    adoc: "doc",
    html: "web",
    htm: "web",
    css: "web",
    scss: "web",
    less: "web",
    svg: "web",
    vue: "web",
    png: "img",
    jpg: "img",
    jpeg: "img",
    gif: "img",
    webp: "img",
    ico: "img",
    avif: "img"
  };
  function fileKind(name) {
    const i = name.lastIndexOf(".");
    return i > 0 && FILE_KIND[name.slice(i + 1).toLowerCase()] || "other";
  }
  async function revealDir(dir) {
    const parts = dir.split("/");
    for (let i = 0;i < parts.length; i++) {
      const p = parts.slice(0, i + 1).join("/");
      const row = treeEl.querySelector('[data-dir="' + CSS.escape(p) + '"]');
      if (!row)
        break;
      if (!row.classList.contains("open"))
        row.click();
      await new Promise((r) => setTimeout(r, 30));
    }
    const last = treeEl.querySelector('[data-dir="' + CSS.escape(dir) + '"]');
    if (last)
      last.scrollIntoView({ block: "center" });
  }
  async function revealFile(path) {
    const dir = path.slice(0, path.lastIndexOf("/"));
    if (dir)
      await revealDir(dir);
    const row = treeEl.querySelector('[data-file="' + CSS.escape(path) + '"]');
    if (row) {
      $$(".tr.sel", treeEl).forEach((x) => x.classList.remove("sel"));
      row.classList.add("sel");
      row.scrollIntoView({ block: "center" });
    }
  }
  function initTree() {
    $("#btn-changed")?.addEventListener("click", (e) => {
      const on = treeEl.classList.toggle("changed-only");
      e.currentTarget.classList.toggle("active", on);
    });
    treeEl.addEventListener("click", async (e) => {
      const dirRow = e.target.closest("[data-dir]");
      if (dirRow) {
        const path = dirRow.dataset.dir;
        const kids = treeEl.querySelector('[data-kids="' + CSS.escape(path) + '"]');
        const open = dirRow.classList.toggle("open");
        kids.classList.toggle("open", open);
        if (open) {
          openDirs.add(path);
          if (!kids.dataset.loaded) {
            kids.dataset.loaded = "1";
            await drawTree(path, kids, path.split("/").length);
          }
        } else
          openDirs.delete(path);
        return;
      }
      const f = e.target.closest("[data-file]");
      if (f) {
        $$(".tr.sel", treeEl).forEach((x) => x.classList.remove("sel"));
        f.classList.add("sel");
        openFile(f.dataset.file);
      }
    });
  }

  // web/src/panels.js
  function showPanel(name) {
    document.body.classList.remove("side-hidden");
    layout();
    render();
  }
  function initPanels() {
    $("#btn-reindex").addEventListener("click", async () => {
      $("#st-index").textContent = "reindexing…";
      const j = await api("/api/reindex");
      S2.meta.files = j.files;
      S2.meta.indexMs = j.indexMs;
      treeEl.innerHTML = "";
      openDirs.clear();
      await drawTree("", treeEl, 0);
      updateStatus();
    });
    (() => {
      const rz = $("#resizer");
      let dragging = false;
      rz.addEventListener("mousedown", (e) => {
        dragging = true;
        rz.classList.add("drag");
        e.preventDefault();
      });
      addEventListener("mousemove", (e) => {
        if (!dragging)
          return;
        $("#side").style.width = Math.max(170, Math.min(620, e.clientX)) + "px";
      });
      addEventListener("mouseup", () => {
        if (dragging) {
          dragging = false;
          rz.classList.remove("drag");
          layout();
          render();
        }
      });
    })();
  }

  // web/src/find.js
  var findbar = $("#findbar");
  var findInput = $("#find-input");
  function editorSelection() {
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed || !sel.rangeCount)
      return "";
    const at = sel.getRangeAt(0).commonAncestorContainer;
    if (!vp.contains(at) && !mdview.contains(at))
      return "";
    const line = sel.toString().split(/\r?\n/).find((l) => l.trim());
    return line ? line.trim() : "";
  }
  function openFind(seed) {
    if (!doc_())
      return;
    const sel = editorSelection();
    if (sel)
      findInput.value = sel;
    else if (findbar.hidden && seed)
      findInput.value = seed;
    findbar.hidden = false;
    findInput.focus();
    findInput.select();
    if (findInput.value)
      runFind();
  }
  function clearFind() {
    findbar.hidden = true;
    S2.find = null;
    $("#find-count").textContent = "0";
    $("#minimap-hits").innerHTML = "";
    clearPreviewMarks();
    paint();
  }
  var runFind = debounce(async () => {
    const d = doc_();
    if (!d)
      return;
    const q = findInput.value;
    if (previewing(d)) {
      const n = findInPreview(q);
      S2.find = q ? { q, ci: false, hits: new Array(n).fill(null), byLine: new Set, active: n ? 0 : -1, preview: true } : null;
      $("#find-count").textContent = !q ? "0" : n ? "1 / " + n : "no results";
      $("#minimap-hits").innerHTML = previewHitOffsets().map((p) => '<i style="top:' + p + '%"></i>').join("");
      if (n)
        jumpToHit(0);
      return;
    }
    if (!q) {
      S2.find = null;
      $("#find-count").textContent = "0";
      $("#minimap-hits").innerHTML = "";
      paint();
      return;
    }
    let j;
    try {
      j = await api("/api/search", { q, glob: d.path });
    } catch {
      return;
    }
    const f = (j.results || []).find((r) => r.path === d.path);
    const hits = [];
    if (f) {
      let prevLine = -1, n = 0;
      for (const m of f.matches) {
        n = m.line === prevLine ? n + 1 : 0;
        prevLine = m.line;
        hits.push({ line: m.line, n });
      }
    }
    S2.find = { q, ci: false, hits, byLine: new Set(hits.map((h) => h.line)), active: hits.length ? 0 : -1 };
    $("#find-count").textContent = hits.length ? "1 / " + hits.length : "no results";
    drawMinimap(hits, d.total);
    if (hits.length)
      jumpToHit(0);
    else
      paint();
  }, 140);
  function drawMinimap(hits, total) {
    const mm = $("#minimap-hits");
    if (!hits.length) {
      mm.innerHTML = "";
      return;
    }
    const seen = new Set;
    mm.innerHTML = hits.filter((h) => !seen.has(h.line) && seen.add(h.line)).map((h) => '<i style="top:' + ((h.line - 1) / total * 100).toFixed(3) + '%"></i>').join("");
  }
  function jumpToHit(i) {
    const d = doc_();
    if (!d || !S2.find || !S2.find.hits.length)
      return;
    const n = S2.find.hits.length;
    S2.find.active = (i % n + n) % n;
    if (S2.find.preview) {
      $("#find-count").textContent = S2.find.active + 1 + " / " + n;
      showPreviewHit(S2.find.active);
      return;
    }
    const h = S2.find.hits[S2.find.active];
    d.cur = h.line;
    const y = (h.line - 1) * LH;
    if (y < vp.scrollTop + LH * 2 || y > vp.scrollTop + vp.clientHeight - LH * 3)
      centerLine(h.line);
    $("#find-count").textContent = S2.find.active + 1 + " / " + n;
    render();
    updateStatus();
  }
  function initFind() {
    findInput.addEventListener("input", runFind);
    findInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        jumpToHit(S2.find ? S2.find.active + (e.shiftKey ? -1 : 1) : 0);
      }
      if (e.key === "Escape") {
        clearFind();
        vp.focus();
      }
    });
    $("#find-next").addEventListener("click", () => jumpToHit(S2.find ? S2.find.active + 1 : 0));
    $("#find-prev").addEventListener("click", () => jumpToHit(S2.find ? S2.find.active - 1 : 0));
    $("#find-close").addEventListener("click", clearFind);
    $("#minimap-hits").addEventListener("click", (e) => {
      const r = $("#minimap-hits").getBoundingClientRect();
      const d = doc_();
      if (!d)
        return;
      if (previewing(d)) {
        scrollPreviewTo((e.clientY - r.top) / r.height);
        return;
      }
      centerLine(Math.round((e.clientY - r.top) / r.height * d.total));
      render();
    });
  }

  // web/src/search.js
  var resultsEl = $("#results");
  var lastResults = null;
  var runSearch = debounce(async () => {
    const qEl = $("#q");
    if (!qEl || !resultsEl)
      return;
    const q = qEl.value;
    if (!q.trim()) {
      resultsEl.innerHTML = "";
      return;
    }
    resultsEl.innerHTML = '<div class="hint">searching…</div>';
    const params = {
      q,
      glob: $("#glob")?.value || "",
      case: $("#o-case")?.classList.contains("on") ? 1 : "",
      word: $("#o-word")?.classList.contains("on") ? 1 : "",
      re: $("#o-re")?.classList.contains("on") ? 1 : ""
    };
    try {
      const j = await api("/api/search", params);
      renderResults(j);
    } catch (e) {
      resultsEl.innerHTML = '<div class="hint">' + esc(e.message) + "</div>";
    }
  }, 160);
  function renderResults(j) {
    lastResults = j;
    if (!resultsEl)
      return;
    if (!j.results || !j.results.length) {
      resultsEl.innerHTML = '<div class="hint">No results.</div>';
      return;
    }
    const head = j.header || j.total.toLocaleString() + " result" + (j.total === 1 ? "" : "s") + " in " + j.files.toLocaleString() + " file" + (j.files === 1 ? "" : "s") + (j.truncated ? " (truncated)" : "");
    let html = '<div class="hint">' + esc(head) + "</div>";
    for (const f of j.results) {
      html += '<div class="rfile" data-toggle="' + esc(f.path) + '" title="' + esc(f.path) + '">' + '<span class="ar">&#9660;</span>' + (f.ext ? '<span class="ext">ext</span>' : "") + '<span class="fp">' + esc(displayPath(f.path)) + "</span>" + '<span class="cnt">' + f.matches.length + "</span></div>" + '<div data-group="' + esc(f.path) + '">';
      for (const m of f.matches) {
        html += '<div class="rline" data-p="' + esc(f.path) + '" data-n="' + m.line + '" title="Jump to ' + esc(f.path) + ":" + m.line + '">' + '<span class="rn">' + m.line + '</span><span class="rt">' + esc(m.pre) + "<mark>" + esc(m.mid) + "</mark>" + esc(m.post) + "</span></div>";
      }
      html += "</div>";
    }
    resultsEl.innerHTML = html;
  }
  function displayPath(p) {
    if (p.length <= 48)
      return p;
    const parts = p.split("/");
    return "…/" + parts.slice(-3).join("/");
  }
  function initSearch() {
    if (!resultsEl)
      return;
    resultsEl.addEventListener("click", (e) => {
      const t = e.target.closest("[data-toggle]");
      if (t) {
        const g = resultsEl.querySelector('[data-group="' + CSS.escape(t.dataset.toggle) + '"]');
        const hidden = g.style.display === "none";
        g.style.display = hidden ? "" : "none";
        $(".ar", t).innerHTML = hidden ? "&#9660;" : "&#9654;";
        return;
      }
      const r = e.target.closest(".rline");
      if (r) {
        $$(".rline.sel", resultsEl).forEach((x) => x.classList.remove("sel"));
        r.classList.add("sel");
        openFile(r.dataset.p, { line: +r.dataset.n });
        const q = $("#q").value;
        if (q)
          flashFind(q);
      }
    });
    $("#q").addEventListener("input", runSearch);
    $("#glob").addEventListener("input", runSearch);
    $$(".opt").forEach((b) => b.addEventListener("click", () => {
      b.classList.toggle("on");
      runSearch();
    }));
    $("#q").addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        const f = $(".rline", resultsEl);
        if (f)
          f.click();
      }
    });
  }

  // web/src/inspector.js
  function showRightInspector(tab = "refs") {
    document.body.classList.remove("right-hidden");
    setRightInspectorTab(tab);
    layout();
    render();
  }
  function hideRightInspector() {
    document.body.classList.add("right-hidden");
    layout();
    render();
  }
  function setRightInspectorTab(tab) {
    $$(".inspector-tab").forEach((b) => b.classList.toggle("active", b.dataset.itab === tab));
    $("#pane-right-refs")?.classList.toggle("active", tab === "refs");
    $("#pane-right-symbols")?.classList.toggle("active", tab === "symbols");
    $("#pane-right-calls")?.classList.toggle("active", tab === "calls");
    if (tab === "symbols") {
      loadOutline();
      $("#right-symbols-filter")?.focus();
    }
  }
  function renderRightResults(word, hits, server, isExact) {
    const targetEl = $("#right-ref-target");
    const badgeEl = $("#right-ref-badge");
    const listEl = $("#right-refs-list");
    if (!targetEl || !badgeEl || !listEl)
      return;
    targetEl.textContent = word;
    badgeEl.textContent = hits.length;
    if (!hits.length) {
      listEl.innerHTML = '<div class="hint">No references found for "<b>' + esc(word) + '</b>".</div>';
      return;
    }
    const grouped = groupHits(hits);
    const head = hits.length + " reference" + (hits.length === 1 ? "" : "s") + (server ? " · " + esc(server) : " · text search");
    let html = '<div class="hint">' + head + "</div>";
    for (const f of grouped) {
      html += '<div class="rfile" data-toggle="r-' + esc(f.path) + '" title="' + esc(f.path) + '">' + '<span class="ar">&#9660;</span>' + '<span class="fp">' + esc(displayPath(f.path)) + "</span>" + '<span class="cnt">' + f.matches.length + "</span></div>" + '<div data-group="r-' + esc(f.path) + '">';
      for (const m of f.matches) {
        html += '<div class="rline" data-p="' + esc(f.path) + '" data-n="' + m.line + '" title="Jump to ' + esc(f.path) + ":" + m.line + '">' + '<span class="rn">' + m.line + '</span><span class="rt">' + esc(m.pre) + "<mark>" + esc(m.mid || word) + "</mark>" + esc(m.post) + "</span></div>";
      }
      html += "</div>";
    }
    listEl.innerHTML = html;
  }
  async function inspectReferences(arg) {
    const d = doc_();
    const at = arg && arg.word ? arg : positionNow(typeof arg === "string" ? arg : S.lastWord);
    if (!d || !at || !at.word)
      return;
    showRightInspector("refs");
    const targetEl = $("#right-ref-target");
    const badgeEl = $("#right-ref-badge");
    const listEl = $("#right-refs-list");
    if (targetEl)
      targetEl.textContent = at.word;
    if (badgeEl)
      badgeEl.textContent = "…";
    if (listEl)
      listEl.innerHTML = '<div class="hint">Finding references for "' + esc(at.word) + '"…</div>';
    if (canAskServer(at)) {
      setStatusNote("references to " + at.word + "…");
      try {
        const j = await lspCall("refs", at, 30000);
        updateStatus();
        if (j && j.hits && j.hits.length) {
          renderRightResults(at.word, j.hits, j.server, true);
          return;
        }
      } catch {
        updateStatus();
      }
    }
    setStatusNote("searching references to " + at.word + "…");
    try {
      const j = await api("/api/search", { q: at.word, word: true, case: true });
      updateStatus();
      const hits = [];
      if (j.results) {
        for (const f of j.results) {
          for (const m of f.matches) {
            hits.push({ path: f.path, line: m.line, pre: m.pre, mid: m.mid, post: m.post });
          }
        }
      }
      renderRightResults(at.word, hits, "", false);
    } catch (err) {
      updateStatus();
      if (listEl)
        listEl.innerHTML = '<div class="hint">Search error: ' + esc(err.message) + "</div>";
    }
  }
  function initInspector() {
    $$(".inspector-tab").forEach((btn) => btn.addEventListener("click", () => {
      setRightInspectorTab(btn.dataset.itab);
    }));
    $("#btn-close-right")?.addEventListener("click", hideRightInspector);
    (() => {
      const rrz = $("#right-resizer");
      if (!rrz)
        return;
      let dragging = false;
      rrz.addEventListener("mousedown", (e) => {
        dragging = true;
        rrz.classList.add("drag");
        e.preventDefault();
      });
      addEventListener("mousemove", (e) => {
        if (!dragging)
          return;
        const w = Math.max(200, Math.min(700, window.innerWidth - e.clientX));
        $("#right-side").style.width = w + "px";
      });
      addEventListener("mouseup", () => {
        if (dragging) {
          dragging = false;
          rrz.classList.remove("drag");
          layout();
          render();
        }
      });
    })();
    $("#right-symbols-list")?.addEventListener("click", (e) => {
      const s = e.target.closest(".sym");
      if (!s)
        return;
      $$("#right-symbols-list .sym.sel, #outline .sym.sel").forEach((x) => x.classList.remove("sel"));
      s.classList.add("sel");
      const d = doc_();
      if (!d)
        return;
      d.cur = +s.dataset.n;
      centerLine(d.cur);
      render();
      updateStatus();
      pushHistory(d.path, d.cur);
    });
    $("#right-symbols-filter")?.addEventListener("input", drawOutline);
    $("#right-refs-list")?.addEventListener("click", (e) => {
      const t = e.target.closest("[data-toggle]");
      if (t) {
        const listEl = $("#right-refs-list");
        const g = listEl.querySelector('[data-group="' + CSS.escape(t.dataset.toggle) + '"]');
        if (!g)
          return;
        const hidden = g.style.display === "none";
        g.style.display = hidden ? "" : "none";
        $(".ar", t).innerHTML = hidden ? "&#9660;" : "&#9654;";
        return;
      }
      const r = e.target.closest(".rline");
      if (r) {
        $$("#right-refs-list .rline.sel").forEach((x) => x.classList.remove("sel"));
        r.classList.add("sel");
        openFile(r.dataset.p, { line: +r.dataset.n });
        const targetEl = $("#right-ref-target");
        if (targetEl && targetEl.textContent)
          flashFind(targetEl.textContent);
      }
    });
  }

  // web/src/lsp.js
  function positionNow(word) {
    const d = doc_();
    if (!d)
      return null;
    if (S2.at && S2.at.word && S2.at.path === d.path)
      return S2.at;
    if (word)
      return { word, line: d.cur, col: 0, imprecise: true };
    return null;
  }
  function canAskServer(at) {
    return !at.imprecise && (S2.lsp.state === "ready" || S2.lsp.state === "indexing");
  }
  async function warmLSP(d, tries = 0) {
    if (!d.lsp || d.lsp.state === "off" || d.lsp.state === "ready" || d.lsp.state === "failed")
      return;
    if (tries > 20)
      return;
    let j;
    try {
      j = await api("/api/lsp/warm", { path: d.path, wait: tries === 0 ? 1 : 1200 });
    } catch {
      return;
    }
    if (!S2.tabs.includes(d))
      return;
    d.lsp = { state: j.state, server: j.server, missing: j.missing || "" };
    if (doc_() === d)
      setLspState(j);
    if (j.state === "starting" || j.state === "indexing") {
      setTimeout(() => warmLSP(d, tries + 1), 900);
    }
  }
  async function lspCall(kind, at, waitMs) {
    const d = doc_();
    if (!d)
      return null;
    try {
      const j = await api("/api/lsp/" + kind, { path: d.path, line: at.line, col: at.col, wait: waitMs });
      setLspState(j);
      return j;
    } catch {
      return null;
    }
  }
  async function gotoDefinition(arg) {
    const d = doc_();
    const at = arg && arg.word ? arg : positionNow(typeof arg === "string" ? arg : S2.lastWord);
    if (!d || !at)
      return;
    if (canAskServer(at)) {
      setStatusNote("definition of " + at.word + "…");
      const j = await lspCall("def", at, S2.lsp.state === "ready" ? 5000 : 20000);
      updateStatus();
      if (j && j.hits && j.hits.length) {
        acceptHits(at.word, j.hits, j.server, "definition");
        return;
      }
    } else if (!at.imprecise && S2.lsp.state === "starting") {
      lspCall("def", at, 60000).then((j) => {
        if (j && j.hits && j.hits.length)
          showHits(at.word, j.hits, j.server, "definition");
      });
    }
    setStatusNote("searching for " + at.word + "…");
    let rx;
    try {
      rx = await api("/api/def", { sym: at.word, path: d.path });
    } catch (e) {
      setStatusNote(e.message);
      return;
    }
    updateStatus();
    if (rx.lsp)
      setLspState(rx.lsp);
    if (!rx.defs || !rx.defs.length) {
      showPanel("search");
      const q = $("#q");
      if (q) {
        q.value = at.word;
        $("#o-word")?.classList.add("on");
        runSearch();
      }
      return;
    }
    acceptHits(at.word, rx.defs, null, "definition", rx.refCount);
  }
  async function findReferences(arg) {
    const d = doc_();
    const at = arg && arg.word ? arg : positionNow(typeof arg === "string" ? arg : S2.lastWord);
    if (!d || !at)
      return;
    inspectReferences(at);
  }
  function acceptHits(word, hits, server, noun, refCount) {
    if (hits.length === 1) {
      const h = hits[0];
      openFile(h.path, { line: h.line });
      flashFind(h.mid || word);
      setStatusNote(server ? server + " · " + h.path + ":" + h.line : h.path + ":" + h.line);
      return;
    }
    showHits(word, hits, server, noun, refCount);
  }
  function showHits(word, hits, server, noun, refCount) {
    const n = hits.length;
    let head = n + " " + noun + (n === 1 ? "" : "s") + ' of "' + word + '"';
    head += server ? "  ·  " + server : "  ·  text match, no language server";
    if (refCount)
      head += "  ·  " + refCount + " other references";
    renderResults({ results: groupHits(hits), files: 0, total: n, header: head, exact: !!server });
    showPanel("search");
  }
  function groupHits(hits) {
    const byPath = new Map;
    for (const h of hits) {
      if (!byPath.has(h.path))
        byPath.set(h.path, { path: h.path, ext: h.ext, matches: [] });
      byPath.get(h.path).matches.push(h);
    }
    return [...byPath.values()];
  }
  function flashFind(q) {
    const d = doc_();
    if (!d || !q)
      return;
    S2.find = { q, ci: true, hits: [{ line: d.cur, n: 0 }], byLine: new Set([d.cur]), active: 0 };
    setTimeout(paint, 0);
  }

  // web/src/cursor.js
  var WORD = /[A-Za-z0-9_$]/;
  function wordAtPoint(x, y) {
    let node, off;
    if (document.caretPositionFromPoint) {
      const p = document.caretPositionFromPoint(x, y);
      if (!p)
        return null;
      node = p.offsetNode;
      off = p.offset;
    } else if (document.caretRangeFromPoint) {
      const r = document.caretRangeFromPoint(x, y);
      if (!r)
        return null;
      node = r.startContainer;
      off = r.startOffset;
    } else
      return null;
    if (!node || node.nodeType !== 3)
      return null;
    const code = node.parentElement && node.parentElement.closest(".c");
    const row = code && code.closest(".row");
    if (!code || !row)
      return null;
    let col = 0;
    const walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode();n; n = walker.nextNode()) {
      if (n === node) {
        col += off;
        break;
      }
      col += n.nodeValue.length;
    }
    const full = code.textContent;
    let a = Math.min(col, full.length), b = a;
    while (a > 0 && WORD.test(full[a - 1]))
      a--;
    while (b < full.length && WORD.test(full[b]))
      b++;
    if (a === b)
      return null;
    const d = doc_();
    return { word: full.slice(a, b), line: +row.dataset.l, col: a, path: d && d.path };
  }
  function colAtPoint(x, y) {
    let node, off;
    if (document.caretPositionFromPoint) {
      const p = document.caretPositionFromPoint(x, y);
      if (!p)
        return null;
      node = p.offsetNode;
      off = p.offset;
    } else if (document.caretRangeFromPoint) {
      const r = document.caretRangeFromPoint(x, y);
      if (!r)
        return null;
      node = r.startContainer;
      off = r.startOffset;
    } else
      return null;
    const el = node && (node.nodeType === 1 ? node : node.parentElement);
    const row = el && el.closest(".row");
    if (!row)
      return null;
    const code = $(".c", row);
    const line = +row.dataset.l;
    if (!code.contains(node))
      return { line, col: el.closest(".g") ? 0 : code.textContent.length };
    const r = document.createRange();
    r.setStart(code, 0);
    r.setEnd(node, off);
    return { line, col: r.toString().length };
  }
  function revealCaretX(x) {
    const d = doc_();
    if (x == null || S2.wrap || !d)
      return;
    const g = rowFor(d.cur)?.querySelector(".g");
    const gw = S2.lineNumbers && g ? g.offsetWidth : 0;
    if (x < vp.scrollLeft + gw + 8)
      vp.scrollLeft = Math.max(0, x - gw - 40);
    else if (x > vp.scrollLeft + vp.clientWidth - 24)
      vp.scrollLeft = x - vp.clientWidth + 60;
  }
  function moveCol(delta) {
    const d = doc_();
    if (!d)
      return;
    const row = rowFor(d.cur);
    const len = row ? $(".c", row).textContent.length : 0;
    const col = Math.min(d.col || 0, len) + delta;
    if (col < 0) {
      if (d.cur > 1) {
        d.col = Infinity;
        moveCursor(-1);
      }
      return;
    }
    if (col > len) {
      if (d.cur < d.total) {
        d.col = 0;
        moveCursor(1);
      }
      return;
    }
    d.col = col;
    revealCaretX(placeCaret());
  }
  function caretToEdge(end) {
    const d = doc_();
    if (!d)
      return;
    d.col = end ? Infinity : 0;
    revealCaretX(placeCaret());
  }
  function moveCursor(delta) {
    const d = doc_();
    if (!d)
      return;
    d.cur = Math.max(1, Math.min(d.total, d.cur + delta));
    const y = (d.cur - 1) * LH;
    if (y < vp.scrollTop)
      vp.scrollTop = y - LH;
    else if (y > vp.scrollTop + vp.clientHeight - LH * 2)
      vp.scrollTop = y - vp.clientHeight + LH * 3;
    render();
    updateStatus();
  }
  function initCursor() {
    vp.addEventListener("mousedown", (e) => {
      const row = e.target.closest(".row");
      if (!row)
        return;
      const d = doc_();
      if (!d)
        return;
      d.cur = +row.dataset.l;
      const p = colAtPoint(e.clientX, e.clientY);
      d.col = p && p.line === d.cur ? p.col : 0;
      placeCaret();
      updateStatus();
      const w = wordAtPoint(e.clientX, e.clientY);
      S2.at = w;
      if (w)
        S2.lastWord = w.word;
      if (e[MOD] && w) {
        e.preventDefault();
        S2.at = w;
        S2.lastWord = w.word;
        pushHistory(d.path, d.cur);
        gotoDefinition(w);
        return;
      }
      for (const r of rowsEl.children)
        r.classList.toggle("cur", +r.dataset.l === d.cur);
    });
    vp.addEventListener("dblclick", (e) => {
      const w = wordAtPoint(e.clientX, e.clientY);
      if (w) {
        S2.at = w;
        S2.lastWord = w.word;
      }
      S2.occ = w && w.word.length > 1 ? w.word : null;
      paint();
    });
  }

  // web/src/lspsetup.js
  var setupSeq = 0;
  var pollTimer = 0;
  var hintHtml = (html) => '<div class="hint">' + html + "</div>";
  function cancelLspSetup() {
    setupSeq++;
    clearTimeout(pollTimer);
  }
  async function renderLspSetup(el, onReady) {
    const d = doc_();
    if (!el || !d)
      return;
    cancelLspSetup();
    const my = setupSeq;
    let s;
    try {
      s = await api("/api/lsp/setup", { path: d.path });
    } catch (e) {
      if (my === setupSeq)
        el.innerHTML = hintHtml("Could not check language servers: " + esc(e.message));
      return;
    }
    if (my !== setupSeq || doc_() !== d)
      return;
    const again = (ms) => {
      pollTimer = setTimeout(() => {
        if (my === setupSeq)
          renderLspSetup(el, onReady);
      }, ms);
    };
    if (s.state === "starting" && !s.server) {
      el.innerHTML = hintHtml("Looking for language servers…");
      again(700);
      return;
    }
    if (s.state !== "off" && s.state !== "failed") {
      start(el, d, onReady);
      return;
    }
    el.innerHTML = drawSetup(s, d);
    wire(el, d, onReady);
    if (s.servers.some((v) => v.job && v.job.running))
      again(1000);
  }
  async function start(el, d, onReady) {
    cancelLspSetup();
    el.innerHTML = hintHtml("Starting the language server…");
    let j;
    try {
      j = await apiPost("/api/lsp/start", { path: d.path });
    } catch (e) {
      el.innerHTML = hintHtml("Could not start the language server: " + esc(e.message));
      return;
    }
    if (doc_() !== d)
      return;
    for (const t of S2.tabs) {
      if (t !== d && t.lsp && (t.lsp.state === "off" || t.lsp.state === "failed"))
        t.lsp = { state: "starting", server: "" };
    }
    d.lsp = { state: j.state, server: j.server, missing: j.missing || "" };
    setLspState(j);
    updateStatus();
    warmLSP(d);
    if (j.state === "off" || j.state === "failed") {
      renderLspSetup(el, onReady);
      return;
    }
    if (onReady)
      onReady();
  }
  function drawSetup(s, d) {
    const ext = (d.path.match(/\.[^./]+$/) || [d.name])[0];
    if (!s.enabled) {
      return hintHtml("Language servers are turned off: px0 was started with <b>-no-lsp</b>. " + "Restart it without that flag for call trails, hover and precise references.");
    }
    if (!s.servers.length) {
      return hintHtml("px0 knows no language server for <b>" + esc(ext) + "</b> files, so call trails are not available here.");
    }
    const offer = s.servers.filter((v) => v.options.length || v.job);
    const running = s.servers.some((v) => v.job && v.job.running);
    let html = '<div class="lsp-setup">';
    if (s.state === "failed") {
      html += "<p><b>" + esc(s.server) + '</b> did not start: <span class="lsp-reason">' + esc(s.reason || "unknown error") + "</span></p>" + '<div class="lsp-row"><button class="lsp-btn" data-start>Retry</button></div>';
      if (offer.length)
        html += "<p>If it is broken or incomplete, install it again:</p>";
    } else {
      html += "<p>Call trails, hover and precise references for " + esc(s.lang) + " need a language server, and none is installed.</p>";
    }
    for (const v of offer) {
      html += '<div class="lsp-server"><div class="lsp-name">' + esc(v.name) + "</div>";
      v.options.forEach((o, i) => {
        html += '<div class="lsp-opt"><code>' + esc(o.cmd) + '</code><span class="lsp-acts">';
        if (!o.auto)
          html += '<span class="lsp-need">run in a terminal</span>';
        else if (!o.hasTool)
          html += '<span class="lsp-need">needs ' + esc(o.tool) + "</span>";
        else
          html += '<button class="lsp-btn primary" data-install="' + esc(v.name) + '" data-option="' + i + '"' + (running ? " disabled" : "") + ">Install</button>";
        html += '<button class="lsp-btn" data-copy="' + esc(o.cmd) + '">Copy</button></span></div>';
      });
      if (v.job)
        html += job(v.job);
      html += "</div>";
    }
    if (!offer.length) {
      html += "<p>px0 has no installer for this one. Install " + s.servers.map((v) => "<b>" + esc(v.name) + "</b>").join(" or ") + " and make sure it is on PATH.</p>";
    }
    html += '<div class="lsp-row"><span>Installed one yourself?</span><button class="lsp-btn" data-start>Detect and start</button></div></div>';
    return html;
  }
  function job(j) {
    const tail = (j.log || "").trimEnd().split(`
`).slice(-12).join(`
`);
    const log = tail ? "<pre>" + esc(tail) + "</pre>" : "";
    if (j.running)
      return '<div class="lsp-job">Installing with <code>' + esc(j.cmd) + "</code>…" + log + "</div>";
    if (j.error)
      return '<div class="lsp-job err">Install failed: ' + esc(j.error) + log + "</div>";
    return "";
  }
  function wire(el, d, onReady) {
    el.querySelectorAll("[data-install]").forEach((b) => b.addEventListener("click", async () => {
      el.querySelectorAll("[data-install]").forEach((x) => {
        x.disabled = true;
      });
      try {
        await apiPost("/api/lsp/install", { server: b.dataset.install, option: b.dataset.option });
      } catch (e) {
        showToast("!", e.message);
      }
      renderLspSetup(el, onReady);
    }));
    el.querySelectorAll("[data-copy]").forEach((b) => b.addEventListener("click", () => {
      copyToClipboard(b.dataset.copy, "Copied " + b.dataset.copy);
    }));
    el.querySelectorAll("[data-start]").forEach((b) => b.addEventListener("click", () => start(el, d, onReady)));
  }

  // web/src/calls.js
  var T = null;
  var dirPref = "in";
  var seq = 0;
  var flat = [];
  var listEl = () => $("#right-calls-list");
  var hint = (html) => {
    const el = listEl();
    if (el)
      el.innerHTML = '<div class="hint">' + html + "</div>";
  };
  var base = (p) => p.split("/").pop();
  var explain = (msg) => /connection lost|exited|EOF/i.test(msg) ? msg + " (the language server crashed answering this; px0 restarts it on the next request)" : msg;
  function wrap(n, parent) {
    let cycle = false;
    for (let p = parent;p; p = p.parent) {
      if (p.n.path === n.path && p.n.line === n.line && p.n.name === n.name) {
        cycle = true;
        break;
      }
    }
    return { n, parent, kids: null, open: false, loading: false, err: "", cycle };
  }
  function target(node) {
    const n = node.n;
    if (T.dir === "in" && n.sites && n.sites.length)
      return { path: n.sitePath, line: n.sites[0] };
    return { path: n.path, line: n.line };
  }
  async function showCalls(arg) {
    const d = doc_();
    const at = arg && arg.word ? arg : positionNow(typeof arg === "string" ? arg : S2.lastWord);
    showRightInspector("calls");
    cancelLspSetup();
    if (!d)
      return;
    if (S2.lsp.state === "off" || S2.lsp.state === "failed") {
      T = null;
      $("#right-calls-target").textContent = at ? at.word : "-";
      renderLspSetup(listEl(), () => showCalls(arg));
      return;
    }
    if (!at || at.imprecise) {
      hint("Click a function name in the editor, then press <b>" + esc(keyLabel("Alt+Shift+H")) + "</b>.");
      return;
    }
    const my = ++seq;
    T = null;
    $("#right-calls-target").textContent = at.word;
    hint('Tracing calls for "' + esc(at.word) + '"…');
    setStatusNote("call trail for " + at.word + "…");
    let j;
    try {
      j = await api("/api/lsp/calls", { path: d.path, line: at.line, col: at.col, wait: S2.lsp.state === "ready" ? 1e4 : 30000 });
    } catch (e) {
      if (my === seq) {
        updateStatus();
        hint('Could not trace "' + esc(at.word) + '": ' + esc(explain(e.message)));
      }
      return;
    }
    if (my !== seq)
      return;
    setLspState(j);
    updateStatus();
    if (!j.nodes || !j.nodes.length) {
      hint('"' + esc(at.word) + '" is not a function ' + esc(j.server || "the language server") + " can trace.");
      return;
    }
    T = { path: d.path, word: at.word, dir: dirPref, roots: j.nodes.map((n) => wrap(n, null)) };
    for (const r of T.roots)
      expand(r);
  }
  async function expand(node) {
    if (node.cycle)
      return;
    node.open = true;
    if (node.kids) {
      draw();
      return;
    }
    node.loading = true;
    draw();
    const t = T, dir = t.dir;
    try {
      const j = await api("/api/lsp/calls", { path: t.path, item: node.n.item, dir, wait: 30000 });
      if (t !== T || dir !== T.dir)
        return;
      node.kids = (j.nodes || []).map((n) => wrap(n, node));
    } catch (e) {
      if (t !== T || dir !== T.dir)
        return;
      node.err = explain(e.message);
      node.kids = [];
    }
    node.loading = false;
    draw();
  }
  function setDir(dir) {
    dirPref = dir;
    $$("#calls-dir [data-dir]").forEach((b) => b.classList.toggle("on", b.dataset.dir === dir));
    if (!T || T.dir === dir)
      return;
    T.dir = dir;
    for (const r of T.roots)
      Object.assign(r, { kids: null, open: false, loading: false, err: "" });
    for (const r of T.roots)
      expand(r);
  }
  function draw() {
    const el = listEl();
    if (!el || !T)
      return;
    flat.length = 0;
    const none = T.dir === "in" ? "no callers found" : "calls nothing traceable";
    let html = "";
    const walk = (node, depth) => {
      const i = flat.push(node) - 1;
      const n = node.n, t = target(node);
      const arrow = node.cycle ? "&#8635;" : node.loading ? "&#8230;" : node.open ? "&#9660;" : "&#9654;";
      const calls = n.sites && n.sites.length > 1 ? " &times;" + n.sites.length : "";
      const tip = t.path + ":" + t.line + (node.cycle ? `
(recursive, already in this trail)` : "") + (n.detail ? `
` + n.detail : "");
      html += '<div class="sym cnode" data-i="' + i + '" style="padding-left:' + (6 + depth * 14) + 'px" title="' + esc(tip) + '">' + '<span class="car' + (node.cycle ? " cyc" : "") + '">' + arrow + "</span>" + '<span class="kd" data-k="' + esc(n.kind) + '">' + esc(n.kind) + "</span>" + '<span class="sn">' + esc(n.name) + "</span>" + '<span class="sl">' + esc(base(t.path)) + ":" + t.line + calls + "</span></div>";
      const pad = 'style="padding-left:' + (26 + (depth + 1) * 14) + 'px"';
      if (node.err)
        html += '<div class="cnone" ' + pad + ">" + esc(node.err) + "</div>";
      else if (node.open && node.kids && !node.kids.length)
        html += '<div class="cnone" ' + pad + ">" + none + "</div>";
      if (node.open && node.kids)
        for (const k of node.kids)
          walk(k, depth + 1);
    };
    for (const r of T.roots)
      walk(r, 0);
    el.innerHTML = html;
  }
  function openLspSetup() {
    showRightInspector("calls");
    T = null;
    renderLspSetup(listEl(), () => showCalls(S2.at));
  }
  function initCalls() {
    $("#calls-dir")?.addEventListener("click", (e) => {
      const b = e.target.closest("[data-dir]");
      if (b)
        setDir(b.dataset.dir);
    });
    $('.inspector-tab[data-itab="calls"]')?.addEventListener("click", () => {
      if (!T && (S2.at || S2.lsp.state === "off" || S2.lsp.state === "failed"))
        showCalls(S2.at);
    });
    $("#st-lsp")?.addEventListener("click", () => {
      if (S2.lsp.missing || S2.lsp.state === "failed")
        openLspSetup();
    });
    listEl()?.addEventListener("click", async (e) => {
      const row = e.target.closest(".cnode");
      if (!row)
        return;
      const node = flat[+row.dataset.i];
      if (!node)
        return;
      if (e.target.closest(".car")) {
        if (node.open) {
          node.open = false;
          draw();
        } else
          expand(node);
        return;
      }
      $$("#right-calls-list .cnode.sel").forEach((x) => x.classList.remove("sel"));
      row.classList.add("sel");
      const t = target(node);
      await openFile(t.path, { line: t.line });
      const called = T && T.dir === "in" && node.parent ? node.parent.n.name : node.n.name;
      flashFind(called);
    });
  }

  // web/src/hover.js
  var hovercard = $("#hovercard");
  var HOVER_DELAY = 380;
  var HOVER_KEEP = 26;
  var hoverTimer = 0;
  var hoverSeq = 0;
  var moveRAF = 0;
  var pendingMove = null;
  var pointerAt = null;
  var sameWord = (a, b) => !!a && !!b && a.line === b.line && a.col === b.col && a.word === b.word;
  function onMove({ x, y, mod }) {
    if (mod) {
      const at = doc_() ? wordAtPoint(x, y) : null;
      if (!sameWord(at, S2.link)) {
        S2.link = at;
        vp.classList.toggle("linking", !!at);
        paint();
      }
      clearTimeout(hoverTimer);
      hideHover();
      return;
    }
    if (S2.link) {
      S2.link = null;
      vp.classList.remove("linking");
      paint();
    }
    if (S2.hoverAnchor) {
      if (!hovercard.hidden) {
        const rect = hovercard.getBoundingClientRect();
        if (x >= rect.left - 4 && x <= rect.right + 4 && y >= rect.top - 4 && y <= rect.bottom + 4)
          return;
      }
      const dx = x - S2.hoverAnchor.x, dy = y - S2.hoverAnchor.y;
      if (dx * dx + dy * dy > HOVER_KEEP * HOVER_KEEP)
        hideHover();
      else
        return;
    }
    if (S2.lsp.state !== "ready" && S2.lsp.state !== "indexing")
      return;
    clearTimeout(hoverTimer);
    hoverTimer = setTimeout(() => hoverAt(x, y), HOVER_DELAY);
  }
  function hoverAt(x, y) {
    const at = doc_() ? wordAtPoint(x, y) : null;
    if (at && at.word)
      showHover(at, x, y);
  }
  async function showHover(at, x, y) {
    const d = doc_();
    if (!d || at.path !== d.path)
      return;
    const seq = ++hoverSeq;
    let j;
    try {
      j = await api("/api/lsp/hover", { path: d.path, line: at.line, col: at.col, wait: 4000 });
    } catch {
      return;
    }
    if (seq !== hoverSeq || doc_() !== d)
      return;
    setLspState(j);
    if (!j || j.empty || !j.signature && !j.doc)
      return;
    S2.hover = at;
    S2.hoverAnchor = { x, y };
    const refPath = d.path + ":" + at.line;
    hovercard.innerHTML = (j.signature ? '<div class="sig">' + j.signature + "</div>" : "") + (j.doc ? '<div class="doc">' + esc(j.doc) + "</div>" : "") + '<div class="actions">' + '<button id="hc-copy-ref" title="Copy file and line reference">Copy Ref</button>' + '<button id="hc-copy-ai" title="Copy snippet with file path for AI Agent / LLMs">Copy for Agent</button>' + '<button id="hc-find-refs" title="Find all usages across codebase">Usages</button>' + '<button id="hc-calls" title="' + withKeys("Trace callers and callees ({Alt+Shift+H})") + '">Calls</button>' + "</div>" + '<div class="foot"><b>' + esc(j.server || "lsp") + "</b>" + "<span>" + withKeys("{Mod+Click} definition") + "</span>" + "<span>" + withKeys("{Shift+F12} references") + "</span></div>";
    const btnRef = hovercard.querySelector("#hc-copy-ref");
    const btnAi = hovercard.querySelector("#hc-copy-ai");
    const btnRefs = hovercard.querySelector("#hc-find-refs");
    if (btnRef)
      btnRef.onclick = (e) => {
        e.stopPropagation();
        copyToClipboard(refPath, "Copied " + refPath);
      };
    if (btnAi)
      btnAi.onclick = (e) => {
        e.stopPropagation();
        const lineText = d.lines[at.line - 1] || at.word || "";
        const ext = d.path.split(".").pop() || "";
        const text = "### Reference: " + refPath + "\n```" + ext + `
` + lineText + "\n```";
        copyToClipboard(text, "Copied snippet for Agent (" + refPath + ")");
      };
    if (btnRefs)
      btnRefs.onclick = (e) => {
        e.stopPropagation();
        hideHover();
        findReferences(at.word);
      };
    const btnCalls = hovercard.querySelector("#hc-calls");
    if (btnCalls)
      btnCalls.onclick = (e) => {
        e.stopPropagation();
        hideHover();
        S2.at = at;
        showCalls(at);
      };
    hovercard.hidden = false;
    placeHover(x, y);
  }
  function placeHover(x, y) {
    const host = editor.getBoundingClientRect();
    const card = hovercard.getBoundingClientRect();
    let left = x - host.left + 6;
    let top = y - host.top + 20;
    if (left + card.width > host.width - 12)
      left = Math.max(8, host.width - card.width - 12);
    if (top + card.height > host.height - 8) {
      const above = y - host.top - card.height - 12;
      top = above > 8 ? above : Math.max(8, host.height - card.height - 8);
    }
    hovercard.style.left = left + "px";
    hovercard.style.top = top + "px";
  }
  function hideHover() {
    hoverSeq++;
    S2.hover = null;
    S2.hoverAnchor = null;
    if (!hovercard.hidden) {
      hovercard.hidden = true;
      hovercard.innerHTML = "";
    }
  }
  function clearLink() {
    clearTimeout(hoverTimer);
    hideHover();
    if (S2.link) {
      S2.link = null;
      vp.classList.remove("linking");
      paint();
    }
  }
  function initHover() {
    vp.addEventListener("mousemove", (e) => {
      pointerAt = { x: e.clientX, y: e.clientY };
      pendingMove = { x: e.clientX, y: e.clientY, mod: e[MOD] };
      if (moveRAF)
        return;
      moveRAF = requestAnimationFrame(() => {
        moveRAF = 0;
        const m = pendingMove;
        pendingMove = null;
        if (m)
          onMove(m);
      });
    });
    vp.addEventListener("mouseleave", () => {
      pointerAt = null;
      clearLink();
    });
    vp.addEventListener("scroll", () => {
      clearTimeout(hoverTimer);
      hideHover();
    }, { passive: true });
    vp.addEventListener("mousedown", (e) => {
      if (e.target.closest("#hovercard"))
        return;
      hideHover();
    });
    const modKey = isMac ? "Meta" : "Control";
    addEventListener("keydown", (e) => {
      if (e.key === modKey && pointerAt)
        onMove({ ...pointerAt, mod: true });
    });
    addEventListener("keyup", (e) => {
      if (e.key === modKey)
        clearLink();
    });
  }

  // web/src/markdown.js
  var mdview = $("#mdview");
  var mdArticle = $("#md");
  var mdShown = null;
  var mdDrawn = null;
  var mdGen = 0;
  function previewing(d = doc_()) {
    return !!(d && d.markdown && S2.mdPreview && !d.mdError && !d.diffMode);
  }
  function syncPreview() {
    const d = doc_();
    const want = previewing(d) ? d : null;
    if (want === mdShown)
      return;
    if (mdShown && mdDrawn === mdShown)
      mdShown.mdScroll = mdview.scrollTop;
    mdShown = want;
    mdDrawn = null;
    mdview.hidden = !want;
    mdArticle.replaceChildren();
    if (want)
      drawPreview(want);
  }
  async function drawPreview(d) {
    const gen = ++mdGen;
    if (d.mdHtml === undefined) {
      try {
        d.mdReq = d.mdReq || api("/api/markdown", { path: d.path });
        d.mdHtml = (await d.mdReq).html;
      } catch (e) {
        d.mdError = e.message;
        if (gen === mdGen && mdShown === d) {
          showToast("!", "No preview for " + d.name + ": " + e.message);
          syncPreview();
          updateStatus();
        }
        return;
      } finally {
        d.mdReq = null;
      }
      if (gen !== mdGen || mdShown !== d)
        return;
    }
    mdArticle.replaceChildren(mdSanitize(d.mdHtml, d.path));
    mdEnhance();
    mdDrawn = d;
    const target = d.mdAnchor && mdFindAnchor(d.mdAnchor);
    if (target)
      mdScrollTo(target);
    else if (d.mdLine)
      previewLine(d.mdLine);
    else
      mdview.scrollTop = d.mdScroll || 0;
    d.mdAnchor = "";
    d.mdLine = 0;
    if (!findbar.hidden)
      runFind();
  }
  function togglePreview() {
    const d = doc_();
    if (!d || !d.markdown) {
      showToast("!", "Preview works on Markdown files");
      return;
    }
    hideHover();
    if (previewing(d)) {
      const line = mdDrawn === d ? previewTopLine() : 1;
      mdSetPref(false);
      syncPreview();
      sourceToLine(line);
    } else {
      d.mdError = "";
      d.mdLine = sourceTopLine();
      mdSetPref(true);
      syncPreview();
    }
    if (!findbar.hidden)
      runFind();
    else
      S2.find = null;
    render();
    updateStatus();
  }
  function mdSetPref(on) {
    S2.mdPreview = on;
    try {
      localStorage.setItem("px0.mdPreview", on ? "true" : "false");
    } catch {}
  }
  var HTML_NS = "http://www.w3.org/1999/xhtml";
  var MD_DROP = new Set(("script style iframe frame frameset object embed applet template noscript noembed " + "svg math form textarea select option button link meta base title audio video source track canvas dialog").split(" "));
  var MD_KEEP = new Set(("a abbr b bdi bdo blockquote br caption center cite code col colgroup dd del details dfn div dl dt " + "em figcaption figure h1 h2 h3 h4 h5 h6 hr i img input ins kbd li mark ol p pre q rp rt ruby s samp section small span " + "strike strong sub summary sup table tbody td tfoot th thead tr tt u ul var wbr").split(" "));
  var MD_ATTRS = new Set(("align valign alt title lang dir width height colspan rowspan start reversed open checked " + "disabled type data-line data-lang").split(" "));
  var MD_TOKENS = new Set("k kt nf nc nb nv no na nt nd np s m o p c cp gi gd gh ge gs err g".split(" "));
  var MD_SCHEME = /^([a-z][a-z0-9+.-]*):/i;
  var MD_ORIGIN = "http://px0.invalid";
  var mdURL = (ref) => ref.replace(/[\t\n\r]/g, "").replace(/^[\x00-\x20]+|[\x00-\x20]+$/g, "");
  function mdSanitize(html, docPath) {
    const body = new DOMParser().parseFromString(html, "text/html").body;
    const dir = docPath.slice(0, docPath.lastIndexOf("/") + 1);
    const base = MD_ORIGIN + "/" + dir.split("/").map(encodeURIComponent).join("/");
    for (const el of [...body.querySelectorAll("*")]) {
      if (!body.contains(el))
        continue;
      const tag = el.localName;
      if (el.namespaceURI !== HTML_NS || MD_DROP.has(tag)) {
        el.remove();
        continue;
      }
      if (!MD_KEEP.has(tag) || tag === "input" && el.getAttribute("type") !== "checkbox") {
        el.replaceWith(...el.childNodes);
        continue;
      }
      const attrs = {};
      for (const a of [...el.attributes]) {
        attrs[a.name] = a.value;
        el.removeAttribute(a.name);
      }
      for (const name in attrs)
        if (MD_ATTRS.has(name))
          el.setAttribute(name, attrs[name]);
      const id = attrs.id || tag === "a" && attrs.name;
      if (id)
        el.id = "md-" + id;
      if (attrs.class) {
        const keep = attrs.class.split(/\s+/).filter((c) => c === "md-code" || c.startsWith("footnote") || tag === "i" && MD_TOKENS.has(c));
        if (keep.length)
          el.className = keep.join(" ");
      }
      if (tag === "input")
        el.disabled = true;
      if (tag === "img")
        mdSetImage(el, mdURL(attrs.src || ""), base);
      if (tag === "a" && attrs.href)
        mdSetLink(el, mdURL(attrs.href), base);
    }
    const frag = document.createDocumentFragment();
    while (body.firstChild)
      frag.appendChild(document.adoptNode(body.firstChild));
    return frag;
  }
  function mdLocal(ref, base) {
    let u;
    try {
      u = new URL(ref, base);
    } catch {
      return null;
    }
    if (u.origin !== MD_ORIGIN)
      return null;
    let path = u.pathname;
    try {
      path = decodeURIComponent(path);
    } catch {}
    return { path: path.slice(1), hash: u.hash.slice(1) };
  }
  function mdSetImage(img, src, base) {
    const m = MD_SCHEME.exec(src);
    if (m) {
      if (/^https?$/i.test(m[1]) || /^data:image\//i.test(src))
        img.setAttribute("src", src);
    } else if (src.startsWith("//")) {
      img.setAttribute("src", src);
    } else if (src) {
      const t = mdLocal(src, base);
      if (t)
        img.setAttribute("src", "/api/raw?path=" + encodeURIComponent(t.path));
    }
  }
  function mdSetLink(a, href, base) {
    if (href.startsWith("#")) {
      a.setAttribute("href", href);
      a.dataset.anchor = href.slice(1);
      return;
    }
    const m = MD_SCHEME.exec(href);
    if (m || href.startsWith("//")) {
      if (m && !/^(https?|mailto)$/i.test(m[1]))
        return;
      a.setAttribute("href", href);
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      return;
    }
    const t = mdLocal(href, base);
    if (!t)
      return;
    a.setAttribute("href", "/api/raw?path=" + encodeURIComponent(t.path));
    a.dataset.path = t.path;
    if (t.hash)
      a.dataset.anchor = t.hash;
  }
  var MD_ALERTS = { note: "Note", tip: "Tip", important: "Important", warning: "Warning", caution: "Caution" };
  function mdEnhance() {
    for (const q of $$("blockquote", mdArticle))
      mdAlert(q);
    for (const pre of $$("pre", mdArticle)) {
      const wrap = document.createElement("div");
      wrap.className = "md-pre";
      if (pre.dataset.lang)
        wrap.dataset.lang = pre.dataset.lang;
      pre.replaceWith(wrap);
      const copy = document.createElement("button");
      copy.className = "md-copy";
      copy.title = "Copy code";
      copy.setAttribute("aria-label", "Copy code");
      copy.innerHTML = '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"><rect x="5.5" y="5.5" width="8" height="8" rx="1.5"/><path d="M10.5 3.5V3a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 3v5A1.5 1.5 0 0 0 4 9.5h.5"/></svg>';
      wrap.append(pre, copy);
    }
  }
  function mdAlert(q) {
    const p = q.firstElementChild;
    const t = p && p.localName === "p" && p.firstChild;
    if (!t || t.nodeType !== 3)
      return;
    const m = /^\s*\[!(\w+)\][ \t]*\n?/.exec(t.nodeValue);
    const kind = m && m[1].toLowerCase();
    if (!kind || !MD_ALERTS[kind])
      return;
    t.nodeValue = t.nodeValue.slice(m[0].length);
    if (!t.nodeValue)
      t.remove();
    if (p.firstChild && p.firstChild.localName === "br")
      p.firstChild.remove();
    if (!p.textContent.trim() && !p.children.length)
      p.remove();
    const title = document.createElement("p");
    title.className = "md-alert-title";
    title.textContent = MD_ALERTS[kind];
    q.prepend(title);
    q.classList.add("md-alert", "md-alert-" + kind);
  }
  var MD_GAP = 16;
  function mdScrollTo(el) {
    mdview.scrollTop += el.getBoundingClientRect().top - mdview.getBoundingClientRect().top - MD_GAP;
  }
  function mdFindAnchor(anchor) {
    let id = anchor;
    try {
      id = decodeURIComponent(anchor);
    } catch {}
    for (const k of [id, id.toLowerCase()]) {
      const el = document.getElementById("md-" + k);
      if (el && mdArticle.contains(el))
        return el;
    }
    return null;
  }
  function previewLine(n) {
    const d = doc_();
    if (!d || mdDrawn !== d) {
      if (d)
        d.mdLine = n;
      return;
    }
    let best = null, at = 0;
    for (const el of mdArticle.querySelectorAll("[data-line]")) {
      const l = +el.dataset.line;
      if (l <= n && l > at) {
        best = el;
        at = l;
      }
    }
    if (best)
      mdScrollTo(best);
    else
      mdview.scrollTop = 0;
  }
  function previewTopLine() {
    const top = mdview.getBoundingClientRect().top + MD_GAP + 8;
    let line = 1;
    for (const el of mdArticle.querySelectorAll("[data-line]")) {
      if (el.getBoundingClientRect().top > top)
        break;
      line = +el.dataset.line;
    }
    return line;
  }
  function sourceTopLine() {
    const top = vp.getBoundingClientRect().top;
    for (const r of rowsEl.children)
      if (r.getBoundingClientRect().bottom > top + 1)
        return +r.dataset.l;
    return 1;
  }
  function sourceToLine(line) {
    vp.scrollTop = (line - 1) * LH;
    for (let i = 0;i < 3; i++) {
      paint();
      const r = rowFor(line);
      const off = r ? r.getBoundingClientRect().top - vp.getBoundingClientRect().top : 0;
      if (Math.abs(off) < 1)
        break;
      vp.scrollTop += off;
    }
  }
  async function mdFollow(path, anchor) {
    const d = doc_();
    path = path.replace(/\/+$/, "");
    if (d && path === d.path) {
      mdJump(anchor);
      return;
    }
    if (d)
      pushHistory(d.path, previewing(d) && mdDrawn === d ? previewTopLine() : d.cur);
    try {
      await api("/api/tree", { dir: path });
      showPanel("files");
      revealDir(path);
      return;
    } catch {}
    const line = /^L(\d+)/.exec(anchor);
    await openFile(path, line ? { line: +line[1] } : {});
    const nd = doc_();
    if (!nd || nd.path !== path) {
      showToast("!", "Cannot open " + path);
      return;
    }
    if (anchor && !line) {
      const el = mdDrawn === nd && mdFindAnchor(anchor);
      if (el)
        mdScrollTo(el);
      else
        nd.mdAnchor = anchor;
    }
  }
  function mdJump(anchor) {
    const d = doc_();
    const el = anchor && mdFindAnchor(anchor);
    if (!d || !el)
      return;
    pushHistory(d.path, previewTopLine());
    mdScrollTo(el);
    const block = el.closest("[data-line]");
    if (block)
      pushHistory(d.path, +block.dataset.line);
  }
  function previewKey(e) {
    const mod = e[MOD];
    if (e.key === "Home" || isMac && mod && e.key === "ArrowUp") {
      mdview.scrollTop = 0;
      return true;
    }
    if (e.key === "End" || isMac && mod && e.key === "ArrowDown") {
      mdview.scrollTop = mdview.scrollHeight;
      return true;
    }
    let by = 0;
    if (e.key === "ArrowDown" || e.key === "j")
      by = 48;
    else if (e.key === "ArrowUp" || e.key === "k")
      by = -48;
    else if (e.key === "PageDown")
      by = mdview.clientHeight * 0.9;
    else if (e.key === "PageUp")
      by = -mdview.clientHeight * 0.9;
    if (!by)
      return false;
    mdview.scrollBy({ top: by });
    return true;
  }
  function selectPreview() {
    const r = document.createRange();
    r.selectNodeContents(mdArticle);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(r);
  }
  function clearPreviewMarks() {
    const marks = $$("mark.md-hit", mdArticle);
    for (const m of marks)
      m.replaceWith(...m.childNodes);
    if (marks.length)
      mdArticle.normalize();
  }
  function findInPreview(q) {
    clearPreviewMarks();
    if (!q)
      return 0;
    const marks = markNodes(mdArticle, q, false, "mark");
    for (const m of marks)
      m.classList.add("md-hit");
    return marks.length;
  }
  function showPreviewHit(i) {
    const marks = $$("mark.md-hit", mdArticle);
    marks.forEach((m, k) => m.classList.toggle("on", k === i));
    const m = marks[i];
    if (!m)
      return;
    const box = mdview.getBoundingClientRect(), r = m.getBoundingClientRect();
    if (r.top < box.top + 40 || r.bottom > box.bottom - 40) {
      mdview.scrollTop += r.top - box.top - mdview.clientHeight / 2;
    }
  }
  function previewHitOffsets() {
    const h = mdview.scrollHeight || 1, top = mdview.getBoundingClientRect().top - mdview.scrollTop;
    const seen = new Set;
    return $$("mark.md-hit", mdArticle).map((m) => ((m.getBoundingClientRect().top - top) / h * 100).toFixed(2)).filter((p) => !seen.has(p) && seen.add(p));
  }
  function scrollPreviewTo(fraction) {
    mdview.scrollTop = fraction * mdview.scrollHeight - mdview.clientHeight / 2;
  }
  function initMarkdown() {
    const sw = $("#md-switch");
    sw.addEventListener("mousedown", (e) => e.preventDefault());
    sw.addEventListener("click", (e) => {
      const b = e.target.closest("[data-md]");
      if (b && b.dataset.md === "preview" !== previewing())
        togglePreview();
    });
    mdArticle.addEventListener("click", (e) => {
      const copy = e.target.closest(".md-copy");
      if (copy) {
        copyToClipboard($("pre", copy.parentElement).textContent, "Copied code block");
        return;
      }
      const a = e.target.closest("a");
      if (!a || e.button !== 0 || e[MOD] || e.shiftKey)
        return;
      if ("path" in a.dataset) {
        e.preventDefault();
        mdFollow(a.dataset.path, a.dataset.anchor || "");
      } else if ("anchor" in a.dataset) {
        e.preventDefault();
        mdJump(a.dataset.anchor);
      }
    });
  }

  // web/src/status.js
  function updateStatus() {
    const d = doc_();
    const sizeEl = $("#st-size");
    if (sizeEl)
      sizeEl.textContent = d ? fmtBytes(d.size) : "";
    const isMd = !!(d && d.markdown), shown = previewing(d);
    const mdBtn = $('[data-action="md-preview"]');
    if (mdBtn) {
      mdBtn.hidden = !isMd;
      mdBtn.classList.toggle("active", shown);
    }
    const sw = $("#md-switch");
    if (sw) {
      sw.hidden = !isMd;
      document.body.classList.toggle("md-tab", isMd);
      for (const b of sw.children)
        b.classList.toggle("on", isMd && b.dataset.md === "preview" === shown);
    }
    const hasDiff = !!(d && d.diffAvailable), mode = d && d.diffMode || "source";
    const dsw = $("#diff-switch");
    if (dsw) {
      dsw.hidden = !hasDiff;
      document.body.classList.toggle("diff-tab", hasDiff);
      for (const b of dsw.children)
        b.classList.toggle("on", hasDiff && b.dataset.diff === mode);
    }
    const idxEl = $("#st-index");
    if (idxEl && S2.meta) {
      idxEl.textContent = S2.meta.indexMs + "ms";
      idxEl.title = `Workspace Indexing: took ${S2.meta.indexMs}ms to index ${S2.meta.files.toLocaleString()} files (${S2.meta.ready ? "ready" : "in progress"})`;
    }
    const verEl = $("#st-ver");
    if (verEl && S2.meta?.version) {
      verEl.textContent = "v" + S2.meta.version;
      verEl.title = `px0 v${S2.meta.version} (Click for shortcuts & help)`;
    }
    drawLspStatus();
  }
  function setStatusNote(msg) {
    const el = $("#st-pos");
    if (el)
      el.textContent = msg;
  }
  function fmtBytes(n) {
    if (n < 1024)
      return n + " B";
    if (n < 1048576)
      return (n / 1024).toFixed(1) + " KB";
    return (n / 1048576).toFixed(1) + " MB";
  }
  function setLspState(j) {
    if (!j || !j.state)
      return;
    S2.lsp.state = j.state;
    S2.lsp.server = j.server || S2.lsp.server;
    if ("missing" in j || j.state !== "off")
      S2.lsp.missing = j.missing || "";
    drawLspStatus();
  }
  function drawLspStatus() {
    const el = $("#st-lsp");
    const { state, server, missing } = S2.lsp;
    el.title = "";
    if (state === "off" && missing) {
      el.dataset.state = "missing";
      el.textContent = "LSP: set up";
      el.title = "No language server for " + missing + ". Click to install or start one.";
      return;
    }
    if (!server || state === "off") {
      el.textContent = "";
      el.removeAttribute("data-state");
      return;
    }
    el.dataset.state = state;
    el.textContent = state === "ready" ? server : server + " " + state;
    if (state === "failed")
      el.title = "The language server did not start. Click for details.";
  }
  function updateMetricsDisplay(m) {
    if (!m)
      return;
    const cpuEl = $("#st-cpu");
    const ramEl = $("#st-ram");
    const contEl = $("#st-metrics");
    if (cpuEl)
      cpuEl.textContent = `${m.cpuUsage.toFixed(1)}%`;
    if (ramEl)
      ramEl.textContent = fmtBytes(m.rssBytes);
    if (contEl) {
      contEl.title = `Editor OS Process Usage:
• Resident RAM (RSS): ${fmtBytes(m.rssBytes)}
• CPU Usage: ${m.cpuUsage.toFixed(1)}%
• Active Goroutines: ${m.goroutines || 0}`;
    }
  }
  async function refreshMetrics() {
    try {
      const m = await api("/api/metrics");
      updateMetricsDisplay(m);
    } catch {}
  }
  function initMetrics() {
    refreshMetrics();
    setInterval(refreshMetrics, 2500);
  }
  var FIT_STEPS = 6;
  var statusEl = $("#status");
  function fitStatus() {
    for (let i = 1;i <= FIT_STEPS; i++)
      statusEl.classList.remove("fit-" + i);
    for (let i = 1;i <= FIT_STEPS && statusEl.scrollWidth > statusEl.clientWidth; i++) {
      statusEl.classList.add("fit-" + i);
    }
  }
  function initStatusFit() {
    new ResizeObserver(fitStatus).observe(statusEl);
    new MutationObserver(fitStatus).observe(statusEl, { childList: true, subtree: true, characterData: true });
    document.fonts?.ready.then(fitStatus);
  }

  // web/src/selbar.js
  var status = $("#status");
  var statsEl = $("#sel-stats");
  var SEL_KEYS = { KeyC: "copy-ref", KeyA: "copy-agent", KeyU: "usages" };
  var current = null;
  var allText = null;
  var allInfo = null;
  function getSelectedRangeInfo() {
    if (S2.selAll)
      return allInfo;
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed || !sel.rangeCount)
      return null;
    const d = doc_();
    if (!d)
      return null;
    const range = sel.getRangeAt(0);
    if (!vp.contains(range.commonAncestorContainer))
      return null;
    const text = sel.toString().trim();
    if (!text)
      return null;
    let startEl = range.startContainer;
    if (startEl.nodeType !== 1)
      startEl = startEl.parentElement;
    let endEl = range.endContainer;
    if (endEl.nodeType !== 1)
      endEl = endEl.parentElement;
    const startRow = startEl ? startEl.closest(".row") : null;
    const endRow = endEl ? endEl.closest(".row") : null;
    let l1 = d.cur || 1, l2 = d.cur || 1;
    if (startRow && startRow.dataset.l)
      l1 = +startRow.dataset.l;
    if (endRow && endRow.dataset.l)
      l2 = +endRow.dataset.l;
    if (l1 > l2) {
      const tmp = l1;
      l1 = l2;
      l2 = tmp;
    }
    return { text, l1, l2, path: d.path };
  }
  var refOf = ({ path, l1, l2 }) => path + ":" + (l1 === l2 ? l1 : l1 + "-" + l2);
  function showSelectionBar(info) {
    current = info;
    const ref = refOf(info);
    const lines = info.l2 - info.l1 + 1;
    statsEl.title = ref;
    statsEl.textContent = (lines === 1 ? "1 line" : lines + " lines") + " · " + info.text.length.toLocaleString() + " chars";
    status.classList.add("selecting");
    fitStatus();
  }
  function hideSelectionBar() {
    if (!current)
      return;
    current = null;
    status.classList.remove("selecting");
    fitStatus();
  }
  function updateSelectionBar() {
    const info = getSelectedRangeInfo();
    if (info)
      showSelectionBar(info);
    else
      hideSelectionBar();
  }
  function selectAll() {
    const d = doc_();
    if (!d)
      return;
    window.getSelection()?.removeAllRanges();
    S2.selAll = d;
    allInfo = null;
    render();
    const text = allText = fetch("/api/raw?path=" + encodeURIComponent(d.path)).then((r) => {
      if (!r.ok)
        throw new Error(r.statusText);
      return r.text();
    });
    text.then((t) => {
      if (allText !== text)
        return;
      allInfo = { text: t, l1: 1, l2: d.total, path: d.path };
      showSelectionBar(allInfo);
    }, () => {
      if (allText !== text)
        return;
      clearSelectAll();
      showToast("!", "Could not read " + d.path);
    });
  }
  function clearSelectAll() {
    if (!S2.selAll)
      return;
    S2.selAll = null;
    allText = null;
    allInfo = null;
    render();
    hideSelectionBar();
  }
  function copySelectAll() {
    const d = S2.selAll;
    if (!d || !allText)
      return false;
    allText.then((t) => copyToClipboard(t, "Copied " + d.path + " (" + d.total.toLocaleString() + " lines)"), () => {});
    return true;
  }
  function runSelectionAction(act) {
    if (!current)
      return false;
    const { text, path } = current;
    const ref = refOf(current);
    if (act === "copy-ref") {
      copyToClipboard(ref, "Copied " + ref);
    } else if (act === "copy-agent") {
      const ext = path.split(".").pop() || "";
      copyToClipboard("### Reference: " + ref + "\n```" + ext + `
` + text + "\n```", "Copied snippet for Agent (" + ref + ")");
    } else if (act === "usages") {
      findReferences(text.split(/\s+/)[0] || text);
    } else {
      return false;
    }
    return true;
  }
  function initSelectionBar() {
    document.addEventListener("mouseup", () => setTimeout(updateSelectionBar, 20));
    vp.addEventListener("keyup", (e) => {
      if (e.shiftKey)
        setTimeout(updateSelectionBar, 20);
    });
    document.addEventListener("selectionchange", () => {
      if (current)
        updateSelectionBar();
    });
    document.addEventListener("mousedown", (e) => {
      if (!S2.selAll || e.target.closest?.("#footer-sel"))
        return;
      if (e.target === vp && (e.offsetX >= vp.clientWidth || e.offsetY >= vp.clientHeight))
        return;
      clearSelectAll();
    }, true);
    const bar = $("#footer-sel");
    bar.addEventListener("mousedown", (e) => e.preventDefault());
    bar.addEventListener("click", (e) => {
      const btn = e.target.closest("[data-sel]");
      if (btn)
        runSelectionAction(btn.dataset.sel);
    });
  }

  // web/src/diff.js
  var diffview = $("#diffview");
  var diffContent = $("#diffcontent");
  var shown = null;
  function setLayoutPref(mode) {
    try {
      localStorage.setItem("px0.diffLayout", mode);
    } catch {}
  }
  function layoutPref() {
    try {
      return localStorage.getItem("px0.diffLayout") || "split";
    } catch {
      return "split";
    }
  }
  function diffMode(d = doc_()) {
    return d && d.diffMode || null;
  }
  function syncDiffView() {
    const d = doc_();
    const want = d && d.diffMode ? d : null;
    if (want !== shown) {
      shown = want;
      diffview.hidden = !want;
      if (want)
        drawDiff(want);
      else
        diffContent.replaceChildren();
    } else if (want && want.diffHunks !== undefined) {
      renderDiff(want);
    }
  }
  async function toggleDiff() {
    if (!S2.meta?.git)
      return;
    const d = doc_();
    if (!d)
      return;
    if (!d.diffMode && !d.diffAvailable) {
      setStatusNote("No diff — clean file or not a git repo");
      return;
    }
    setDiffMode(d.diffMode ? "source" : layoutPref());
  }
  async function setDiffMode(mode) {
    const d = doc_();
    if (!d)
      return;
    if (mode !== "source" && !d.diffAvailable) {
      setStatusNote("No diff — clean file or not a git repo");
      return;
    }
    if (mode === "source") {
      d.diffMode = null;
    } else {
      d.diffMode = mode;
      setLayoutPref(mode);
    }
    syncPreview();
    syncDiffView();
    updateStatus();
  }
  async function drawDiff(d) {
    if (d.diffText === undefined) {
      diffContent.replaceChildren();
      try {
        d.diffReq = d.diffReq || api("/api/diff", { path: d.path });
        const j = await d.diffReq;
        d.diffText = j.diff || "";
        d.diffHunks = parseDiff(d.diffText);
      } catch (e) {
        d.diffText = "";
        d.diffHunks = [];
        setStatusNote("No diff: " + e.message);
      } finally {
        d.diffReq = null;
      }
      if (shown !== d)
        return;
    }
    renderDiff(d);
  }
  function renderDiff(d) {
    diffContent.replaceChildren();
    if (!d.diffHunks || !d.diffHunks.length) {
      const p = document.createElement("div");
      p.className = "diff-empty";
      p.textContent = "No changes against HEAD.";
      diffContent.append(p);
      return;
    }
    const frag = document.createDocumentFragment();
    for (const hunk of d.diffHunks) {
      frag.append(hunkHeader(hunk));
      frag.append(d.diffMode === "unified" ? unifiedTable(hunk) : splitTable(hunk));
    }
    diffContent.append(frag);
  }
  function hunkHeader(hunk) {
    const el = document.createElement("div");
    el.className = "diff-hunk-head";
    el.textContent = "@@ -" + hunk.oldStart + " +" + hunk.newStart + " @@" + (hunk.section ? " " + hunk.section : "");
    return el;
  }
  var HUNK_RE = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@[ \t]?(.*)$/;
  function parseDiff(text) {
    if (!text)
      return [];
    const hunks = [];
    let cur = null, oldLine = 0, newLine = 0;
    for (const line of text.split(`
`)) {
      const m = HUNK_RE.exec(line);
      if (m) {
        oldLine = +m[1];
        newLine = +m[3];
        cur = { oldStart: oldLine, newStart: newLine, section: m[5] || "", rows: [] };
        hunks.push(cur);
        continue;
      }
      if (!cur || line === "" || line.startsWith("\\"))
        continue;
      const c = line[0], body = line.slice(1);
      if (c === "+")
        cur.rows.push({ type: "add", newLine: newLine++, text: body });
      else if (c === "-")
        cur.rows.push({ type: "del", oldLine: oldLine++, text: body });
      else
        cur.rows.push({ type: "ctx", oldLine: oldLine++, newLine: newLine++, text: body });
    }
    return hunks;
  }
  function unifiedTable(hunk) {
    const table = document.createElement("div");
    table.className = "diff-table diff-unified";
    for (const row of hunk.rows) {
      const r = document.createElement("div");
      r.className = "diff-row diff-" + row.type;
      r.append(lineCell(row.type === "add" ? "" : row.oldLine), lineCell(row.type === "del" ? "" : row.newLine), markerCell(row.type), codeCell(row.text));
      table.append(r);
    }
    return table;
  }
  function splitTable(hunk) {
    const table = document.createElement("div");
    table.className = "diff-table diff-split";
    for (const pair of pairRows(hunk.rows)) {
      const r = document.createElement("div");
      r.className = "diff-row-pair";
      r.append(splitSide(pair.left, "left"), splitSide(pair.right, "right"));
      table.append(r);
    }
    return table;
  }
  function pairRows(rows) {
    const pairs = [];
    let i = 0;
    while (i < rows.length) {
      const row = rows[i];
      if (row.type === "ctx") {
        pairs.push({ left: row, right: row });
        i++;
        continue;
      }
      let dels = [], adds = [];
      while (i < rows.length && rows[i].type === "del")
        dels.push(rows[i++]);
      while (i < rows.length && rows[i].type === "add")
        adds.push(rows[i++]);
      const n = Math.max(dels.length, adds.length);
      for (let k = 0;k < n; k++)
        pairs.push({ left: dels[k] || null, right: adds[k] || null });
    }
    return pairs;
  }
  function splitSide(row, side) {
    const el = document.createElement("div");
    el.className = "diff-side diff-side-" + side + (row ? " diff-" + row.type : " diff-blank");
    if (!row) {
      el.append(lineCell(""), markerCell(""), codeCell(""));
      return el;
    }
    const ln = side === "left" ? row.oldLine : row.newLine;
    el.append(lineCell(ln), markerCell(row.type), codeCell(row.text));
    return el;
  }
  function lineCell(n) {
    const el = document.createElement("div");
    el.className = "diff-ln";
    el.textContent = n === "" || n === undefined ? "" : String(n);
    return el;
  }
  var MARKS = { add: "+", del: "-", ctx: "" };
  function markerCell(type) {
    const el = document.createElement("div");
    el.className = "diff-mk";
    el.textContent = MARKS[type] || "";
    return el;
  }
  function codeCell(text) {
    const el = document.createElement("div");
    el.className = "diff-code";
    el.innerHTML = esc(text || "") || "&nbsp;";
    return el;
  }
  function initDiff() {
    const sw = $("#diff-switch");
    sw.addEventListener("mousedown", (e) => e.preventDefault());
    sw.addEventListener("click", (e) => {
      const b = e.target.closest("[data-diff]");
      if (!b)
        return;
      setDiffMode(b.dataset.diff === diffMode() ? "source" : b.dataset.diff);
    });
  }

  // web/src/tabs.js
  var closedTabs = [];
  var MAX_CLOSED = 20;
  function isImageTab(d) {
    return !!(d && d.image);
  }
  function showImageTab(d) {
    document.body.classList.add("image-tab");
    showImage(d.path);
  }
  function hideImageTab() {
    document.body.classList.remove("image-tab");
    hideImage();
  }
  function activateImageTab(d, { push, path }) {
    $("#empty").hidden = true;
    showImageTab(d);
    syncPreview();
    syncDiffView();
    S2.at = null;
    S2.lsp.state = "off";
    S2.lsp.server = "";
    S2.lsp.missing = "";
    drawTabs();
    drawCrumbs();
    updateStatus();
    if (push)
      pushHistory(path);
  }
  async function openFile(path, opts = {}) {
    const { line, push = true, col } = opts;
    let idx = S2.tabs.findIndex((t) => t.path === path);
    if (idx < 0) {
      let j;
      const start = line ? Math.max(0, Math.floor((line - 1) / CHUNK) * CHUNK) : 0;
      try {
        j = await api("/api/file", { path, start, count: CHUNK });
      } catch (e) {
        setStatusNote(path + ": " + e.message);
        return;
      }
      if (j.image) {
        S2.tabs.push({ path, name: path.split("/").pop(), image: true, size: j.size });
        idx = S2.tabs.length - 1;
      } else {
        const d = {
          path,
          name: path.split("/").pop(),
          lang: j.lang,
          total: j.total,
          maxCols: j.maxCols,
          size: j.size,
          lines: new Array(j.total),
          chunks: new Set([start / CHUNK]),
          pending: new Set,
          refining: new Set,
          scrollTop: 0,
          cur: line || 1,
          outline: null,
          gen: 0,
          markdown: !!j.markdown,
          gutter: null,
          diffMode: null,
          diffAvailable: false
        };
        for (let i = 0;i < j.lines.length; i++)
          d.lines[j.start + i] = j.lines[i];
        d.lsp = j.lsp || { state: "off", server: "" };
        S2.tabs.push(d);
        idx = S2.tabs.length - 1;
        if (j.refine)
          refineChunk(d, start / CHUNK);
        loadGutter(d);
      }
    }
    const prev = doc_();
    if (prev && prev !== S2.tabs[idx] && !isImageTab(prev))
      prev.scrollTop = vp.scrollTop;
    if (prev !== S2.tabs[idx])
      clearSelectAll();
    S2.active = idx;
    const d = S2.tabs[idx];
    if (isImageTab(d)) {
      activateImageTab(d, { push, path });
      return;
    }
    $("#empty").hidden = true;
    hideImageTab();
    syncPreview();
    syncDiffView();
    if (!S2.at || S2.at.path !== d.path)
      S2.at = null;
    S2.lsp.state = d.lsp && d.lsp.state || "off";
    S2.lsp.server = d.lsp && d.lsp.server || "";
    S2.lsp.missing = d.lsp && d.lsp.missing || "";
    warmLSP(d);
    drawTabs();
    drawCrumbs();
    layout();
    if (line) {
      d.cur = line;
      centerLine(line);
    } else
      vp.scrollTop = d.scrollTop;
    render();
    updateStatus();
    if ($("#panel-outline")?.classList.contains("active"))
      loadOutline();
    if (push)
      pushHistory(path, line || d.cur, col);
  }
  function loadGutter(d) {
    if (!S2.meta?.git)
      return;
    api("/api/gutter", { path: d.path }).then((j) => {
      d.diffAvailable = !!j.available;
      if (doc_() === d)
        updateStatus();
      if (!j.available)
        return;
      const marks = new Map;
      for (const n of j.modified)
        marks.set(n, "mod");
      for (const n of j.added)
        marks.set(n, "add");
      d.gutter = { marks, dels: new Set(j.deleted) };
      if (doc_() === d)
        render();
    }).catch(() => {});
  }
  function centerLine(n) {
    if (previewing()) {
      previewLine(n);
      return;
    }
    const y = (n - 1) * LH - Math.max(0, vp.clientHeight / 2 - LH * 2);
    vp.scrollTop = Math.max(0, y);
  }
  function closeTab(i) {
    clearSelectAll();
    const [closed] = S2.tabs.splice(i, 1);
    if (closed) {
      if (closed.path) {
        const scrollTop = i === S2.active ? vp.scrollTop : closed.scrollTop;
        closedTabs.push({ path: closed.path, cur: closed.cur, scrollTop });
        if (closedTabs.length > MAX_CLOSED)
          closedTabs.shift();
        api("/api/close", { path: closed.path }).then(() => refreshMetrics()).catch(() => {});
      }
      closed.lines = null;
      closed.chunks?.clear?.();
      closed.pending?.clear?.();
      closed.refining?.clear?.();
      closed.outline = null;
    }
    if (S2.tabs.length === 0) {
      S2.active = -1;
      hideImageTab();
      syncPreview();
      syncDiffView();
      rowsEl.innerHTML = "";
      sizer.style.height = "0px";
      $("#empty").hidden = false;
      drawCrumbs();
      drawTabs();
      updateStatus();
      return;
    }
    S2.active = Math.min(i, S2.tabs.length - 1);
    const d = doc_();
    if (isImageTab(d)) {
      syncPreview();
      syncDiffView();
      drawTabs();
      drawCrumbs();
      showImageTab(d);
      updateStatus();
      return;
    }
    hideImageTab();
    syncPreview();
    syncDiffView();
    drawTabs();
    drawCrumbs();
    layout();
    vp.scrollTop = d.scrollTop;
    render();
    updateStatus();
  }
  async function reopenClosedTab() {
    while (closedTabs.length) {
      const t = closedTabs.pop();
      if (S2.tabs.some((d) => d.path === t.path))
        continue;
      await openFile(t.path, { line: t.cur });
      if (doc_()?.path !== t.path)
        return;
      vp.scrollTop = t.scrollTop;
      render();
      updateStatus();
      return;
    }
  }
  function drawTabs() {
    $("#tabs").innerHTML = S2.tabs.map((t, i) => '<div class="tab' + (i === S2.active ? " active" : "") + '" data-i="' + i + '" title="' + esc(t.path) + '">' + '<span class="tn">' + esc(t.name) + '</span><span class="x" data-close="' + i + '" title="' + withKeys("Close tab ({Alt+W})") + '"><svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2 2l6 6M8 2l-6 6"/></svg></span></div>').join("");
    const act = $("#tabs .tab.active");
    if (act)
      act.scrollIntoView({ block: "nearest", inline: "nearest" });
  }
  function switchTab(i) {
    if (i === S2.active || !S2.tabs[i])
      return;
    clearLink();
    const prev = doc_();
    if (prev && !isImageTab(prev))
      prev.scrollTop = vp.scrollTop;
    S2.active = i;
    const d = S2.tabs[i];
    clearFind();
    clearSelectAll();
    S2.at = null;
    if (isImageTab(d)) {
      syncPreview();
      syncDiffView();
      S2.lsp.state = "off";
      S2.lsp.server = "";
      S2.lsp.missing = "";
      drawTabs();
      drawCrumbs();
      showImageTab(d);
      updateStatus();
      pushHistory(d.path);
      return;
    }
    hideImageTab();
    syncPreview();
    syncDiffView();
    S2.lsp.state = d.lsp && d.lsp.state || "off";
    S2.lsp.server = d.lsp && d.lsp.server || "";
    S2.lsp.missing = d.lsp && d.lsp.missing || "";
    warmLSP(d);
    drawTabs();
    drawCrumbs();
    layout();
    vp.scrollTop = d.scrollTop;
    render();
    updateStatus();
    if ($("#panel-outline")?.classList.contains("active"))
      loadOutline();
    pushHistory(d.path, d.cur);
  }
  function drawCrumbs() {
    const el = $("#crumbs");
    if (el)
      el.innerHTML = "";
  }
  function showImage(path) {
    hideImage();
    const box = document.createElement("div");
    box.id = "imgview";
    box.innerHTML = '<img src="/api/raw?path=' + encodeURIComponent(path) + '" alt="">';
    editor.appendChild(box);
    $("#empty").hidden = true;
  }
  function hideImage() {
    const b = $("#imgview");
    if (b)
      b.remove();
  }
  function initTabs() {
    $("#tabs").addEventListener("click", (e) => {
      const x = e.target.closest("[data-close]");
      if (x) {
        closeTab(+x.dataset.close);
        return;
      }
      const t = e.target.closest(".tab");
      if (t)
        switchTab(+t.dataset.i);
    });
    $("#tabs").addEventListener("auxclick", (e) => {
      const t = e.target.closest(".tab");
      if (t && e.button === 1) {
        e.preventDefault();
        closeTab(+t.dataset.i);
      }
    });
    const crumbsEl = $("#crumbs");
    if (crumbsEl) {
      crumbsEl.addEventListener("click", (e) => {
        const c = e.target.closest("[data-dir]");
        if (c) {
          showPanel("files");
          revealDir(c.dataset.dir);
        }
      });
    }
  }

  // web/src/theme.js
  var KEY = "px0.theme";
  var THEME_SELECTOR = /^(?::root|html)?\[data-theme=["']?([\w-]+)["']?\]$/;
  var themes = null;
  function listThemes() {
    if (themes)
      return themes;
    const found = new Map;
    const walk = (rules) => {
      for (const r of rules) {
        if (r.styleSheet) {
          try {
            walk(r.styleSheet.cssRules);
          } catch {}
          continue;
        }
        if (!r.selectorText) {
          if (r.cssRules)
            walk(r.cssRules);
          continue;
        }
        for (const part of r.selectorText.split(",")) {
          const m = part.trim().match(THEME_SELECTOR);
          if (!m)
            continue;
          const t = found.get(m[1]) || { id: m[1], name: m[1], scheme: "" };
          const name = r.style.getPropertyValue("--theme-name").trim().replace(/^["']|["']$/g, "");
          const scheme = r.style.getPropertyValue("color-scheme").trim();
          if (name)
            t.name = name;
          if (scheme)
            t.scheme = scheme;
          found.set(m[1], t);
        }
      }
    };
    for (const sheet of document.styleSheets) {
      try {
        walk(sheet.cssRules);
      } catch {}
    }
    themes = [...found.values()].sort((a, b) => a.name.localeCompare(b.name));
    return themes;
  }
  var currentTheme = () => document.documentElement.dataset.theme;
  function setTheme(id, persist = true) {
    if (!listThemes().some((t) => t.id === id))
      return false;
    document.documentElement.dataset.theme = id;
    if (persist) {
      try {
        localStorage.setItem(KEY, id);
      } catch {}
    }
    return true;
  }
  function cycleTheme() {
    const all = listThemes();
    if (!all.length)
      return;
    const next = all[(all.findIndex((t) => t.id === currentTheme()) + 1) % all.length];
    setTheme(next.id);
    showToast("Theme", next.name);
  }
  function initTheme() {
    let saved = null;
    try {
      saved = localStorage.getItem(KEY);
    } catch {}
    if (saved && setTheme(saved, false))
      return;
    const all = listThemes();
    if (all.length && !all.some((t) => t.id === currentTheme()))
      setTheme(all[0].id, false);
  }

  // web/src/shortcuts.js
  var SHORTCUTS = [
    [["Mod+K"], "Quick search / palette"],
    [["Mod+P"], "Go to file"],
    [["Mod+Shift+P"], "Command palette"],
    [["Mod+Shift+O"], "Go to symbol"],
    [["Mod+Shift+F"], "Search in files"],
    [["Mod+F"], "Find in file"],
    [["Mod+G"], "Go to line"],
    [["Mod+D"], "Toggle diff view (git)"],
    [["Alt+Z"], "Toggle word wrap"],
    [["Alt+L"], "Toggle line numbers"],
    [["Alt+M"], "Toggle Markdown preview"],
    [["Enter", "Shift+Enter"], "Next / previous match"],
    [["F12", "Mod+Click"], "Go to definition"],
    [["Shift+F12"], "Find all references"],
    [["Alt+Shift+H"], "Call trail (callers / callees)"],
    [["Mod+J"], "Toggle right inspector (Symbols/Refs)"],
    [["Alt+Left", "Alt+Right"], "Navigate back / forward"],
    [["Mod+B"], "Toggle sidebar"],
    [["Alt+W"], "Close tab"],
    [["Alt+Shift+T"], "Reopen closed tab"],
    [["Ctrl+Tab"], "Next tab"],
    [["Alt+1…9"], "Select tab"],
    [["Double click"], "Highlight all occurrences"],
    [["Mod+A"], "Select whole file"],
    [["Alt+C", "Alt+A"], "Copy selection ref / for agent"],
    [["Alt+U"], "Find usages of selection"],
    [["Mod+Home|Mod+Up", "Mod+End|Mod+Down"], "Top / bottom of file"],
    [["Home|Mod+Left", "End|Mod+Right"], "Start / end of line"],
    [["Left", "Right"], "Move caret along the line"],
    [["Esc"], "Dismiss"]
  ];
  function showHelp() {
    const h = $("#helpsheet");
    const ver = S2.meta?.version ? ` <span class="help-version">v${esc(S2.meta.version)}</span>` : "";
    h.innerHTML = '<div class="help-card"><div class="help-header"><h2>Keyboard Shortcuts</h2>' + ver + '</div><dl class="help-grid">' + SHORTCUTS.map(([combos, v]) => "<dt>" + combos.map(keyCaps).filter(Boolean).join('<span class="key-or">/</span>') + "</dt>" + "<dd>" + esc(v) + "</dd>").join("") + "</dl></div>";
    h.hidden = false;
  }
  var inField = (el) => el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA");
  function initShortcuts() {
    $("#btn-theme")?.addEventListener("click", cycleTheme);
    $("#btn-help")?.addEventListener("click", showHelp);
    $("#st-ver")?.addEventListener("click", showHelp);
    $("#helpsheet").addEventListener("click", () => {
      $("#helpsheet").hidden = true;
    });
    $("#footer-actions")?.addEventListener("click", (e) => {
      const btn = e.target.closest(".footer-btn");
      if (!btn)
        return;
      const act = btn.dataset.action;
      if (act === "quick-open")
        openPalette("file");
      else if (act === "search") {
        showPanel("search");
        $("#q")?.select();
      } else if (act === "symbols")
        openPalette("symbol");
      else if (act === "find")
        openFind(S2.lastWord);
      else if (act === "goto")
        openPalette("line");
      else if (act === "wrap")
        toggleWordWrap();
      else if (act === "line-numbers")
        toggleLineNumbers();
      else if (act === "md-preview")
        togglePreview();
      else if (act === "palette")
        openPalette("command");
      else if (act === "help")
        showHelp();
    });
    addEventListener("keydown", (e) => {
      const mod = e[MOD];
      if (e.key === "Escape") {
        if (!overlay.hidden) {
          closePalette();
          return;
        }
        if (!$("#helpsheet").hidden) {
          $("#helpsheet").hidden = true;
          return;
        }
        if (!hovercard.hidden) {
          clearLink();
          return;
        }
        if (!findbar.hidden) {
          clearFind();
          return;
        }
        if (S2.selAll) {
          clearSelectAll();
          return;
        }
        if (!document.body.classList.contains("right-hidden")) {
          hideRightInspector();
          return;
        }
        if (S2.occ) {
          S2.occ = null;
          paint();
          return;
        }
        if (inField(document.activeElement))
          document.activeElement.blur();
        return;
      }
      if (mod && (e.key === "k" || e.key === "K")) {
        e.preventDefault();
        openPalette(e.shiftKey ? "command" : "file");
        return;
      }
      if (mod && (e.key === "j" || e.key === "J")) {
        e.preventDefault();
        if (document.body.classList.contains("right-hidden"))
          showRightInspector("refs");
        else
          hideRightInspector();
        return;
      }
      if (mod && e.shiftKey && (e.key === "P" || e.key === "p")) {
        e.preventDefault();
        openPalette("command");
        return;
      }
      if (mod && e.shiftKey && (e.key === "O" || e.key === "o")) {
        e.preventDefault();
        showRightInspector("symbols");
        return;
      }
      if (mod && e.shiftKey && (e.key === "F" || e.key === "f")) {
        e.preventDefault();
        showPanel("search");
        $("#q")?.select();
        return;
      }
      if (mod && !e.shiftKey && (e.key === "p" || e.key === "P")) {
        e.preventDefault();
        openPalette("file");
        return;
      }
      if (mod && (e.key === "g" || e.key === "G")) {
        e.preventDefault();
        openPalette("line");
        return;
      }
      if (mod && (e.key === "f" || e.key === "F")) {
        e.preventDefault();
        openFind(S2.lastWord);
        return;
      }
      if (mod && (e.key === "b" || e.key === "B")) {
        e.preventDefault();
        document.body.classList.toggle("side-hidden");
        layout();
        render();
        return;
      }
      if (mod && !e.shiftKey && (e.key === "d" || e.key === "D")) {
        if (S2.meta?.git) {
          e.preventDefault();
          toggleDiff();
        }
        return;
      }
      if (mod && (e.key === "w" || e.key === "W") || e.altKey && e.code === "KeyW") {
        e.preventDefault();
        e.stopPropagation();
        if (S2.active >= 0)
          closeTab(S2.active);
        return;
      }
      if (e.altKey && e.shiftKey && !mod && e.code === "KeyT") {
        e.preventDefault();
        reopenClosedTab();
        return;
      }
      if (e.key === "F12") {
        e.preventDefault();
        if (e.shiftKey)
          findReferences();
        else
          gotoDefinition();
        return;
      }
      if (e.altKey && e.key === "ArrowLeft") {
        e.preventDefault();
        go(-1);
        return;
      }
      if (e.altKey && e.key === "ArrowRight") {
        e.preventDefault();
        go(1);
        return;
      }
      if (e.ctrlKey && e.key === "Tab") {
        e.preventDefault();
        if (S2.tabs.length > 1)
          switchTab((S2.active + (e.shiftKey ? -1 : 1) + S2.tabs.length) % S2.tabs.length);
        return;
      }
      if (e.altKey && e.shiftKey && e.code === "KeyH") {
        e.preventDefault();
        showCalls();
        return;
      }
      if (e.altKey && !mod && !e.shiftKey && /^Digit[1-9]$/.test(e.code)) {
        e.preventDefault();
        switchTab(+e.code.slice(5) - 1);
        return;
      }
      if (e.altKey && !mod && !e.shiftKey && SEL_KEYS[e.code] && runSelectionAction(SEL_KEYS[e.code])) {
        e.preventDefault();
        return;
      }
      if (e.altKey && e.code === "KeyZ") {
        e.preventDefault();
        toggleWordWrap();
        return;
      }
      if (e.altKey && e.code === "KeyL") {
        e.preventDefault();
        toggleLineNumbers();
        return;
      }
      if (e.altKey && !mod && !e.shiftKey && e.code === "KeyM") {
        e.preventDefault();
        togglePreview();
        return;
      }
      if (inField(document.activeElement))
        return;
      const plainMod = mod && !e.shiftKey && !e.altKey;
      if (plainMod && (e.key === "a" || e.key === "A")) {
        e.preventDefault();
        if (previewing())
          selectPreview();
        else
          selectAll();
        return;
      }
      if (plainMod && (e.key === "c" || e.key === "C") && copySelectAll()) {
        e.preventDefault();
        return;
      }
      if (e.key === "?") {
        e.preventDefault();
        showHelp();
        return;
      }
      const d = doc_();
      if (!d)
        return;
      if (previewing(d)) {
        if (previewKey(e))
          e.preventDefault();
        return;
      }
      const toTop = () => {
        vp.scrollTop = 0;
        d.cur = 1;
        render();
        updateStatus();
      };
      const toBottom = () => {
        vp.scrollTop = sizer.offsetHeight;
        d.cur = d.total;
        render();
        updateStatus();
      };
      if (mod && e.key === "Home") {
        e.preventDefault();
        toTop();
        return;
      }
      if (mod && e.key === "End") {
        e.preventDefault();
        toBottom();
        return;
      }
      if (isMac && mod && e.key === "ArrowUp") {
        e.preventDefault();
        toTop();
        return;
      }
      if (isMac && mod && e.key === "ArrowDown") {
        e.preventDefault();
        toBottom();
        return;
      }
      if (isMac && mod && (e.key === "ArrowLeft" || e.key === "ArrowRight")) {
        e.preventDefault();
        caretToEdge(e.key === "ArrowRight");
        return;
      }
      if (e.key === "ArrowDown" || e.key === "j") {
        e.preventDefault();
        moveCursor(1);
        return;
      }
      if (e.key === "ArrowUp" || e.key === "k") {
        e.preventDefault();
        moveCursor(-1);
        return;
      }
      if (!mod && !e.altKey && e.key === "ArrowLeft") {
        e.preventDefault();
        moveCol(-1);
        return;
      }
      if (!mod && !e.altKey && e.key === "ArrowRight") {
        e.preventDefault();
        moveCol(1);
        return;
      }
      if (!mod && (e.key === "Home" || e.key === "End")) {
        e.preventDefault();
        caretToEdge(e.key === "End");
        return;
      }
      if (e.key === "PageDown") {
        e.preventDefault();
        moveCursor(Math.floor(vp.clientHeight / LH) - 2);
        return;
      }
      if (e.key === "PageUp") {
        e.preventDefault();
        moveCursor(-(Math.floor(vp.clientHeight / LH) - 2));
        return;
      }
    }, { capture: true });
  }

  // web/src/palette.js
  var overlay = $("#overlay");
  var palInput = $("#pal");
  var palList = $("#pal-list");
  var pal = null;
  var COMMANDS = [
    { name: "Go to File…", run: () => openPalette("file") },
    { name: "Go to Symbol in File…", run: () => openPalette("symbol") },
    { name: "Go to Line…", run: () => openPalette("line") },
    { name: "Search in Files", run: () => showPanel("search") },
    { name: "Find in Current File", run: () => openFind(S2.lastWord) },
    { name: "Go to Definition", run: () => gotoDefinition() },
    { name: "Find All References (Right Panel)", run: () => findReferences() },
    { name: withKeys("Show Call Trail: Callers / Callees ({Alt+Shift+H})"), run: () => showCalls() },
    { name: "Set Up Language Server…", run: () => openLspSetup() },
    { name: "Toggle Right Inspector (Symbols & References)", run: () => {
      if (document.body.classList.contains("right-hidden"))
        showRightInspector("refs");
      else
        hideRightInspector();
    } },
    { name: "Show File Symbols (Right Panel)", run: () => showRightInspector("symbols") },
    { name: "Reveal Active File in Explorer", run: () => {
      const d = doc_();
      if (d) {
        showPanel("files");
        revealFile(d.path);
      }
    } },
    { name: withKeys("Toggle Word Wrap ({Alt+Z})"), run: () => toggleWordWrap() },
    { name: withKeys("Toggle Line Numbers ({Alt+L})"), run: () => toggleLineNumbers() },
    { name: withKeys("Toggle Markdown Preview ({Alt+M})"), run: () => togglePreview() },
    { name: withKeys("Toggle Sidebar ({Mod+B})"), run: () => document.body.classList.toggle("side-hidden") },
    { name: "Select Theme…", run: () => openPalette("theme") },
    { name: "Next Theme", run: cycleTheme },
    { name: "Re-index Workspace", run: () => $("#btn-reindex").click() },
    { name: "Close Tab", run: () => {
      if (S2.active >= 0)
        closeTab(S2.active);
    } },
    { name: "Close All Tabs", run: () => {
      while (S2.tabs.length)
        closeTab(0);
    } },
    { name: withKeys("Reopen Closed Tab ({Alt+Shift+T})"), run: () => reopenClosedTab() },
    { name: "Keyboard Shortcuts", run: showHelp }
  ];
  var PAL_MODES = {
    file: { tag: "File", hint: "Type to fuzzy-match any file. Prefix : for a line, @ for a symbol, > for a command." },
    symbol: { tag: "Symbol", hint: "Symbols in the active file." },
    line: { tag: "Line", hint: "Enter a line number." },
    command: { tag: "Command", hint: "" },
    theme: { tag: "Theme", hint: "Arrows preview a theme. Enter keeps it, Esc restores the previous one." }
  };
  function openPalette(mode, seed) {
    pal = { mode, items: [], sel: 0, restoreTheme: mode === "theme" ? currentTheme() : null };
    overlay.hidden = false;
    palInput.value = seed !== undefined ? seed : { symbol: "@", line: ":", command: ">" }[mode] || "";
    $("#pal-mode").textContent = PAL_MODES[mode].tag;
    $("#pal-hint").textContent = PAL_MODES[mode].hint;
    palInput.focus();
    palInput.setSelectionRange(palInput.value.length, palInput.value.length);
    refreshPalette();
  }
  function closePalette() {
    overlay.hidden = true;
    if (pal && pal.restoreTheme)
      setTheme(pal.restoreTheme, false);
    pal = null;
  }
  var refreshPalette = debounce(async () => {
    if (!pal)
      return;
    let raw = palInput.value;
    let mode = pal.mode === "theme" ? "theme" : "file";
    if (mode === "theme") {} else if (raw.startsWith(">")) {
      mode = "command";
      raw = raw.slice(1);
    } else if (raw.startsWith("@")) {
      mode = "symbol";
      raw = raw.slice(1);
    } else if (raw.startsWith(":")) {
      mode = "line";
      raw = raw.slice(1);
    }
    pal.mode = mode;
    $("#pal-mode").textContent = PAL_MODES[mode].tag;
    $("#pal-hint").textContent = PAL_MODES[mode].hint;
    const q = raw.trim();
    if (mode === "line") {
      const d = doc_();
      const n = parseInt(q, 10);
      pal.items = d && n > 0 ? [{ kind: "line", n: Math.min(n, d.total), label: "Line " + Math.min(n, d.total), sub: d.path }] : [];
    } else if (mode === "command") {
      const lq = q.toLowerCase();
      pal.items = COMMANDS.filter((c) => c.name.toLowerCase().includes(lq)).map((c) => ({ kind: "cmd", cmd: c, label: c.name, sub: "" }));
    } else if (mode === "symbol") {
      const d = doc_();
      if (d && !d.outline) {
        try {
          d.outline = (await api("/api/outline", { path: d.path })).symbols || [];
        } catch {
          d.outline = [];
        }
      }
      const lq = q.toLowerCase();
      pal.items = (d && d.outline || []).filter((s) => !lq || s.name.toLowerCase().includes(lq)).slice(0, 400).map((s) => ({ kind: "sym", n: s.line, label: s.name, sub: s.kind, right: String(s.line) }));
    } else if (mode === "theme") {
      const lq = q.toLowerCase();
      pal.items = listThemes().filter((t) => (t.name + " " + t.id).toLowerCase().includes(lq)).map((t) => ({ kind: "theme", id: t.id, label: t.name, sub: t.scheme, right: t.id === pal.restoreTheme ? "current" : "" }));
    } else {
      let j;
      try {
        j = await api("/api/find", { q, limit: 120 });
      } catch {
        return;
      }
      pal.items = j.results.map((r) => {
        const cut = r.path.length - r.name.length;
        return {
          kind: "file",
          path: r.path,
          label: fuzzyHTML(r.path.slice(cut), (r.pos || []).filter((p) => p >= cut).map((p) => p - cut)),
          sub: fuzzyHTML(r.path.slice(0, Math.max(0, cut - 1)), (r.pos || []).filter((p) => p < cut)),
          raw: true
        };
      });
    }
    pal.sel = mode === "theme" ? Math.max(0, pal.items.findIndex((it) => it.id === currentTheme())) : 0;
    drawPalette();
  }, 40);
  function fuzzyHTML(text, pos) {
    if (!pos || !pos.length)
      return esc(text);
    const set = new Set(pos);
    let out = "", open = false;
    for (let i = 0;i < text.length; i++) {
      const hit = set.has(i);
      if (hit && !open) {
        out += "<b>";
        open = true;
      }
      if (!hit && open) {
        out += "</b>";
        open = false;
      }
      out += esc(text[i]);
    }
    return out + (open ? "</b>" : "");
  }
  function drawPalette() {
    if (!pal)
      return;
    if (!pal.items.length) {
      palList.innerHTML = '<div class="pi"><span class="pp">No matches</span></div>';
      return;
    }
    palList.innerHTML = pal.items.map((it, i) => '<div class="pi' + (i === pal.sel ? " sel" : "") + '" data-i="' + i + '">' + '<span class="pn">' + (it.raw ? it.label : esc(it.label)) + "</span>" + '<span class="pp">' + (it.raw ? it.sub : esc(it.sub || "")) + "</span>" + (it.right ? '<span class="pr">' + esc(it.right) + "</span>" : "") + "</div>").join("");
    const s = palList.children[pal.sel];
    if (s)
      s.scrollIntoView({ block: "nearest" });
    if (pal.mode === "theme")
      setTheme(pal.items[pal.sel].id, false);
  }
  function movePalette(delta) {
    if (!pal || !pal.items.length)
      return;
    pal.sel = (pal.sel + delta + pal.items.length) % pal.items.length;
    drawPalette();
  }
  function acceptPalette() {
    if (!pal || !pal.items.length)
      return;
    const it = pal.items[pal.sel];
    if (it.kind === "theme")
      pal.restoreTheme = null;
    closePalette();
    if (it.kind === "file")
      openFile(it.path);
    else if (it.kind === "sym" || it.kind === "line") {
      const d = doc_();
      if (!d)
        return;
      d.cur = it.n;
      centerLine(it.n);
      render();
      updateStatus();
      pushHistory(d.path, it.n);
    } else if (it.kind === "cmd")
      it.cmd.run();
    else if (it.kind === "theme")
      setTheme(it.id);
  }
  function initPalette() {
    palInput.addEventListener("input", refreshPalette);
    palInput.addEventListener("keydown", (e) => {
      if (e.key === "ArrowDown" || e.ctrlKey && e.key === "n") {
        e.preventDefault();
        movePalette(1);
      } else if (e.key === "ArrowUp" || e.ctrlKey && e.key === "p") {
        e.preventDefault();
        movePalette(-1);
      } else if (e.key === "Enter") {
        e.preventDefault();
        acceptPalette();
      } else if (e.key === "Escape") {
        e.preventDefault();
        closePalette();
      } else if (e.key === "Tab") {
        e.preventDefault();
        movePalette(e.shiftKey ? -1 : 1);
      }
    });
    palList.addEventListener("click", (e) => {
      const p = e.target.closest(".pi");
      if (p && p.dataset.i !== undefined) {
        pal.sel = +p.dataset.i;
        acceptPalette();
      }
    });
    overlay.addEventListener("mousedown", (e) => {
      if (e.target === overlay)
        closePalette();
    });
  }

  // web/src/main.js
  initRenderer();
  initTabs();
  initCursor();
  initHover();
  initSelectionBar();
  initTree();
  initSearch();
  initOutline();
  initPanels();
  initInspector();
  initCalls();
  initFind();
  initPalette();
  initShortcuts();
  initMarkdown();
  initDiff();
  initMetrics();
  initStatusFit();
  (async function boot() {
    try {
      initTheme();
      const wrapPref = localStorage.getItem("px0.wrap");
      S2.wrap = wrapPref !== null ? wrapPref === "true" : true;
      document.body.classList.toggle("word-wrap", S2.wrap);
      const linesPref = localStorage.getItem("px0.lineNumbers");
      S2.lineNumbers = linesPref !== null ? linesPref === "true" : true;
      document.body.classList.toggle("hide-lines", !S2.lineNumbers);
      const mdPref = localStorage.getItem("px0.mdPreview");
      S2.mdPreview = mdPref !== null ? mdPref === "true" : true;
      updateEditorOptionControls();
    } catch {}
    applyKeyLabels();
    measure();
    S2.meta = await api("/api/meta");
    if (S2.meta.metrics)
      updateMetricsDisplay(S2.meta.metrics);
    if (S2.meta.git) {
      const b = $("#btn-changed");
      if (b)
        b.hidden = false;
    }
    document.title = S2.meta.name + " - px0";
    $("#root-name").textContent = S2.meta.name;
    $("#root-name").title = S2.meta.root;
    if (S2.meta.version) {
      const emptyVerEl = $("#empty-ver");
      if (emptyVerEl)
        emptyVerEl.textContent = "v" + S2.meta.version;
    }
    updateStatus();
    await drawTree("", treeEl, 0);
    if (document.fonts && document.fonts.ready) {
      document.fonts.ready.then(() => {
        measure();
        layout();
        render();
      });
    }
    if (S2.meta && !S2.meta.ready) {
      const timer = setInterval(async () => {
        try {
          const m = await api("/api/meta");
          if (m.ready) {
            clearInterval(timer);
            S2.meta = m;
            updateStatus();
          }
        } catch {
          clearInterval(timer);
        }
      }, 150);
    }
  })();
})();
