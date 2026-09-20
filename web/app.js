(() => {
  // web/src/state.js
  var $ = (s, r = document) => r.querySelector(s);
  var $$ = (s, r = document) => [...r.querySelectorAll(s)];
  var esc2 = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
  var request = async (method, path, params, opts = {}) => {
    const u = new URL(path, location.origin);
    const fetchOpts = { method, ...opts };
    const isPost = method === "POST" || method === "PUT" || method === "PATCH";
    if (params) {
      const hasComplex = typeof params === "object" && params !== null && (Array.isArray(params) || Object.values(params).some((v) => typeof v === "object" && v !== null));
      if (isPost && (opts.json || hasComplex)) {
        fetchOpts.headers = { "Content-Type": "application/json", ...opts.headers || {} };
        fetchOpts.body = JSON.stringify(params);
      } else {
        for (const [k, v] of Object.entries(params)) {
          if (v !== undefined && v !== "")
            u.searchParams.set(k, v);
        }
      }
    }
    const r = await fetch(u, fetchOpts);
    const j = await r.json();
    if (j.error)
      throw Object.assign(new Error(j.error), { body: j });
    return j;
  };
  var api = (path, params, opts) => request("GET", path, params, opts);
  var apiPost = (path, params, opts) => request("POST", path, params, opts);
  var apiPostJson = (path, params, opts) => request("POST", path, params, { json: true, ...opts });
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
  var keyCaps = (combo) => keyParts(combo).map((k) => "<kbd>" + esc2(k) + "</kbd>").join("");
  var withKeys = (text) => text.replace(/\{([^}]+)\}/g, (_, combo) => keyLabel(combo));
  function applyKeyLabels(root = document) {
    for (const el of $$("[data-keys]", root))
      el.textContent = keyLabel(el.dataset.keys);
    for (const el of $$("[data-caps]", root))
      el.innerHTML = keyCaps(el.dataset.caps);
    for (const el of $$('[title*="{"]', root))
      el.title = withKeys(el.title);
  }
  var LH2 = 20;
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
    mdPreview: true,
    settings: null,
    agentTargets: []
  };
  var doc_ = () => S2.active >= 0 ? S2.tabs[S2.active] : null;

  // web/src/ui.js
  var vp = $("#viewport");
  var sizer = $("#sizer");
  var rowsEl = $("#rows");
  var editor = $("#editor");
  var toastEl = $("#toast");
  var toastTimer = 0;
  var toastLeaveTimer = 0;
  function showToast(accentText, text, duration = 2200) {
    if (!toastEl)
      return;
    clearTimeout(toastTimer);
    clearTimeout(toastLeaveTimer);
    toastEl.classList.remove("toast-hide");
    let iconHtml = "";
    if (accentText) {
      if (accentText === "✓") {
        iconHtml = '<span class="toast-icon toast-icon-ok"><svg viewBox="0 0 16 16" width="11" height="11" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3.5 8.5l3 3 6-6"/></svg></span>';
      } else if (accentText === "!") {
        iconHtml = '<span class="toast-icon toast-icon-warn"><svg viewBox="0 0 16 16" width="11" height="11" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="8" y1="4" x2="8" y2="9"/><circle cx="8" cy="12.5" r="0.6" fill="currentColor"/></svg></span>';
      } else {
        iconHtml = '<span class="toast-chip">' + esc2(accentText) + "</span>";
      }
    }
    toastEl.innerHTML = iconHtml + '<span class="toast-msg">' + esc2(text) + "</span>";
    toastEl.hidden = false;
    toastTimer = setTimeout(() => {
      toastEl.classList.add("toast-hide");
      toastLeaveTimer = setTimeout(() => {
        toastEl.hidden = true;
        toastEl.classList.remove("toast-hide");
      }, 180);
    }, duration);
  }
  async function copyToClipboard(text, notify = "Copied") {
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
    const gutter = digits * S2.chW + 30;
    const w = S2.wrap ? vp.clientWidth : Math.max(vp.clientWidth, gutter + (d.maxCols + 4) * S2.chW);
    sizer.style.height = d.total * LH2 + Math.max(120, vp.clientHeight * 0.5) + "px";
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
    layout();
    render();
  }
  function applyEditorTypography(fontSize, fontFamily, lineHeight, tabSize) {
    if (fontSize)
      document.documentElement.style.setProperty("--fs", fontSize + "px");
    if (fontFamily)
      document.documentElement.style.setProperty("--mono", fontFamily);
    if (lineHeight) {
      document.documentElement.style.setProperty("--lh", lineHeight + "px");
    } else if (fontSize) {
      document.documentElement.style.setProperty("--lh", Math.round(fontSize * 1.5) + "px");
    }
    if (tabSize)
      document.documentElement.style.setProperty("--tab-size", tabSize);
    measure();
    layout();
    render();
  }
  function updateEditorOptionControls() {
    const wrapBtn = $('[data-action="wrap"]');
    if (wrapBtn)
      wrapBtn.classList.toggle("active", !!S2.wrap);
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
    const first = Math.max(0, Math.floor(top / LH2) - OVERSCAN);
    const count = Math.ceil(vp.clientHeight / LH2) + OVERSCAN * 2;
    const last = Math.min(d.total, first + count);
    ensureChunks(d, first, last);
    let html = "";
    const gut = d.gutter || null;
    const agentRanges = (S2.agentTargets || []).filter((t) => t.path === d.path);
    for (let i = first;i < last; i++) {
      const n = i + 1;
      const body = d.lines[i];
      let rc = "row", gc = "g";
      if (n === d.cur)
        rc += " cur";
      if (agentRanges.some((r) => n >= r.l1 && n <= r.l2))
        rc += " agent-sel";
      if (agentRanges.some((r) => n === r.l1))
        rc += " agent-anchor";
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
    rowsEl.style.transform = "translateY(" + first * LH2 + "px)";
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
      y = S2.wrap ? rect.top - (LH2 - rect.height) / 2 : row.getBoundingClientRect().top;
    } else {
      const cr = code.getBoundingClientRect();
      x = cr.left + parseFloat(getComputedStyle(code).paddingLeft || "0");
      y = cr.top;
    }
    const g = $(".g", row);
    if (g && x < g.getBoundingClientRect().right - 1) {
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
      const row2 = rowsEl.children[off] || rowsEl.lastElementChild;
      if (!row2)
        return null;
      const atEnd = !rowsEl.children[off];
      return { line: +row2.dataset.l, col: atEnd ? $(".c", row2).textContent.length : 0 };
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
      return (d.outlineSource ? '<div class="hint"><span class="src">' + esc2(d.outlineSource) + "</span> · " + items.length + " symbols</div>" : "") + items.map((s) => '<div class="sym" data-n="' + s.line + '" style="padding-left:' + (10 + Math.min(s.indent - base, 16) * 5) + 'px" title="Jump to ' + esc2(s.name) + " at line " + s.line + '">' + '<span class="kd" data-k="' + esc2(s.kind) + '">' + esc2(kindLabel(s.kind)) + "</span>" + '<span class="sn">' + esc2(s.name) + '</span><span class="sl">' + s.line + "</span></div>").join("");
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
        return '<div class="tw"><div class="tr dir' + ig + dc + '" data-dir="' + esc2(c.path) + '" style="padding-left:' + pad + 'px" title="Folder: ' + esc2(c.path) + note + '">' + '<span class="ar"></span><span class="nm">' + esc2(c.name) + "</span></div>" + '<div class="kids" data-kids="' + esc2(c.path) + '"></div></div>';
      }
      const g = GIT_STATUS[c.status];
      const gc = g ? " dirty " + g[0] : "";
      const badge = g ? '<span class="gs" title="git: ' + g[1] + '">' + esc2(c.status) + "</span>" : "";
      return '<div class="tr file' + ig + gc + '" data-file="' + esc2(c.path) + '" style="padding-left:' + (pad + 12) + 'px" title="Open ' + esc2(c.path) + note + '">' + '<span class="ic" data-t="' + fileKind(c.name) + '"></span><span class="nm">' + esc2(c.name) + "</span>" + badge + "</div>";
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
  async function refreshTree() {
    await drawTree("", treeEl, 0);
    const dirs = Array.from(openDirs).sort((a, b) => a.split("/").length - b.split("/").length);
    for (const path of dirs) {
      const dirRow = treeEl.querySelector('[data-dir="' + CSS.escape(path) + '"]');
      const kids = treeEl.querySelector('[data-kids="' + CSS.escape(path) + '"]');
      if (kids && dirRow) {
        dirRow.classList.add("open");
        kids.classList.add("open");
        kids.dataset.loaded = "1";
        await drawTree(path, kids, path.split("/").length);
      } else {
        openDirs.delete(path);
      }
    }
    if (treeEl.classList.contains("changed-only")) {
      await expandDirtyDirs();
    }
  }
  function restoreOpenDirs(dirs) {
    if (Array.isArray(dirs)) {
      for (const d of dirs) {
        if (typeof d === "string")
          openDirs.add(d);
      }
    }
  }
  async function revealDir(dir) {
    const parts = dir.split("/");
    for (let i = 0;i < parts.length; i++) {
      const p = parts.slice(0, i + 1).join("/");
      const row = treeEl.querySelector('[data-dir="' + CSS.escape(p) + '"]');
      if (!row)
        break;
      if (!row.classList.contains("open")) {
        row.classList.add("open");
        const kids = treeEl.querySelector('[data-kids="' + CSS.escape(p) + '"]');
        if (kids) {
          kids.classList.add("open");
          openDirs.add(p);
          kids.dataset.loaded = "1";
          await drawTree(p, kids, p.split("/").length);
        }
      }
    }
    const last = treeEl.querySelector('[data-dir="' + CSS.escape(dir) + '"]');
    if (last)
      last.scrollIntoView({ block: "center" });
    try {
      sessionStorage.setItem("px0.openDirs", JSON.stringify(Array.from(openDirs)));
    } catch {}
  }
  async function revealFile(path) {
    const idx = path.lastIndexOf("/");
    if (idx > 0)
      await revealDir(path.slice(0, idx));
    const row = treeEl.querySelector('[data-file="' + CSS.escape(path) + '"]');
    if (row) {
      $$(".tr.sel", treeEl).forEach((x) => x.classList.remove("sel"));
      row.classList.add("sel");
      row.scrollIntoView({ block: "center" });
    }
  }
  async function expandDirtyDirs(container = treeEl) {
    const dirtyRows = Array.from(container.querySelectorAll(".tr.dir.dirty:not(.open)"));
    for (const dirRow of dirtyRows) {
      const path = dirRow.dataset.dir;
      const kids = container.querySelector('[data-kids="' + CSS.escape(path) + '"]');
      if (kids) {
        dirRow.classList.add("open");
        kids.classList.add("open");
        kids.dataset.loaded = "1";
        openDirs.add(path);
        await drawTree(path, kids, path.split("/").length);
        await expandDirtyDirs(kids);
      }
    }
    try {
      sessionStorage.setItem("px0.openDirs", JSON.stringify(Array.from(openDirs)));
    } catch {}
  }
  async function patchTreeGitStatus(statuses = {}, dirtyDirs = {}) {
    const dirRows = treeEl.querySelectorAll(".tr.dir");
    for (const dirRow of dirRows) {
      const p = dirRow.dataset.dir;
      dirRow.classList.toggle("dirty", !!dirtyDirs[p]);
    }
    const dirtyFiles = treeEl.querySelectorAll(".tr.file.dirty");
    for (const fileRow of dirtyFiles) {
      const p = fileRow.dataset.file;
      if (!statuses[p]) {
        fileRow.classList.remove("dirty", "git-M", "git-A", "git-D", "git-untracked", "git-R");
        const badge = fileRow.querySelector(".gs");
        if (badge)
          badge.remove();
      }
    }
    for (const [p, code] of Object.entries(statuses)) {
      const fileRow = treeEl.querySelector('[data-file="' + CSS.escape(p) + '"]');
      if (!fileRow)
        continue;
      const g = GIT_STATUS[code];
      fileRow.classList.remove("git-M", "git-A", "git-D", "git-untracked", "git-R");
      if (g) {
        fileRow.classList.add("dirty", g[0]);
        let badge = fileRow.querySelector(".gs");
        if (!badge) {
          badge = document.createElement("span");
          badge.className = "gs";
          fileRow.appendChild(badge);
        }
        badge.title = "git: " + g[1];
        badge.textContent = code;
      } else {
        fileRow.classList.remove("dirty");
        const badge = fileRow.querySelector(".gs");
        if (badge)
          badge.remove();
      }
    }
    if (treeEl.classList.contains("changed-only")) {
      await expandDirtyDirs();
    }
  }
  function updateSidebarToggleState() {
    const btnChanged = $("#btn-changed");
    const hasGitChanges = !!(S2.meta?.git && S2.meta.gitChanges > 0);
    if (btnChanged) {
      btnChanged.disabled = !hasGitChanges;
      btnChanged.classList.toggle("disabled", !hasGitChanges);
      if (!S2.meta?.git) {
        btnChanged.title = "Git not available in workspace";
      } else if (!hasGitChanges) {
        btnChanged.title = "There are no git modified files.";
      } else {
        btnChanged.title = "Git changes (show changed files only)";
      }
    }
  }
  async function setSidebarMode(mode) {
    const btnChanged = $("#btn-changed");
    const btnFiles = $("#btn-files");
    updateSidebarToggleState();
    const hasGitChanges = !!(S2.meta?.git && S2.meta.gitChanges > 0);
    if (mode === "git" && hasGitChanges) {
      treeEl.classList.add("changed-only");
      btnChanged?.classList.add("active");
      btnFiles?.classList.remove("active");
      await expandDirtyDirs();
    } else {
      treeEl.classList.remove("changed-only");
      btnFiles?.classList.add("active");
      btnChanged?.classList.remove("active");
    }
  }
  function initTree() {
    updateSidebarToggleState();
    $("#btn-changed")?.addEventListener("click", async () => {
      const hasGitChanges = !!(S2.meta?.git && S2.meta.gitChanges > 0);
      if (!hasGitChanges)
        return;
      await setSidebarMode("git");
    });
    $("#btn-files")?.addEventListener("click", () => {
      setSidebarMode("files");
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
          kids.dataset.loaded = "1";
          await drawTree(path, kids, path.split("/").length);
        } else
          openDirs.delete(path);
        try {
          sessionStorage.setItem("px0.openDirs", JSON.stringify(Array.from(openDirs)));
        } catch {}
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
  async function reindexWorkspace() {
    try {
      const j = await api("/api/reindex");
      S2.meta.files = j.files;
      S2.meta.indexMs = j.indexMs;
      if (j.gitChanges !== undefined)
        S2.meta.gitChanges = j.gitChanges;
      if (j.gitFiles !== undefined)
        S2.meta.gitFiles = j.gitFiles;
      const hasGitChanges = !!(S2.meta?.git && S2.meta.gitChanges > 0);
      if (hasGitChanges) {
        await setSidebarMode("git");
      } else {
        setSidebarMode("files");
      }
      await refreshTree();
      await reloadOpenTabs();
      updateStatus();
      showToast("✓", "Workspace refreshed");
    } catch (e) {
      showToast("!", "Refresh failed: " + e.message);
    }
  }
  function initPanels() {
    $("#btn-reindex").addEventListener("click", reindexWorkspace);
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
    const y = (h.line - 1) * LH2;
    if (y < vp.scrollTop + LH2 * 2 || y > vp.scrollTop + vp.clientHeight - LH2 * 3)
      centerLine(h.line);
    $("#find-count").textContent = S2.find.active + 1 + " / " + n;
    render();
    updateStatus();
  }
  function findNextMatch(delta = 1) {
    if (!S2.find || !S2.find.hits || !S2.find.hits.length) {
      if (findInput.value) {
        runFind();
        return;
      }
      return;
    }
    jumpToHit(S2.find.active + delta);
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
  var searchAbort = null;
  function cancelSearch() {
    if (searchAbort) {
      searchAbort.abort();
      searchAbort = null;
    }
  }
  var runSearch = debounce(async () => {
    const qEl = $("#q");
    if (!qEl || !resultsEl)
      return;
    const q = qEl.value;
    if (!q.trim()) {
      cancelSearch();
      resultsEl.innerHTML = "";
      return;
    }
    cancelSearch();
    const controller = new AbortController;
    searchAbort = controller;
    resultsEl.innerHTML = '<div class="hint">searching…</div>';
    const params = {
      q,
      glob: $("#glob")?.value || "",
      case: $("#o-case")?.classList.contains("on") ? 1 : "",
      word: $("#o-word")?.classList.contains("on") ? 1 : "",
      re: $("#o-re")?.classList.contains("on") ? 1 : ""
    };
    try {
      const j = await api("/api/search", params, { signal: controller.signal });
      if (searchAbort === controller) {
        searchAbort = null;
        renderResults(j);
      }
    } catch (e) {
      if (e.name === "AbortError")
        return;
      if (searchAbort === controller) {
        searchAbort = null;
        resultsEl.innerHTML = '<div class="hint">' + esc2(e.message) + "</div>";
      }
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
    let html = '<div class="hint">' + esc2(head) + "</div>";
    for (const f of j.results) {
      html += '<div class="rfile" data-toggle="' + esc2(f.path) + '" title="' + esc2(f.path) + '">' + '<span class="ar">&#9660;</span>' + (f.ext ? '<span class="ext">ext</span>' : "") + '<span class="fp">' + esc2(displayPath(f.path)) + "</span>" + '<span class="cnt">' + f.matches.length + "</span></div>" + '<div data-group="' + esc2(f.path) + '">';
      for (const m of f.matches) {
        html += '<div class="rline" data-p="' + esc2(f.path) + '" data-n="' + m.line + '" title="Jump to ' + esc2(f.path) + ":" + m.line + '">' + '<span class="rn">' + m.line + '</span><span class="rt">' + esc2(m.pre) + "<mark>" + esc2(m.mid) + "</mark>" + esc2(m.post) + "</span></div>";
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
    cancelSearch();
    document.body.classList.add("right-hidden");
    layout();
    render();
  }
  function setRightInspectorTab(tab) {
    if (tab !== "search")
      cancelSearch();
    $$(".inspector-tab").forEach((b) => b.classList.toggle("active", b.dataset.itab === tab));
    $("#pane-right-refs")?.classList.toggle("active", tab === "refs");
    $("#pane-right-symbols")?.classList.toggle("active", tab === "symbols");
    $("#pane-right-calls")?.classList.toggle("active", tab === "calls");
    $("#pane-right-search")?.classList.toggle("active", tab === "search");
    if (tab === "symbols") {
      loadOutline();
      $("#right-symbols-filter")?.focus();
    }
    if (tab === "search")
      $("#q")?.focus();
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
      listEl.innerHTML = '<div class="hint">No references found for "<b>' + esc2(word) + '</b>".</div>';
      return;
    }
    const grouped = groupHits(hits);
    const head = hits.length + " reference" + (hits.length === 1 ? "" : "s") + (server ? " · " + esc2(server) : " · text search");
    let html = '<div class="hint">' + head + "</div>";
    for (const f of grouped) {
      html += '<div class="rfile" data-toggle="r-' + esc2(f.path) + '" title="' + esc2(f.path) + '">' + '<span class="ar">&#9660;</span>' + '<span class="fp">' + esc2(displayPath(f.path)) + "</span>" + '<span class="cnt">' + f.matches.length + "</span></div>" + '<div data-group="r-' + esc2(f.path) + '">';
      for (const m of f.matches) {
        html += '<div class="rline" data-p="' + esc2(f.path) + '" data-n="' + m.line + '" title="Jump to ' + esc2(f.path) + ":" + m.line + '">' + '<span class="rn">' + m.line + '</span><span class="rt">' + esc2(m.pre) + "<mark>" + esc2(m.mid || word) + "</mark>" + esc2(m.post) + "</span></div>";
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
      listEl.innerHTML = '<div class="hint">Finding references for "' + esc2(at.word) + '"…</div>';
    if (canAskServer(at)) {
      setStatusNote("references to " + at.word + "…", 8000);
      try {
        const j = await lspCall("refs", at, 30000);
        updateStatus();
        if (j && j.hits && j.hits.length) {
          setStatusNote("");
          renderRightResults(at.word, j.hits, j.server, true);
          return;
        }
      } catch {
        updateStatus();
      }
    }
    setStatusNote("searching references to " + at.word + "…", 8000);
    try {
      const j = await api("/api/search", { q: at.word, word: true, case: true });
      updateStatus();
      setStatusNote("");
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
      setStatusNote("");
      if (listEl)
        listEl.innerHTML = '<div class="hint">Search error: ' + esc2(err.message) + "</div>";
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
      setStatusNote("definition of " + at.word + "…", 8000);
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
    setStatusNote("searching for " + at.word + "…", 8000);
    let rx;
    try {
      rx = await api("/api/def", { sym: at.word, path: d.path });
    } catch (e) {
      setStatusNote(e.message, 4000);
      return;
    }
    updateStatus();
    if (rx.lsp)
      setLspState(rx.lsp);
    if (!rx.defs || !rx.defs.length) {
      setStatusNote("");
      showRightInspector("search");
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
      setStatusNote(server ? server + " · " + h.path + ":" + h.line : h.path + ":" + h.line, 4000);
      return;
    }
    setStatusNote("");
    showHits(word, hits, server, noun, refCount);
  }
  function showHits(word, hits, server, noun, refCount) {
    setStatusNote("");
    const n = hits.length;
    let head = n + " " + noun + (n === 1 ? "" : "s") + ' of "' + word + '"';
    head += server ? "  ·  " + server : "  ·  text match, no language server";
    if (refCount)
      head += "  ·  " + refCount + " other references";
    renderResults({ results: groupHits(hits), files: 0, total: n, header: head, exact: !!server });
    showRightInspector("search");
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
      const r2 = document.caretRangeFromPoint(x, y);
      if (!r2)
        return null;
      node = r2.startContainer;
      off = r2.startOffset;
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
    const gw = g ? g.offsetWidth : 0;
    if (x < vp.scrollLeft + gw + 8)
      vp.scrollLeft = Math.max(0, x - gw - 40);
    else if (x > vp.scrollLeft + vp.clientWidth - 24)
      vp.scrollLeft = x - vp.clientWidth + 60;
  }
  function updateDomSelection() {
    const d = doc_();
    if (!d)
      return;
    const sel = window.getSelection();
    if (!sel)
      return;
    if (!d.selAnchor) {
      if (sel.rangeCount && !sel.isCollapsed && vp.contains(sel.getRangeAt(0).commonAncestorContainer)) {
        sel.removeAllRanges();
      }
      return;
    }
    const pa = toPoint(d.selAnchor);
    const headCol = d.col === Infinity ? rowFor(d.cur) ? $(".c", rowFor(d.cur)).textContent.length : 0 : d.col || 0;
    const pf = toPoint({ line: d.cur, col: headCol });
    if (pa && pf) {
      sel.setBaseAndExtent(pa[0], pa[1], pf[0], pf[1]);
    }
  }
  function ensureAnchor(d) {
    if (!d.selAnchor) {
      const col = d.col === Infinity ? rowFor(d.cur) ? $(".c", rowFor(d.cur)).textContent.length : 0 : d.col || 0;
      d.selAnchor = { line: d.cur, col };
    }
  }
  function clearSelection(d) {
    if (d)
      d.selAnchor = null;
    const sel = window.getSelection();
    if (sel && sel.rangeCount && !sel.isCollapsed && vp.contains(sel.getRangeAt(0).commonAncestorContainer)) {
      sel.removeAllRanges();
    }
  }
  function moveCol(delta, shift = false) {
    const d = doc_();
    if (!d)
      return;
    if (shift)
      ensureAnchor(d);
    else
      d.selAnchor = null;
    const row = rowFor(d.cur);
    const len = row ? $(".c", row).textContent.length : 0;
    const col = Math.min(d.col || 0, len) + delta;
    if (col < 0) {
      if (d.cur > 1) {
        d.col = Infinity;
        moveCursor(-1, shift);
      } else
        updateDomSelection();
      return;
    }
    if (col > len) {
      if (d.cur < d.total) {
        d.col = 0;
        moveCursor(1, shift);
      } else
        updateDomSelection();
      return;
    }
    d.col = col;
    revealCaretX(placeCaret());
    updateDomSelection();
  }
  function moveWord(delta, shift = false) {
    const d = doc_();
    if (!d)
      return;
    if (shift)
      ensureAnchor(d);
    else
      d.selAnchor = null;
    const row = rowFor(d.cur);
    const text = row ? $(".c", row).textContent : "";
    const len = text.length;
    let col = Math.min(d.col === Infinity ? len : d.col || 0, len);
    if (delta < 0) {
      if (col === 0) {
        if (d.cur > 1) {
          d.col = Infinity;
          moveCursor(-1, shift);
        }
        return;
      }
      col--;
      while (col > 0 && /\s/.test(text[col]))
        col--;
      if (WORD.test(text[col])) {
        while (col > 0 && WORD.test(text[col - 1]))
          col--;
      } else {
        while (col > 0 && !WORD.test(text[col - 1]) && !/\s/.test(text[col - 1]))
          col--;
      }
    } else {
      if (col >= len) {
        if (d.cur < d.total) {
          d.col = 0;
          moveCursor(1, shift);
        }
        return;
      }
      if (WORD.test(text[col])) {
        while (col < len && WORD.test(text[col]))
          col++;
      } else if (!/\s/.test(text[col])) {
        while (col < len && !WORD.test(text[col]) && !/\s/.test(text[col]))
          col++;
      }
      while (col < len && /\s/.test(text[col]))
        col++;
    }
    d.col = col;
    revealCaretX(placeCaret());
    updateDomSelection();
  }
  function caretToEdge(end, shift = false) {
    const d = doc_();
    if (!d)
      return;
    if (shift)
      ensureAnchor(d);
    else
      d.selAnchor = null;
    d.col = end ? Infinity : 0;
    revealCaretX(placeCaret());
    updateDomSelection();
  }
  function moveCursor(delta, shift = false) {
    const d = doc_();
    if (!d)
      return;
    if (shift)
      ensureAnchor(d);
    else
      d.selAnchor = null;
    d.cur = Math.max(1, Math.min(d.total, d.cur + delta));
    const y = (d.cur - 1) * LH2;
    if (y < vp.scrollTop)
      vp.scrollTop = y - LH2;
    else if (y > vp.scrollTop + vp.clientHeight - LH2 * 2)
      vp.scrollTop = y - vp.clientHeight + LH2 * 3;
    render();
    updateStatus();
    updateDomSelection();
  }
  function initCursor() {
    vp.addEventListener("mousedown", (e) => {
      if (e.button !== 0)
        return;
      const row = e.target.closest(".row");
      if (!row)
        return;
      const d = doc_();
      if (!d)
        return;
      const targetLine = +row.dataset.l;
      const p = colAtPoint(e.clientX, e.clientY);
      const targetCol = p && p.line === targetLine ? p.col : 0;
      if (e.shiftKey) {
        ensureAnchor(d);
        d.cur = targetLine;
        d.col = targetCol;
        placeCaret();
        updateStatus();
        updateDomSelection();
        for (const r of rowsEl.children)
          r.classList.toggle("cur", +r.dataset.l === d.cur);
        return;
      }
      d.selAnchor = null;
      d.cur = targetLine;
      d.col = targetCol;
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
        el.innerHTML = hintHtml("Could not check language servers: " + esc2(e.message));
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
      el.innerHTML = hintHtml("Could not start the language server: " + esc2(e.message));
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
      return hintHtml("px0 knows no language server for <b>" + esc2(ext) + "</b> files, so call trails are not available here.");
    }
    const offer = s.servers.filter((v) => v.options.length || v.job);
    const running = s.servers.some((v) => v.job && v.job.running);
    let html = '<div class="lsp-setup">';
    if (s.state === "failed") {
      html += "<p><b>" + esc2(s.server) + '</b> did not start: <span class="lsp-reason">' + esc2(s.reason || "unknown error") + "</span></p>" + '<div class="lsp-row"><button class="lsp-btn" data-start title="Retry starting language server">Retry</button></div>';
      if (offer.length)
        html += "<p>If it is broken or incomplete, install it again:</p>";
    } else {
      html += "<p>Call trails, hover and precise references for " + esc2(s.lang) + " need a language server, and none is installed.</p>";
    }
    for (const v of offer) {
      html += '<div class="lsp-server"><div class="lsp-name">' + esc2(v.name) + "</div>";
      v.options.forEach((o, i) => {
        html += '<div class="lsp-opt"><code>' + esc2(o.cmd) + '</code><span class="lsp-acts">';
        if (!o.auto)
          html += '<span class="lsp-need">run in a terminal</span>';
        else if (!o.hasTool)
          html += '<span class="lsp-need">needs ' + esc2(o.tool) + "</span>";
        else
          html += '<button class="lsp-btn primary" data-install="' + esc2(v.name) + '" data-option="' + i + '"' + (running ? " disabled" : "") + ' title="Install language server">Install</button>';
        html += '<button class="lsp-btn" data-copy="' + esc2(o.cmd) + '" title="Copy command to clipboard">Copy</button></span></div>';
      });
      if (v.job)
        html += job(v.job);
      html += "</div>";
    }
    if (!offer.length) {
      html += "<p>px0 has no installer for this one. Install " + s.servers.map((v) => "<b>" + esc2(v.name) + "</b>").join(" or ") + " and make sure it is on PATH.</p>";
    }
    html += '<div class="lsp-row"><span>Installed one yourself?</span><button class="lsp-btn" data-start title="Detect and start language server">Detect and start</button></div></div>';
    return html;
  }
  function job(j) {
    const tail = (j.log || "").trimEnd().split(`
`).slice(-12).join(`
`);
    const log = tail ? "<pre>" + esc2(tail) + "</pre>" : "";
    if (j.running)
      return '<div class="lsp-job">Installing with <code>' + esc2(j.cmd) + "</code>…" + log + "</div>";
    if (j.error)
      return '<div class="lsp-job err">Install failed: ' + esc2(j.error) + log + "</div>";
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
  var callSeq = 0;
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
      hint("Click a function name in the editor, then press <b>" + esc2(keyLabel("Alt+Shift+H")) + "</b>.");
      return;
    }
    const my = ++callSeq;
    T = null;
    $("#right-calls-target").textContent = at.word;
    hint('Tracing calls for "' + esc2(at.word) + '"…');
    setStatusNote("call trail for " + at.word + "…", 8000);
    let j;
    try {
      j = await api("/api/lsp/calls", { path: d.path, line: at.line, col: at.col, wait: S2.lsp.state === "ready" ? 1e4 : 30000 });
    } catch (e) {
      if (my === callSeq) {
        updateStatus();
        setStatusNote("");
        hint('Could not trace "' + esc2(at.word) + '": ' + esc2(explain(e.message)));
      }
      return;
    }
    if (my !== callSeq)
      return;
    setLspState(j);
    updateStatus();
    setStatusNote("");
    if (!j.nodes || !j.nodes.length) {
      hint('"' + esc2(at.word) + '" is not a function ' + esc2(j.server || "the language server") + " can trace.");
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
      html += '<div class="sym cnode" data-i="' + i + '" style="padding-left:' + (6 + depth * 14) + 'px" title="' + esc2(tip) + '">' + '<span class="car' + (node.cycle ? " cyc" : "") + '">' + arrow + "</span>" + '<span class="kd" data-k="' + esc2(n.kind) + '">' + esc2(n.kind) + "</span>" + '<span class="sn">' + esc2(n.name) + "</span>" + '<span class="sl">' + esc2(base(t.path)) + ":" + t.line + calls + "</span></div>";
      const pad = 'style="padding-left:' + (26 + (depth + 1) * 14) + 'px"';
      if (node.err)
        html += '<div class="cnone" ' + pad + ">" + esc2(node.err) + "</div>";
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
    if (S2.settings && (S2.settings["lsp.hover.enabled"] === false || S2.settings["lsp.enabled"] === false))
      return;
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
    hovercard.innerHTML = (j.signature ? '<div class="sig">' + j.signature + "</div>" : "") + (j.doc ? '<div class="doc">' + esc2(j.doc) + "</div>" : "") + '<div class="actions">' + '<button id="hc-copy-ref" title="Copy file and line reference">Copy Ref</button>' + '<button id="hc-copy-ai" title="Copy snippet with file path and line numbers">Copy with Context</button>' + '<button id="hc-find-refs" title="Find all usages across codebase">Usages</button>' + '<button id="hc-calls" title="' + withKeys("Trace callers and callees ({Alt+Shift+H})") + '">Calls</button>' + "</div>" + '<div class="foot"><b>' + esc2(j.server || "lsp") + "</b>" + "<span>" + withKeys("{Mod+Click} definition") + "</span>" + "<span>" + withKeys("{Shift+F12} references") + "</span></div>";
    const btnRef = hovercard.querySelector("#hc-copy-ref");
    const btnAi = hovercard.querySelector("#hc-copy-ai");
    const btnRefs = hovercard.querySelector("#hc-find-refs");
    if (btnRef)
      btnRef.onclick = (e) => {
        e.stopPropagation();
        copyToClipboard(refPath, "Copied");
      };
    if (btnAi)
      btnAi.onclick = (e) => {
        e.stopPropagation();
        const lineText = d.lines[at.line - 1] || at.word || "";
        const ext = d.path.split(".").pop() || "";
        const lineStr = "line " + at.line;
        const text = "@" + d.path + " " + lineStr + "\n```" + ext + `
` + lineText + "\n```";
        copyToClipboard(text, "Copied");
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
    const target2 = d.mdAnchor && mdFindAnchor(d.mdAnchor);
    if (target2)
      mdScrollTo(target2);
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
    const base2 = MD_ORIGIN + "/" + dir.split("/").map(encodeURIComponent).join("/");
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
        mdSetImage(el, mdURL(attrs.src || ""), base2);
      if (tag === "a" && attrs.href)
        mdSetLink(el, mdURL(attrs.href), base2);
    }
    const frag = document.createDocumentFragment();
    while (body.firstChild)
      frag.appendChild(document.adoptNode(body.firstChild));
    return frag;
  }
  function mdLocal(ref, base2) {
    let u;
    try {
      u = new URL(ref, base2);
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
  function mdSetImage(img, src, base2) {
    img.setAttribute("loading", "lazy");
    img.setAttribute("decoding", "async");
    img.classList.add("md-zoomable");
    const m = MD_SCHEME.exec(src);
    if (m) {
      if (/^https?$/i.test(m[1]) || /^data:image\//i.test(src)) {
        img.setAttribute("src", src);
        img.dataset.origSrc = src;
      }
    } else if (src.startsWith("//")) {
      img.setAttribute("src", src);
      img.dataset.origSrc = src;
    } else if (src) {
      const t = mdLocal(src, base2);
      if (t) {
        img.setAttribute("src", "/api/raw?path=" + encodeURIComponent(t.path));
        img.dataset.rawPath = t.path;
        img.dataset.origSrc = src;
      }
    }
  }
  function mdSetLink(a, href, base2) {
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
    const t = mdLocal(href, base2);
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
      const wrap2 = document.createElement("div");
      wrap2.className = "md-pre";
      if (pre.dataset.lang)
        wrap2.dataset.lang = pre.dataset.lang;
      pre.replaceWith(wrap2);
      const copy = document.createElement("button");
      copy.className = "md-copy";
      copy.title = "Copy code";
      copy.setAttribute("aria-label", "Copy code");
      copy.innerHTML = '<svg viewBox="0 0 16 16" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"><rect x="5.5" y="5.5" width="8" height="8" rx="1.5"/><path d="M10.5 3.5V3a1.5 1.5 0 0 0-1.5-1.5H4A1.5 1.5 0 0 0 2.5 3v5A1.5 1.5 0 0 0 4 9.5h.5"/></svg>';
      wrap2.append(pre, copy);
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
    vp.scrollTop = (line - 1) * LH2;
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
    marks.forEach((m2, k) => m2.classList.toggle("on", k === i));
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
      const img = e.target.closest("img.md-zoomable");
      const a = e.target.closest("a");
      if (img && !a && e.button === 0 && !e[MOD] && !e.shiftKey) {
        e.preventDefault();
        openLightbox(img);
        return;
      }
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
    mdArticle.addEventListener("error", (e) => {
      if (e.target && e.target.localName === "img") {
        const img = e.target;
        const path = img.dataset.rawPath || img.dataset.origSrc || img.getAttribute("src") || "image";
        const fallback = document.createElement("div");
        fallback.className = "md-img-broken";
        fallback.innerHTML = '<svg viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="2" y="2" width="12" height="12" rx="2"/><path d="M2 14l5-5 3 3 4-4"/><circle cx="5.5" cy="5.5" r="1.5"/><line x1="2" y1="2" x2="14" y2="14"/></svg><span>Image not found: ' + esc(path) + "</span>";
        img.replaceWith(fallback);
      }
    }, true);
    const lb = $("#img-lightbox");
    if (lb) {
      lb.addEventListener("click", (e) => {
        if (e.target.closest(".lightbox-close") || e.target.classList.contains("lightbox-backdrop")) {
          lb.hidden = true;
        }
      });
    }
  }
  function openLightbox(img) {
    const lb = $("#img-lightbox");
    if (!lb)
      return;
    const lbImg = $("#lb-img");
    const lbTitle = $("#lb-title");
    const lbMeta = $("#lb-meta");
    const lbOpenTab = $("#lb-open-tab");
    const lbCopyPath = $("#lb-copy-path");
    const src = img.getAttribute("src");
    const rawPath = img.dataset.rawPath || "";
    const alt = img.getAttribute("alt") || "";
    const displayTitle = rawPath || alt || src.split("/").pop() || "Image Preview";
    lbImg.src = src;
    lbTitle.textContent = displayTitle;
    lbTitle.title = displayTitle;
    const updateMeta = () => {
      if (lbImg.naturalWidth) {
        lbMeta.textContent = `${lbImg.naturalWidth} × ${lbImg.naturalHeight} px`;
      } else {
        lbMeta.textContent = "";
      }
    };
    if (lbImg.complete && lbImg.naturalWidth)
      updateMeta();
    else
      lbImg.onload = updateMeta;
    if (rawPath) {
      lbOpenTab.hidden = false;
      lbOpenTab.onclick = () => {
        lb.hidden = true;
        openFile(rawPath);
      };
      lbCopyPath.hidden = false;
      lbCopyPath.onclick = () => {
        copyToClipboard(rawPath, "Copied image path");
      };
    } else {
      lbOpenTab.hidden = true;
      lbCopyPath.onclick = () => {
        copyToClipboard(src, "Copied image URL");
      };
    }
    lb.hidden = false;
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
  function syncDiffView(force = false) {
    const d = doc_();
    const want = d && d.diffMode ? d : null;
    if (force && want) {
      want.diffText = undefined;
      want.diffHunks = undefined;
    }
    if (want !== shown || force) {
      shown = want;
      diffview.hidden = !want;
      if (want)
        drawDiff(want, force);
      else
        diffContent.replaceChildren();
    } else if (want && want.diffHunks !== undefined) {
      renderDiff(want);
    }
  }
  function diffScrollTop() {
    return diffview.hidden ? 0 : diffview.scrollTop;
  }
  async function toggleDiff() {
    if (!S2.meta?.git)
      return;
    const d = doc_();
    if (!d)
      return;
    if (!d.diffMode && !d.diffAvailable) {
      setStatusNote("No diff — clean file or not a git repo", 4000);
      return;
    }
    setDiffMode(d.diffMode ? "source" : layoutPref() || "split");
  }
  async function setDiffMode(mode) {
    const d = doc_();
    if (!d)
      return;
    if (mode !== "source" && !d.diffAvailable) {
      setStatusNote("No diff — clean file or not a git repo", 4000);
      return;
    }
    if (mode === "source") {
      d.diffMode = null;
      d.diffDismissed = true;
    } else {
      d.diffMode = mode;
      d.diffDismissed = false;
      d.openedInDiffView = true;
      setLayoutPref(mode);
    }
    syncPreview();
    syncDiffView();
    updateStatus();
  }
  async function drawDiff(d, force = false) {
    if (force || d.diffText === undefined) {
      diffContent.replaceChildren();
      try {
        d.diffReq = api("/api/diff", { path: d.path });
        const j = await d.diffReq;
        d.diffText = j.diff || "";
        d.diffHunks = parseDiff(d.diffText);
      } catch (e) {
        d.diffText = "";
        d.diffHunks = [];
        setStatusNote("No diff: " + e.message, 4000);
      } finally {
        d.diffReq = null;
      }
      if (shown !== d)
        return;
    }
    renderDiff(d);
    if (d.diffScroll) {
      diffview.scrollTop = d.diffScroll;
      d.diffScroll = 0;
    }
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
    syncDiffAgentTargets();
  }
  function syncDiffAgentTargets() {
    if (!diffview || diffview.hidden)
      return;
    const d = doc_();
    if (!d)
      return;
    const ranges = (S2.agentTargets || []).filter((t) => t.path === d.path);
    for (const el of diffview.querySelectorAll("[data-l]")) {
      const l = +el.dataset.l;
      const inAgent = ranges.some((r) => l >= r.l1 && l <= r.l2);
      const isAnchor = ranges.some((r) => l === r.l1);
      el.classList.toggle("agent-sel", inAgent);
      el.classList.toggle("agent-anchor", isAnchor);
    }
  }
  function hunkHeader(hunk) {
    const el = document.createElement("div");
    el.className = "diff-hunk-head";
    el.textContent = "@@ -" + hunk.oldStart + " +" + hunk.newStart + " @@";
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
        cur.rows.push({ type: "del", oldLine: oldLine++, at: newLine, text: body });
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
      anchor(r, row);
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
    anchor(el, row);
    el.append(lineCell(ln), markerCell(row.type), codeCell(row.text));
    return el;
  }
  function anchor(el, row) {
    if (row.newLine !== undefined)
      el.dataset.l = row.newLine;
    else if (row.at !== undefined)
      el.dataset.at = row.at;
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
    el.innerHTML = esc2(text || "") || "&nbsp;";
    return el;
  }
  function initDiff() {
    const sw = $("#diff-switch");
    if (!sw)
      return;
    sw.addEventListener("mousedown", (e) => {
      if (!e.target.closest("button"))
        e.preventDefault();
    });
    $("#diff-source")?.addEventListener("click", (e) => {
      e.stopPropagation();
      setDiffMode("source");
    });
    $("#diff-btn")?.addEventListener("click", (e) => {
      e.stopPropagation();
      const d = doc_();
      if (!d || !d.diffAvailable)
        return;
      setDiffMode(d.diffMode || layoutPref());
    });
    const menu = $("#diff-menu");
    if (menu) {
      menu.addEventListener("click", (e) => {
        const item = e.target.closest("[data-diff-opt]");
        if (!item)
          return;
        e.stopPropagation();
        setDiffMode(item.dataset.diffOpt);
        item.blur();
      });
    }
  }

  // web/src/status.js
  function updateStatus() {
    const d = doc_();
    const sizeEl = $("#st-size");
    if (sizeEl)
      sizeEl.textContent = d ? fmtBytes(d.size) : "";
    if (d && d.isImage) {
      const posEl = $("#st-pos");
      if (posEl) {
        const zoomText = d.imageFit ? `Fit (${Math.round((d.imageScale || 1) * 100)}%)` : `${Math.round((d.imageScale || 1) * 100)}%`;
        posEl.textContent = d.imageMeta ? `${d.imageMeta.width} × ${d.imageMeta.height} px · ${zoomText}` : zoomText;
      }
    }
    const isMd = !!(d && d.markdown), shown2 = previewing(d);
    const mdBtn = $('[data-action="md-preview"]');
    if (mdBtn) {
      mdBtn.hidden = !isMd;
      mdBtn.classList.toggle("active", shown2);
    }
    const sw = $("#md-switch");
    if (sw) {
      sw.hidden = !isMd;
      document.body.classList.toggle("md-tab", isMd);
      for (const b of sw.children)
        b.classList.toggle("on", isMd && b.dataset.md === "preview" === shown2);
    }
    const isCode = d && !d.isImage;
    const inGit = !!S2.meta?.git;
    const hasDiff = !!(d && d.diffAvailable);
    const isDiffOn = !!(d && d.diffMode);
    const currentLayout = d && d.diffMode || layoutPref();
    const dsw = $("#diff-switch");
    if (dsw) {
      const showSwitch = inGit && isCode;
      dsw.hidden = !showSwitch;
      document.body.classList.toggle("diff-tab", hasDiff);
      const btn = $("#diff-btn");
      if (btn) {
        btn.disabled = !hasDiff;
        btn.classList.toggle("disabled", !hasDiff);
        btn.classList.toggle("on", hasDiff && isDiffOn);
        btn.title = hasDiff ? withKeys(`Show changes against HEAD, ${currentLayout === "unified" ? "unified" : "split"} ({Mod+D})`) : "There are no git modified files.";
      }
      const srcBtn = $("#diff-source");
      if (srcBtn) {
        srcBtn.classList.toggle("on", !hasDiff || !isDiffOn);
        srcBtn.title = withKeys("Show the file ({Mod+D})");
      }
      const menuItems = dsw.querySelectorAll(".diff-menu-item");
      for (const item of menuItems) {
        item.classList.toggle("active", item.dataset.diffOpt === currentLayout);
      }
    }
    const verEl = $("#st-ver");
    if (verEl && S2.meta?.version) {
      verEl.textContent = "v" + S2.meta.version;
      verEl.title = `px0 v${S2.meta.version} (Click for shortcuts & help)`;
    }
    drawLspStatus();
  }
  var noteTimer = null;
  function setStatusNote(msg, timeoutMs = 0) {
    if (noteTimer) {
      clearTimeout(noteTimer);
      noteTimer = null;
    }
    const el = $("#st-pos");
    if (el)
      el.textContent = msg || "";
    if (msg && timeoutMs > 0) {
      noteTimer = setTimeout(() => {
        if (el && el.textContent === msg)
          el.textContent = "";
        noteTimer = null;
      }, timeoutMs);
    }
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
  var metricsMenuEl = $("#metrics-menu");
  var lastMetrics = null;
  function renderMetricsMenu(m) {
    if (!metricsMenuEl || !m)
      return;
    metricsMenuEl.innerHTML = `
    <div class="metrics-title">
      <span>Process Metrics</span>
      <span class="toast-chip">px0</span>
    </div>
    <div class="metrics-grid">
      <div class="metrics-row">
        <span class="metrics-label">Resident RAM (RSS)</span>
        <span class="metrics-val">${fmtBytes(m.rssBytes)}</span>
      </div>
      <div class="metrics-row">
        <span class="metrics-label">CPU Usage</span>
        <span class="metrics-val">${m.cpuUsage.toFixed(1)}%</span>
      </div>
      <div class="metrics-row">
        <span class="metrics-label">Active Goroutines</span>
        <span class="metrics-val">${m.goroutines || 0}</span>
      </div>
    </div>
  `;
  }
  function closeMetricsMenu() {
    if (metricsMenuEl)
      metricsMenuEl.hidden = true;
  }
  function placeMetricsMenu() {
    const contEl = $("#st-metrics");
    if (!contEl || !metricsMenuEl)
      return;
    const r = contEl.getBoundingClientRect();
    metricsMenuEl.style.bottom = innerHeight - r.top + 6 + "px";
    metricsMenuEl.style.right = Math.max(8, innerWidth - r.right) + "px";
    metricsMenuEl.style.left = "auto";
  }
  function toggleMetricsMenu() {
    if (!metricsMenuEl)
      return;
    if (!metricsMenuEl.hidden) {
      closeMetricsMenu();
      return;
    }
    if (lastMetrics)
      renderMetricsMenu(lastMetrics);
    metricsMenuEl.hidden = false;
    placeMetricsMenu();
    refreshMetrics();
  }
  function updateMetricsDisplay(m) {
    if (!m)
      return;
    lastMetrics = m;
    const cpuEl = $("#st-cpu");
    const ramEl = $("#st-ram");
    if (cpuEl)
      cpuEl.textContent = `${m.cpuUsage.toFixed(1)}%`;
    if (ramEl)
      ramEl.textContent = fmtBytes(m.rssBytes);
    if (metricsMenuEl && !metricsMenuEl.hidden) {
      renderMetricsMenu(m);
      placeMetricsMenu();
    }
  }
  async function refreshMetrics() {
    try {
      const m = await api("/api/metrics");
      updateMetricsDisplay(m);
    } catch {}
  }
  function initMetrics() {
    const contEl = $("#st-metrics");
    if (contEl) {
      contEl.addEventListener("click", (e) => {
        e.stopPropagation();
        toggleMetricsMenu();
      });
      contEl.addEventListener("keydown", (e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          toggleMetricsMenu();
        }
      });
    }
    addEventListener("click", (e) => {
      if (!e.target.closest("#metrics-menu, #st-metrics"))
        closeMetricsMenu();
    });
    addEventListener("keydown", (e) => {
      if (e.key === "Escape")
        closeMetricsMenu();
    });
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
  var diffviewEl = $("#diffview");
  var SEL_KEYS = { KeyC: "copy-ref", KeyA: "copy-agent", KeyU: "usages", KeyE: "agent-edit" };
  var agentHandler = null;
  function setAgentHandler(fn) {
    agentHandler = fn;
  }
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
    if (diffviewEl && !diffviewEl.hidden && diffviewEl.contains(range.commonAncestorContainer)) {
      return diffSelection(range, d);
    }
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
  function diffSelection(range, d) {
    let l1 = Infinity, l2 = -Infinity, at1 = Infinity, at2 = -Infinity;
    const parts = [];
    const seen = new Set;
    for (const el of diffviewEl.querySelectorAll("[data-l], [data-at]")) {
      if (!range.intersectsNode(el))
        continue;
      const code = el.querySelector(".diff-code");
      if (el.dataset.l !== undefined) {
        const n = +el.dataset.l;
        if (n < l1)
          l1 = n;
        if (n > l2)
          l2 = n;
        if (seen.has(n))
          continue;
        seen.add(n);
      } else {
        const n = +el.dataset.at;
        if (n < at1)
          at1 = n;
        if (n > at2)
          at2 = n;
      }
      parts.push(code ? code.textContent : "");
    }
    if (!parts.length)
      return null;
    if (l1 === Infinity) {
      const last = Math.max(1, d.total || 1);
      l1 = Math.min(last, Math.max(1, at1 - 1));
      l2 = Math.max(l1, Math.min(last, at2));
    }
    const text = parts.join(`
`).trim();
    if (!text)
      return null;
    return { text, l1, l2, path: d.path, fromDiff: true };
  }
  var selectionRef = ({ path, l1, l2 }) => path + ":" + (l1 === l2 ? l1 : l1 + "-" + l2);
  function showSelectionBar(info) {
    current = info;
    const lines = info.l2 - info.l1 + 1;
    statsEl.textContent = (lines === 1 ? "1 line" : lines + " lines") + " · " + info.text.length.toLocaleString() + " chars";
    status.classList.add("selecting");
    fitStatus();
  }
  function hideSelectionBar() {
    closeSelMenu();
    if (!current)
      return;
    current = null;
    if (statsEl)
      statsEl.textContent = "";
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
    if (!current) {
      if (act === "agent-edit") {
        const d = doc_();
        if (d && agentHandler) {
          const line = d.cur || 1;
          const text2 = d.lines && d.lines[line - 1] || "";
          agentHandler({ text: text2, l1: line, l2: line, path: d.path });
          return true;
        }
      }
      return false;
    }
    const { text, path } = current;
    const ref = selectionRef(current);
    if (act === "copy-ref") {
      copyToClipboard(ref, "Copied");
    } else if (act === "copy-agent") {
      const ext = path.split(".").pop() || "";
      const lineStr = current.l1 === current.l2 ? "line " + current.l1 : "lines " + current.l1 + "-" + current.l2;
      const snippet = "@" + path + " " + lineStr + "\n```" + ext + `
` + text + "\n```";
      copyToClipboard(snippet, "Copied");
    } else if (act === "agent-edit") {
      if (!agentHandler)
        return false;
      agentHandler(current);
    } else if (act === "usages") {
      findReferences(text.split(/\s+/)[0] || text);
    } else {
      return false;
    }
    return true;
  }
  var menu = $("#sel-menu");
  function closeSelMenu() {
    if (menu && !menu.hidden)
      menu.hidden = true;
  }
  var SEL_MENU_ITEMS = [
    { sel: "copy-ref", label: "Copy Ref", keys: "Alt+C" },
    { sel: "copy-agent", label: "Copy with Context", keys: "Alt+A" },
    { sel: "agent-edit", label: "Edit Inline", keys: "Alt+E" },
    { sel: "usages", label: "Find Usages", keys: "Alt+U" }
  ];
  function openSelMenu(x, y) {
    menu.replaceChildren();
    for (const item of SEL_MENU_ITEMS) {
      const btn = document.createElement("button");
      btn.className = "sel-menu-item";
      btn.dataset.sel = item.sel;
      btn.setAttribute("role", "menuitem");
      btn.title = item.label + (item.keys ? ` (${keyLabel(item.keys)})` : "");
      const label = document.createElement("span");
      label.textContent = item.label;
      btn.append(label);
      const kbd = document.createElement("kbd");
      kbd.className = "footer-kbd";
      kbd.textContent = keyLabel(item.keys);
      btn.append(kbd);
      menu.append(btn);
    }
    menu.hidden = false;
    const { offsetWidth: w, offsetHeight: h } = menu;
    menu.style.left = Math.max(4, x + w > innerWidth - 4 ? x - w : x) + "px";
    menu.style.top = Math.max(4, y + h > innerHeight - 4 ? y - h : y) + "px";
  }
  var bar = () => $("#footer-sel");
  function initSelectionBar() {
    document.addEventListener("mouseup", () => setTimeout(updateSelectionBar, 20));
    vp.addEventListener("keyup", (e) => {
      if (e.shiftKey)
        setTimeout(updateSelectionBar, 20);
    });
    document.addEventListener("selectionchange", () => updateSelectionBar());
    document.addEventListener("mousedown", (e) => {
      if (!S2.selAll || e.button === 2 || e.target.closest?.("#footer-sel, #sel-menu"))
        return;
      if (e.target === vp && (e.offsetX >= vp.clientWidth || e.offsetY >= vp.clientHeight))
        return;
      clearSelectAll();
    }, true);
    for (const el of [bar(), menu]) {
      if (!el)
        continue;
      el.addEventListener("mousedown", (e) => e.preventDefault());
      el.addEventListener("click", (e) => {
        const btn = e.target.closest("[data-sel]");
        if (!btn)
          return;
        closeSelMenu();
        runSelectionAction(btn.dataset.sel);
      });
    }
    if (!menu)
      return;
    document.addEventListener("contextmenu", (e) => {
      if (menu.contains(e.target)) {
        e.preventDefault();
        return;
      }
      const inCode = vp.contains(e.target) || diffviewEl && !diffviewEl.hidden && diffviewEl.contains(e.target);
      if (!inCode) {
        closeSelMenu();
        return;
      }
      updateSelectionBar();
      if (!current) {
        closeSelMenu();
        return;
      }
      e.preventDefault();
      openSelMenu(e.clientX, e.clientY);
    });
    document.addEventListener("mousedown", (e) => {
      if (!menu.hidden && !menu.contains(e.target))
        closeSelMenu();
    }, true);
    addEventListener("keydown", (e) => {
      if (e.key === "Escape")
        closeSelMenu();
    });
    addEventListener("resize", closeSelMenu);
    addEventListener("blur", closeSelMenu);
    document.addEventListener("scroll", closeSelMenu, true);
  }

  // web/src/imageview.js
  var ivInit = false;
  var isPanning = false;
  var panStart = { x: 0, y: 0 };
  var panOrigin = { x: 0, y: 0 };
  function isImageViewing(d = doc_()) {
    return !!(d && d.isImage);
  }
  function syncImageView() {
    const d = doc_();
    const imgView = $("#imgview");
    if (!imgView)
      return;
    if (isImageViewing(d)) {
      $("#empty").hidden = true;
      imgView.hidden = false;
      renderImageView(d);
    } else {
      imgView.hidden = true;
    }
  }
  function renderImageView(d) {
    if (!ivInit)
      initImageViewer();
    const img = $("#imgview-img");
    const canvas = $("#imgview-canvas");
    if (!img || !canvas)
      return;
    const rawUrl = "/api/raw?path=" + encodeURIComponent(d.path);
    if (img.dataset.curPath !== d.path) {
      img.dataset.curPath = d.path;
      img.src = rawUrl;
    }
    if (d.imageFit === undefined)
      d.imageFit = true;
    if (d.imageScale === undefined)
      d.imageScale = 1;
    if (d.imagePanX === undefined)
      d.imagePanX = 0;
    if (d.imagePanY === undefined)
      d.imagePanY = 0;
    if (d.imageBg === undefined)
      d.imageBg = "checker";
    if (d.imagePixelated === undefined) {
      d.imagePixelated = d.imageMeta ? d.imageMeta.width <= 64 && d.imageMeta.height <= 64 : false;
    }
    const onLoaded = () => {
      d.imageMeta = {
        width: img.naturalWidth,
        height: img.naturalHeight
      };
      if (d.imagePixelated === undefined) {
        d.imagePixelated = d.imageMeta.width <= 64 && d.imageMeta.height <= 64;
      }
      applyImageTransform(d);
    };
    if (img.complete && img.naturalWidth > 0) {
      onLoaded();
    } else {
      img.onload = onLoaded;
    }
    applyImageTransform(d);
  }
  function applyImageTransform(d = doc_()) {
    if (!d || !d.isImage)
      return;
    const canvas = $("#imgview-canvas");
    const img = $("#imgview-img");
    const vp2 = $("#imgview-viewport");
    if (!canvas || !img || !vp2)
      return;
    const natW = d.imageMeta?.width || img.naturalWidth || 100;
    const natH = d.imageMeta?.height || img.naturalHeight || 100;
    let currentScale = d.imageScale || 1;
    if (d.imageFit) {
      const vpW = Math.max(100, vp2.clientWidth - 64);
      const vpH = Math.max(100, vp2.clientHeight - 64);
      const fitScale = Math.min(vpW / natW, vpH / natH);
      currentScale = natW <= vpW && natH <= vpH ? 1 : fitScale;
      d.imageScale = currentScale;
      d.imagePanX = 0;
      d.imagePanY = 0;
    }
    canvas.style.transform = `translate(${d.imagePanX || 0}px, ${d.imagePanY || 0}px) scale(${currentScale})`;
    canvas.className = "bg-" + (d.imageBg || "checker");
    img.classList.toggle("render-pixelated", !!d.imagePixelated);
    img.classList.toggle("render-smooth", !d.imagePixelated);
    const zoomLabel = $("#iv-zoom-label");
    if (zoomLabel) {
      zoomLabel.textContent = d.imageFit ? `Fit (${Math.round(currentScale * 100)}%)` : `${Math.round(currentScale * 100)}%`;
    }
    const bgBtn = $("#iv-bg");
    if (bgBtn) {
      bgBtn.textContent = d.imageBg === "dark" ? "Dark" : d.imageBg === "light" ? "Light" : "Checker";
    }
    const pixelBtn = $("#iv-pixel");
    if (pixelBtn) {
      pixelBtn.textContent = d.imagePixelated ? "Pixelated" : "Smooth";
      pixelBtn.classList.toggle("active", !!d.imagePixelated);
    }
    const metaEl = $("#iv-meta");
    if (metaEl) {
      metaEl.textContent = `${natW} × ${natH} px · ${fmtBytes(d.size || 0)}`;
    }
    updateStatus();
  }
  function zoomImage(delta, factor = 1.25) {
    const d = doc_();
    if (!d || !d.isImage)
      return;
    d.imageFit = false;
    if (delta > 0) {
      d.imageScale = Math.min(32, (d.imageScale || 1) * factor);
    } else {
      d.imageScale = Math.max(0.05, (d.imageScale || 1) / factor);
    }
    applyImageTransform(d);
  }
  function fitImage() {
    const d = doc_();
    if (!d || !d.isImage)
      return;
    d.imageFit = true;
    d.imagePanX = 0;
    d.imagePanY = 0;
    applyImageTransform(d);
  }
  function actualSizeImage() {
    const d = doc_();
    if (!d || !d.isImage)
      return;
    d.imageFit = false;
    d.imageScale = 1;
    d.imagePanX = 0;
    d.imagePanY = 0;
    applyImageTransform(d);
  }
  function cycleImageBg() {
    const d = doc_();
    if (!d || !d.isImage)
      return;
    const modes = ["checker", "dark", "light"];
    const curIdx = modes.indexOf(d.imageBg || "checker");
    d.imageBg = modes[(curIdx + 1) % modes.length];
    applyImageTransform(d);
  }
  function toggleImagePixelated() {
    const d = doc_();
    if (!d || !d.isImage)
      return;
    d.imagePixelated = !d.imagePixelated;
    applyImageTransform(d);
  }
  function panImage(dx, dy) {
    const d = doc_();
    if (!d || !d.isImage)
      return;
    d.imageFit = false;
    d.imagePanX = (d.imagePanX || 0) + dx;
    d.imagePanY = (d.imagePanY || 0) + dy;
    applyImageTransform(d);
  }
  function handleImageKey(e) {
    const d = doc_();
    if (!d || !d.isImage)
      return false;
    if (e.key === "+" || e.key === "=") {
      zoomImage(1);
      return true;
    }
    if (e.key === "-" || e.key === "_") {
      zoomImage(-1);
      return true;
    }
    if (e.key === "0") {
      fitImage();
      return true;
    }
    if (e.key === "1") {
      actualSizeImage();
      return true;
    }
    if (e.key === "b" || e.key === "B") {
      cycleImageBg();
      return true;
    }
    if (e.key === "p" || e.key === "P") {
      toggleImagePixelated();
      return true;
    }
    if (e.key === "ArrowUp") {
      panImage(0, 40);
      return true;
    }
    if (e.key === "ArrowDown") {
      panImage(0, -40);
      return true;
    }
    if (e.key === "ArrowLeft") {
      panImage(40, 0);
      return true;
    }
    if (e.key === "ArrowRight") {
      panImage(-40, 0);
      return true;
    }
    return false;
  }
  function initImageViewer() {
    if (ivInit)
      return;
    ivInit = true;
    const vp2 = $("#imgview-viewport");
    const hud = $("#imgview-hud");
    if (!vp2)
      return;
    $("#iv-zoom-in")?.addEventListener("click", (e) => {
      e.stopPropagation();
      zoomImage(1);
    });
    $("#iv-zoom-out")?.addEventListener("click", (e) => {
      e.stopPropagation();
      zoomImage(-1);
    });
    $("#iv-zoom-label")?.addEventListener("click", (e) => {
      e.stopPropagation();
      const d = doc_();
      if (d?.imageFit)
        actualSizeImage();
      else
        fitImage();
    });
    $("#iv-fit")?.addEventListener("click", (e) => {
      e.stopPropagation();
      fitImage();
    });
    $("#iv-100")?.addEventListener("click", (e) => {
      e.stopPropagation();
      actualSizeImage();
    });
    $("#iv-bg")?.addEventListener("click", (e) => {
      e.stopPropagation();
      cycleImageBg();
    });
    $("#iv-pixel")?.addEventListener("click", (e) => {
      e.stopPropagation();
      toggleImagePixelated();
    });
    vp2.addEventListener("mousedown", (e) => {
      if (e.target.closest("#imgview-hud") || e.button !== 0)
        return;
      const d = doc_();
      if (!d || !d.isImage)
        return;
      isPanning = true;
      panStart = { x: e.clientX, y: e.clientY };
      panOrigin = { x: d.imagePanX || 0, y: d.imagePanY || 0 };
      vp2.classList.add("panning");
      e.preventDefault();
    });
    window.addEventListener("mousemove", (e) => {
      if (!isPanning)
        return;
      const d = doc_();
      if (!d || !d.isImage)
        return;
      const dx = e.clientX - panStart.x;
      const dy = e.clientY - panStart.y;
      if (Math.abs(dx) > 3 || Math.abs(dy) > 3) {
        d.imageFit = false;
      }
      d.imagePanX = panOrigin.x + dx;
      d.imagePanY = panOrigin.y + dy;
      applyImageTransform(d);
    });
    window.addEventListener("mouseup", () => {
      if (!isPanning)
        return;
      isPanning = false;
      vp2.classList.remove("panning");
    });
    vp2.addEventListener("wheel", (e) => {
      const d = doc_();
      if (!d || !d.isImage)
        return;
      e.preventDefault();
      const factor = e.ctrlKey || e.metaKey ? 1.15 : Math.abs(e.deltaY) > 50 ? 1.25 : 1.1;
      if (e.deltaY < 0) {
        zoomImage(1, factor);
      } else {
        zoomImage(-1, factor);
      }
    }, { passive: false });
    window.addEventListener("resize", () => {
      const d = doc_();
      if (d?.isImage && d.imageFit) {
        applyImageTransform(d);
      }
    });
  }

  // web/src/tabs.js
  var closedTabs = [];
  var MAX_CLOSED = 20;
  async function openFile(path, opts = {}) {
    const { line, push = true, col } = opts;
    let idx = S2.tabs.findIndex((t) => t.path === path);
    if (idx < 0) {
      let j;
      const start2 = line ? Math.max(0, Math.floor((line - 1) / CHUNK) * CHUNK) : 0;
      try {
        j = await api("/api/file", { path, start: start2, count: CHUNK });
      } catch (e) {
        setStatusNote(path + ": " + e.message, 4000);
        return;
      }
      const isImg = !!j.image;
      const hasDiff = !isImg && !!j.diffAvailable;
      const d2 = {
        path,
        name: path.split("/").pop(),
        lang: isImg ? "image" : j.lang,
        total: isImg ? 0 : j.total,
        maxCols: isImg ? 0 : j.maxCols,
        size: j.size,
        lines: isImg ? [] : new Array(j.total),
        chunks: new Set(isImg ? [] : [start2 / CHUNK]),
        pending: new Set,
        refining: new Set,
        scrollTop: 0,
        cur: line || 1,
        outline: null,
        gen: 0,
        markdown: !isImg && !!j.markdown,
        isImage: isImg,
        gutter: null,
        diffMode: hasDiff ? layoutPref() || "split" : null,
        diffAvailable: hasDiff,
        diffDismissed: false,
        openedInDiffView: hasDiff
      };
      if (!isImg) {
        for (let i = 0;i < j.lines.length; i++)
          d2.lines[j.start + i] = j.lines[i];
      }
      d2.lsp = !isImg && j.lsp || { state: "off", server: "" };
      S2.tabs.push(d2);
      idx = S2.tabs.length - 1;
      if (!isImg && j.refine)
        refineChunk(d2, start2 / CHUNK);
      if (!isImg)
        loadGutter(d2);
    }
    const prev = doc_();
    if (prev && prev !== S2.tabs[idx])
      prev.scrollTop = vp.scrollTop;
    if (prev !== S2.tabs[idx]) {
      clearSelectAll();
      clearFind();
    }
    S2.active = idx;
    const d = S2.tabs[idx];
    if (d && d.diffAvailable && (treeEl?.classList.contains("changed-only") || !d.diffDismissed && d.diffMode === null)) {
      d.diffMode = layoutPref() || "split";
      d.diffDismissed = false;
      d.openedInDiffView = true;
    }
    $("#empty").hidden = true;
    syncImageView();
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
    saveWorkspaceState();
  }
  async function loadGutter(d) {
    if (!S2.meta?.git)
      return;
    try {
      const j = await api("/api/gutter", { path: d.path });
      d.diffAvailable = !!j.available;
      if (j.available && d.diffMode === null && !d.diffDismissed) {
        d.diffMode = layoutPref() || "split";
        d.openedInDiffView = true;
        if (doc_() === d) {
          syncDiffView();
          syncPreview();
        }
      }
      if (!j.available) {
        d.gutter = null;
      } else {
        const marks = new Map;
        for (const n of j.modified)
          marks.set(n, "mod");
        for (const n of j.added)
          marks.set(n, "add");
        d.gutter = { marks, dels: new Set(j.deleted) };
      }
      if (doc_() === d) {
        updateStatus();
        render();
      }
      drawTabs();
    } catch {}
  }
  async function reloadOpenTabs() {
    if (S2.tabs.length === 0)
      return;
    const activeDoc = doc_();
    if (activeDoc) {
      activeDoc.scrollTop = vp.scrollTop;
      if (previewing(activeDoc)) {
        const mv = $("#mdview");
        if (mv)
          activeDoc.mdScroll = mv.scrollTop;
      }
    }
    const targets = S2.tabs.map((t) => ({
      oldDoc: t,
      path: t.path,
      anchor: t.cur || 1,
      start: t.cur ? Math.max(0, Math.floor((t.cur - 1) / CHUNK) * CHUNK) : 0
    }));
    const results = await Promise.allSettled(targets.map((tgt) => api("/api/file", { path: tgt.path, start: tgt.start, count: CHUNK })));
    for (let i = 0;i < targets.length; i++) {
      const res = results[i];
      const tgt = targets[i];
      const idx = S2.tabs.indexOf(tgt.oldDoc);
      if (idx < 0)
        continue;
      if (res.status !== "fulfilled") {
        if (idx === S2.active) {
          setStatusNote(tgt.path + ": " + (res.reason?.message || "failed to load"), 4000);
        }
        continue;
      }
      const j = res.value;
      if (j.image) {
        tgt.oldDoc.size = j.size;
        continue;
      }
      const keep = tgt.oldDoc;
      const hasDiff = !!j.diffAvailable;
      const newCur = Math.max(1, Math.min(keep.cur || 1, j.total));
      const diffMode = hasDiff ? keep.diffMode || null : null;
      const d2 = {
        path: tgt.path,
        name: tgt.path.split("/").pop(),
        lang: j.lang,
        total: j.total,
        maxCols: j.maxCols,
        size: j.size,
        lines: new Array(j.total),
        chunks: new Set([tgt.start / CHUNK]),
        pending: new Set,
        refining: new Set,
        scrollTop: keep.scrollTop || 0,
        cur: newCur,
        col: keep.col || 0,
        outline: null,
        gen: 0,
        markdown: !!j.markdown,
        mdScroll: keep.mdScroll || 0,
        gutter: null,
        diffMode,
        diffAvailable: hasDiff,
        diffDismissed: !!keep.diffDismissed || !keep.diffMode,
        openedInDiffView: !!keep.openedInDiffView || !!keep.diffMode,
        diffScroll: keep === activeDoc && keep.diffMode ? diffScrollTop() : 0
      };
      for (let k = 0;k < j.lines.length; k++) {
        d2.lines[j.start + k] = j.lines[k];
      }
      d2.lsp = j.lsp || { state: "off", server: "" };
      S2.tabs[idx] = d2;
      if (j.refine)
        refineChunk(d2, tgt.start / CHUNK);
    }
    await Promise.allSettled(S2.tabs.filter((t) => !t.isImage).map((t) => loadGutter(t)));
    const d = doc_();
    if (d) {
      S2.lsp.state = d.lsp && d.lsp.state || "off";
      S2.lsp.server = d.lsp && d.lsp.server || "";
      S2.lsp.missing = d.lsp && d.lsp.missing || "";
      warmLSP(d);
      syncImageView();
      syncPreview();
      syncDiffView(true);
      layout();
      vp.scrollTop = d.scrollTop;
      render();
      if ($("#panel-outline")?.classList.contains("active"))
        loadOutline();
    }
    drawTabs();
    drawCrumbs();
    updateStatus();
    saveWorkspaceState();
  }
  function centerLine(n) {
    if (previewing()) {
      previewLine(n);
      return;
    }
    const y = (n - 1) * LH2 - Math.max(0, vp.clientHeight / 2 - LH2 * 2);
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
      syncImageView();
      syncPreview();
      syncDiffView();
      rowsEl.innerHTML = "";
      sizer.style.height = "0px";
      $("#empty").hidden = false;
      drawCrumbs();
      drawTabs();
      updateStatus();
      saveWorkspaceState();
      return;
    }
    if (i < S2.active) {
      S2.active--;
    } else if (i === S2.active) {
      S2.active = Math.min(i, S2.tabs.length - 1);
    }
    const d = doc_();
    syncImageView();
    syncPreview();
    syncDiffView();
    drawTabs();
    drawCrumbs();
    layout();
    vp.scrollTop = d.scrollTop;
    render();
    updateStatus();
    saveWorkspaceState();
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
    $("#tabs").innerHTML = S2.tabs.map((t, i) => '<div class="tab' + (i === S2.active ? " active" : "") + (t.isImage ? " tab-image" : "") + (t.diffAvailable ? " git-modified" : "") + '" data-i="' + i + '" title="' + esc2(t.path) + '">' + (t.isImage ? '<svg class="tab-icon" viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.4"><rect x="2" y="2" width="12" height="12" rx="2"/><circle cx="5.5" cy="5.5" r="1.5"/><path d="M14 10l-3.5-3.5L3 14"/></svg>' : "") + '<span class="tn">' + esc2(t.name) + "</span>" + (t.diffAvailable ? '<span class="tab-git-dot" title="Modified in git">●</span>' : "") + '<span class="x" data-close="' + i + '" title="' + withKeys("Close tab ({Alt+W})") + '"><svg viewBox="0 0 10 10" aria-hidden="true"><path d="M2 2l6 6M8 2l-6 6"/></svg></span></div>').join("");
    const act = $("#tabs .tab.active");
    if (act)
      act.scrollIntoView({ block: "nearest", inline: "nearest" });
  }
  function switchTab(i) {
    if (i === S2.active || !S2.tabs[i])
      return;
    clearLink();
    const prev = doc_();
    if (prev)
      prev.scrollTop = vp.scrollTop;
    S2.active = i;
    const curDoc = S2.tabs[i];
    if (curDoc && curDoc.diffAvailable && (treeEl?.classList.contains("changed-only") || !curDoc.diffDismissed && curDoc.diffMode === null)) {
      curDoc.diffMode = layoutPref() || "split";
      curDoc.diffDismissed = false;
      curDoc.openedInDiffView = true;
    }
    syncImageView();
    syncPreview();
    syncDiffView();
    clearFind();
    clearSelectAll();
    S2.at = null;
    S2.lsp.state = S2.tabs[i].lsp && S2.tabs[i].lsp.state || "off";
    S2.lsp.server = S2.tabs[i].lsp && S2.tabs[i].lsp.server || "";
    S2.lsp.missing = S2.tabs[i].lsp && S2.tabs[i].lsp.missing || "";
    warmLSP(S2.tabs[i]);
    drawTabs();
    drawCrumbs();
    layout();
    vp.scrollTop = S2.tabs[i].scrollTop;
    render();
    updateStatus();
    if ($("#panel-outline")?.classList.contains("active"))
      loadOutline();
    pushHistory(S2.tabs[i].path, S2.tabs[i].cur);
    saveWorkspaceState();
  }
  function saveWorkspaceState() {
    try {
      const tabs = S2.tabs.map((t) => ({ path: t.path, cur: t.cur }));
      sessionStorage.setItem("px0.tabs", JSON.stringify({ tabs, active: S2.active }));
    } catch {}
  }
  async function restoreWorkspaceTabs() {
    try {
      const saved = sessionStorage.getItem("px0.tabs");
      if (!saved)
        return false;
      const { tabs, active } = JSON.parse(saved);
      if (!Array.isArray(tabs) || tabs.length === 0)
        return false;
      for (const t of tabs) {
        if (t.path)
          await openFile(t.path, { line: t.cur, push: false });
      }
      if (typeof active === "number" && active >= 0 && active < S2.tabs.length) {
        switchTab(active);
      }
      return true;
    } catch {
      return false;
    }
  }
  function drawCrumbs() {
    const el = $("#crumbs");
    if (el)
      el.innerHTML = "";
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
  var DEFAULT_THEME = "github-dark";
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
    if (setTheme(DEFAULT_THEME, false))
      return;
    const all = listThemes();
    if (all.length && !all.some((t) => t.id === currentTheme()))
      setTheme(all[0].id, false);
  }

  // web/src/vim.js
  var vimEnabled = false;
  var vimMode = "NORMAL";
  var vimCount = "";
  var vimPending = "";
  var vimPendingTimer = null;
  var WORD_RE = /[A-Za-z0-9_$]/;
  function isVimEnabled() {
    return vimEnabled;
  }
  function setVimModeEnabled(enabled, persist = true) {
    vimEnabled = !!enabled;
    if (!vimEnabled) {
      exitVisualMode();
      resetVimState();
    }
    document.body.classList.toggle("vim-mode-enabled", vimEnabled);
    updateVimCaret();
    updateVimStatus();
    const chip = $("#st-vim");
    if (chip)
      chip.hidden = !vimEnabled;
    const helpBtn = $("#btn-vim-help");
    if (helpBtn)
      helpBtn.hidden = !vimEnabled;
    if (persist) {
      try {
        localStorage.setItem("px0.editor.vimMode", vimEnabled ? "true" : "false");
      } catch {}
      if (S2.settings)
        S2.settings["editor.vimMode"] = vimEnabled;
    }
  }
  function updateVimCaret() {
    if (!vimEnabled) {
      document.body.classList.remove("vim-normal-caret");
      return;
    }
    document.body.classList.toggle("vim-normal-caret", vimMode === "NORMAL");
  }
  function resetVimState() {
    vimCount = "";
    vimPending = "";
    if (vimPendingTimer) {
      clearTimeout(vimPendingTimer);
      vimPendingTimer = null;
    }
    updateVimStatus();
  }
  function setVimPending(key) {
    vimPending = key;
    if (vimPendingTimer)
      clearTimeout(vimPendingTimer);
    vimPendingTimer = setTimeout(() => {
      resetVimState();
    }, 1400);
    updateVimStatus();
  }
  function getCount() {
    const c = parseInt(vimCount, 10);
    return isNaN(c) || c <= 0 ? 1 : c;
  }
  function updateVimStatus() {
    const chip = $("#st-vim");
    if (!chip)
      return;
    chip.hidden = !vimEnabled;
    if (!vimEnabled)
      return;
    chip.className = "status-vim-chip";
    let modeLabel = vimMode;
    if (vimMode === "VISUAL_LINE") {
      chip.classList.add("mode-visual-line");
      modeLabel = "V-LINE";
    } else if (vimMode === "VISUAL") {
      chip.classList.add("mode-visual");
      modeLabel = "VISUAL";
    } else {
      chip.classList.add("mode-normal");
      modeLabel = "NORMAL";
    }
    let extra = "";
    if (vimCount)
      extra += vimCount;
    if (vimPending)
      extra += vimPending;
    if (extra) {
      chip.innerHTML = esc2(modeLabel) + ' <span class="status-vim-pending">' + esc2(extra) + "</span>";
    } else {
      chip.textContent = modeLabel;
    }
  }
  function wordAtCaret() {
    const d = doc_();
    if (!d)
      return null;
    const row = rowFor(d.cur);
    if (!row)
      return S2.at || null;
    const code = row.querySelector(".c");
    if (!code)
      return S2.at || null;
    const full = code.textContent;
    let col = Math.min(d.col === Infinity ? full.length : d.col || 0, full.length);
    if (col >= full.length && col > 0)
      col = full.length - 1;
    let a = col, b = col;
    if (full[a] && WORD_RE.test(full[a])) {
      while (a > 0 && WORD_RE.test(full[a - 1]))
        a--;
      while (b < full.length && WORD_RE.test(full[b]))
        b++;
      if (a < b)
        return { word: full.slice(a, b), line: d.cur, col: a, path: d.path };
    }
    return S2.at || null;
  }
  function showHoverForCaret() {
    const at = wordAtCaret();
    if (!at)
      return;
    const caret = $("#caret");
    let x = 120, y = 120;
    if (caret) {
      const r = caret.getBoundingClientRect();
      x = Math.max(16, r.left);
      y = r.bottom + 4;
    }
    showHover(at, x, y);
  }
  function enterVisualMode(lineWise = false) {
    const d = doc_();
    if (!d)
      return;
    vimMode = lineWise ? "VISUAL_LINE" : "VISUAL";
    const row = rowFor(d.cur);
    const len = row ? row.querySelector(".c")?.textContent.length || 0 : 0;
    if (lineWise) {
      d.selAnchor = { line: d.cur, col: 0 };
      d.col = len;
    } else {
      if (!d.selAnchor) {
        const col = d.col === Infinity ? len : d.col || 0;
        d.selAnchor = { line: d.cur, col };
      }
    }
    placeCaret();
    updateDomSelection();
    updateVimCaret();
    updateVimStatus();
  }
  function exitVisualMode() {
    const d = doc_();
    vimMode = "NORMAL";
    if (d)
      clearSelection(d);
    resetVimState();
    updateVimCaret();
    updateVimStatus();
  }
  function ensureLineSelection() {
    const d = doc_();
    if (!d || vimMode !== "VISUAL_LINE")
      return;
    if (!d.selAnchor)
      d.selAnchor = { line: d.cur, col: 0 };
    const row = rowFor(d.cur);
    d.col = row ? row.querySelector(".c")?.textContent.length || 0 : 0;
    placeCaret();
    updateDomSelection();
  }
  function handleVimKeyDown(e) {
    if (!vimEnabled)
      return false;
    const active = document.activeElement;
    if (active && (active.tagName === "INPUT" || active.tagName === "TEXTAREA" || active.isContentEditable)) {
      return false;
    }
    if (e.key === "Escape") {
      if (vimMode !== "NORMAL") {
        e.preventDefault();
        exitVisualMode();
        return true;
      }
      if (vimPending || vimCount) {
        e.preventDefault();
        resetVimState();
        return true;
      }
      return false;
    }
    const d = doc_();
    if (!d)
      return false;
    const isVisual = vimMode === "VISUAL" || vimMode === "VISUAL_LINE";
    if (e.ctrlKey && !e.altKey && !e.metaKey) {
      if (e.key === "d") {
        e.preventDefault();
        const half = Math.max(1, Math.floor(vp.clientHeight / LH / 2)) * getCount();
        moveCursor(half, isVisual);
        if (vimMode === "VISUAL_LINE")
          ensureLineSelection();
        resetVimState();
        return true;
      }
      if (e.key === "u") {
        e.preventDefault();
        const half = Math.max(1, Math.floor(vp.clientHeight / LH / 2)) * getCount();
        moveCursor(-half, isVisual);
        if (vimMode === "VISUAL_LINE")
          ensureLineSelection();
        resetVimState();
        return true;
      }
      if (e.key === "f") {
        e.preventDefault();
        const page = Math.max(1, Math.floor(vp.clientHeight / LH) - 2) * getCount();
        moveCursor(page, isVisual);
        if (vimMode === "VISUAL_LINE")
          ensureLineSelection();
        resetVimState();
        return true;
      }
      if (e.key === "b") {
        e.preventDefault();
        const page = Math.max(1, Math.floor(vp.clientHeight / LH) - 2) * getCount();
        moveCursor(-page, isVisual);
        if (vimMode === "VISUAL_LINE")
          ensureLineSelection();
        resetVimState();
        return true;
      }
      if (e.key === "o") {
        e.preventDefault();
        go(-getCount());
        resetVimState();
        return true;
      }
      if (e.key === "i") {
        e.preventDefault();
        go(getCount());
        resetVimState();
        return true;
      }
    }
    if (e.metaKey || e.altKey)
      return false;
    if (isVisual) {
      if (e.key === "v" && !e.shiftKey) {
        e.preventDefault();
        if (vimMode === "VISUAL")
          exitVisualMode();
        else
          enterVisualMode(false);
        return true;
      }
      if (e.key === "V") {
        e.preventDefault();
        if (vimMode === "VISUAL_LINE")
          exitVisualMode();
        else
          enterVisualMode(true);
        return true;
      }
      if (e.key === "y") {
        e.preventDefault();
        const info = getSelectedRangeInfo();
        if (info && info.text) {
          const lineCount = info.l2 - info.l1 + 1;
          copyToClipboard(info.text, "Yanked " + (lineCount === 1 ? "1 line" : lineCount + " lines"));
        }
        exitVisualMode();
        return true;
      }
      if (e.key === "Y") {
        e.preventDefault();
        runSelectionAction("copy-ref");
        exitVisualMode();
        return true;
      }
      if (e.key === "e" || e.key === "c") {
        e.preventDefault();
        runSelectionAction("agent-edit");
        exitVisualMode();
        return true;
      }
      if (e.key === "u") {
        e.preventDefault();
        runSelectionAction("usages");
        exitVisualMode();
        return true;
      }
    }
    if (!vimPending && /^[0-9]$/.test(e.key)) {
      if (e.key === "0" && !vimCount) {} else {
        e.preventDefault();
        vimCount += e.key;
        updateVimStatus();
        return true;
      }
    }
    if (vimPending === "g") {
      e.preventDefault();
      if (e.key === "g") {
        const count2 = parseInt(vimCount, 10);
        if (!isNaN(count2) && count2 > 0) {
          d.cur = Math.max(1, Math.min(d.total, count2));
          d.col = 0;
          const y = (d.cur - 1) * LH;
          vp.scrollTop = Math.max(0, y - LH * 3);
          render();
          updateStatus();
        } else {
          vp.scrollTop = 0;
          d.cur = 1;
          d.col = 0;
          render();
          updateStatus();
        }
        if (isVisual) {
          placeCaret();
          updateDomSelection();
          if (vimMode === "VISUAL_LINE")
            ensureLineSelection();
        }
      } else if (e.key === "d") {
        const w = wordAtCaret();
        if (w) {
          pushHistory(d.path, d.cur);
          gotoDefinition(w);
        }
      } else if (e.key === "r") {
        findReferences();
      } else if (e.key === "h") {
        showCalls();
      } else if (e.key === "t") {
        const count2 = parseInt(vimCount, 10);
        if (!isNaN(count2) && count2 > 0 && count2 <= S2.tabs.length) {
          switchTab(count2 - 1);
        } else if (S2.tabs.length > 1) {
          switchTab((S2.active + 1) % S2.tabs.length);
        }
      } else if (e.key === "T") {
        if (S2.tabs.length > 1) {
          switchTab((S2.active - 1 + S2.tabs.length) % S2.tabs.length);
        }
      }
      resetVimState();
      return true;
    }
    if (vimPending === "z") {
      e.preventDefault();
      if (e.key === "z") {
        vp.scrollTop = Math.max(0, (d.cur - 1) * LH - (vp.clientHeight - LH) / 2);
        render();
        updateStatus();
      } else if (e.key === "t") {
        vp.scrollTop = Math.max(0, (d.cur - 1) * LH);
        render();
        updateStatus();
      } else if (e.key === "b") {
        vp.scrollTop = Math.max(0, (d.cur - 1) * LH - vp.clientHeight + LH * 2);
        render();
        updateStatus();
      }
      resetVimState();
      return true;
    }
    const count = getCount();
    switch (e.key) {
      case "h": {
        e.preventDefault();
        moveCol(-count, isVisual);
        resetVimState();
        return true;
      }
      case "l": {
        e.preventDefault();
        moveCol(count, isVisual);
        resetVimState();
        return true;
      }
      case "j": {
        e.preventDefault();
        moveCursor(count, isVisual);
        if (vimMode === "VISUAL_LINE")
          ensureLineSelection();
        resetVimState();
        return true;
      }
      case "k": {
        e.preventDefault();
        moveCursor(-count, isVisual);
        if (vimMode === "VISUAL_LINE")
          ensureLineSelection();
        resetVimState();
        return true;
      }
      case "w": {
        e.preventDefault();
        moveWord(count, isVisual);
        if (vimMode === "VISUAL_LINE")
          ensureLineSelection();
        resetVimState();
        return true;
      }
      case "b": {
        e.preventDefault();
        moveWord(-count, isVisual);
        if (vimMode === "VISUAL_LINE")
          ensureLineSelection();
        resetVimState();
        return true;
      }
      case "0": {
        e.preventDefault();
        caretToEdge(false, isVisual);
        resetVimState();
        return true;
      }
      case "$": {
        e.preventDefault();
        caretToEdge(true, isVisual);
        resetVimState();
        return true;
      }
      case "^": {
        e.preventDefault();
        const row = rowFor(d.cur);
        const text = row ? row.querySelector(".c")?.textContent || "" : "";
        const idx = text.search(/\S/);
        d.col = idx >= 0 ? idx : 0;
        revealCaretX(placeCaret());
        if (isVisual)
          updateDomSelection();
        resetVimState();
        return true;
      }
      case "G": {
        e.preventDefault();
        const targetLine = vimCount ? parseInt(vimCount, 10) : d.total;
        d.cur = Math.max(1, Math.min(d.total, targetLine));
        d.col = 0;
        const y = (d.cur - 1) * LH;
        vp.scrollTop = Math.max(0, y - LH * 3);
        render();
        updateStatus();
        if (isVisual) {
          placeCaret();
          updateDomSelection();
          if (vimMode === "VISUAL_LINE")
            ensureLineSelection();
        }
        resetVimState();
        return true;
      }
      case "g": {
        e.preventDefault();
        setVimPending("g");
        return true;
      }
      case "z": {
        e.preventDefault();
        setVimPending("z");
        return true;
      }
      case "H": {
        e.preventDefault();
        const topL = Math.floor(vp.scrollTop / LH) + 1;
        d.cur = Math.max(1, Math.min(d.total, topL));
        render();
        updateStatus();
        if (isVisual) {
          placeCaret();
          updateDomSelection();
          if (vimMode === "VISUAL_LINE")
            ensureLineSelection();
        }
        resetVimState();
        return true;
      }
      case "M": {
        e.preventDefault();
        const midL = Math.floor((vp.scrollTop + vp.clientHeight / 2) / LH) + 1;
        d.cur = Math.max(1, Math.min(d.total, midL));
        render();
        updateStatus();
        if (isVisual) {
          placeCaret();
          updateDomSelection();
          if (vimMode === "VISUAL_LINE")
            ensureLineSelection();
        }
        resetVimState();
        return true;
      }
      case "L": {
        e.preventDefault();
        const botL = Math.floor((vp.scrollTop + vp.clientHeight - LH) / LH);
        d.cur = Math.max(1, Math.min(d.total, botL));
        render();
        updateStatus();
        if (isVisual) {
          placeCaret();
          updateDomSelection();
          if (vimMode === "VISUAL_LINE")
            ensureLineSelection();
        }
        resetVimState();
        return true;
      }
      case "K": {
        e.preventDefault();
        showHoverForCaret();
        resetVimState();
        return true;
      }
      case "/": {
        e.preventDefault();
        openFind();
        resetVimState();
        return true;
      }
      case "?": {
        e.preventDefault();
        openFind();
        findNextMatch(-1);
        resetVimState();
        return true;
      }
      case "n": {
        e.preventDefault();
        findNextMatch(1);
        resetVimState();
        return true;
      }
      case "N": {
        e.preventDefault();
        findNextMatch(-1);
        resetVimState();
        return true;
      }
      case "*": {
        e.preventDefault();
        const w = wordAtCaret();
        if (w && w.word) {
          S2.at = w;
          S2.lastWord = w.word;
          S2.occ = w.word;
          paint();
          openFind(w.word);
          findNextMatch(1);
        }
        resetVimState();
        return true;
      }
      case "#": {
        e.preventDefault();
        const w = wordAtCaret();
        if (w && w.word) {
          S2.at = w;
          S2.lastWord = w.word;
          S2.occ = w.word;
          paint();
          openFind(w.word);
          findNextMatch(-1);
        }
        resetVimState();
        return true;
      }
      case "v": {
        e.preventDefault();
        enterVisualMode(false);
        resetVimState();
        return true;
      }
      case "V": {
        e.preventDefault();
        enterVisualMode(true);
        resetVimState();
        return true;
      }
      case ":": {
        e.preventDefault();
        openPalette("command");
        resetVimState();
        return true;
      }
    }
    return false;
  }
  var VIM_SHORTCUT_SECTIONS = [
    {
      title: "Modes & Motions",
      items: [
        [["h", "j", "k", "l"], "Move left, down, up, right"],
        [["w", "b"], "Next / previous word boundary"],
        [["0", "^", "$"], "Start of line / first non-blank / end of line"],
        [["gg", "G"], "First line / last line (or [count]gg / [count]G)"],
        [["Ctrl+d", "Ctrl+u"], "Scroll half-page down / up"],
        [["Ctrl+f", "Ctrl+b"], "Scroll full-page down / up"],
        [["zz", "zt", "zb"], "Center line / line to top / line to bottom"],
        [["H", "M", "L"], "Move to top, middle, bottom visible line"]
      ]
    },
    {
      title: "Code Intelligence & LSP",
      items: [
        [["gd"], "Go to Definition (replaces F12)"],
        [["gr"], "Find References across workspace (replaces Shift+F12)"],
        [["K"], "Show hover documentation & signatures"],
        [["gh"], "Call Trail (callers / callees)"],
        [["Ctrl+o", "Ctrl+i"], "Jump back / forward in navigation history"]
      ]
    },
    {
      title: "Search & Occurrences",
      items: [
        [["/"], "Find in file (forward)"],
        [["?"], "Find in file (backward)"],
        [["n", "N"], "Next / previous match"],
        [["*", "#"], "Search current word under cursor forward / backward"],
        [["Esc"], "Clear highlights, search, and occurrences"]
      ]
    },
    {
      title: "Visual Mode & AI Agent Actions",
      items: [
        [["v"], "Character-wise visual selection"],
        [["V"], "Line-wise visual selection"],
        [["e", "c"], "Edit selection inline with AI coding agent"],
        [["y"], "Yank (copy) code to clipboard"],
        [["Y"], "Yank reference (file:line-range)"],
        [["u"], "Find usages of selected symbol"],
        [["Esc"], "Cancel selection and return to Normal mode"]
      ]
    },
    {
      title: "Tabs & Commands",
      items: [
        [["gt", "gT"], "Next tab / previous tab"],
        [["[N]gt"], "Switch to tab N"],
        [[":"], "Open Command Palette"]
      ]
    }
  ];
  function showVimHelp() {
    let modal = $("#vim-helpsheet");
    if (!modal) {
      modal = document.createElement("div");
      modal.id = "vim-helpsheet";
      document.body.appendChild(modal);
    }
    const isChecked = vimEnabled ? "checked" : "";
    modal.innerHTML = `
    <div class="help-card vim-help-card">
      <div class="help-header vim-help-header">
        <div class="vim-help-title">
          <h2>Vim Keybindings</h2>
          <span class="help-version">Modal Navigation</span>
        </div>
        <div class="vim-toggle-row">
          <label class="vim-switch-label">
            <input type="checkbox" id="vim-toggle-input" ${isChecked}>
            <span class="vim-switch-slider"></span>
            <span class="vim-switch-text">${vimEnabled ? "Enabled" : "Disabled"}</span>
          </label>
          <button id="btn-close-vim-help" class="mini" title="Close (Esc)">✕</button>
        </div>
      </div>
      <div class="vim-help-content">
        ${VIM_SHORTCUT_SECTIONS.map((sec) => `
          <div class="vim-help-section">
            <div class="vim-sec-title">${esc2(sec.title)}</div>
            <dl class="help-grid vim-help-grid">
              ${sec.items.map(([combos, v]) => `
                <dt>${combos.map(keyCaps).filter(Boolean).join('<span class="key-or">/</span>')}</dt>
                <dd>${esc2(v)}</dd>
              `).join("")}
            </dl>
          </div>
        `).join("")}
      </div>
      <div class="vim-help-footer">
        <button id="btn-switch-to-std-help" class="settings-btn-link" title="View Standard Shortcuts (?)">View Standard Shortcuts (?)</button>
        <span class="agent-hint">Press Esc or click outside to dismiss</span>
      </div>
    </div>
  `;
    modal.hidden = false;
    const toggleInput = modal.querySelector("#vim-toggle-input");
    if (toggleInput) {
      toggleInput.addEventListener("change", (e) => {
        const active = e.target.checked;
        setVimModeEnabled(active, true);
        const txt = modal.querySelector(".vim-switch-text");
        if (txt)
          txt.textContent = active ? "Enabled" : "Disabled";
        showToast("✓", active ? "Vim mode enabled" : "Vim mode disabled");
      });
    }
    modal.querySelector("#btn-close-vim-help")?.addEventListener("click", closeVimHelp);
    modal.querySelector("#btn-switch-to-std-help")?.addEventListener("click", () => {
      closeVimHelp();
      showHelp();
    });
    modal.addEventListener("click", (e) => {
      if (e.target === modal)
        closeVimHelp();
    });
  }
  function closeVimHelp() {
    const modal = $("#vim-helpsheet");
    if (modal)
      modal.hidden = true;
  }
  function initVim() {
    let initial = false;
    try {
      const val = localStorage.getItem("px0.editor.vimMode");
      if (val === "true")
        initial = true;
    } catch {}
    if (S2.settings && S2.settings["editor.vimMode"] !== undefined) {
      initial = S2.settings["editor.vimMode"] === true || S2.settings["editor.vimMode"] === "true";
    }
    setVimModeEnabled(initial, false);
    const chip = $("#st-vim");
    if (chip) {
      chip.addEventListener("click", () => {
        showVimHelp();
      });
    }
    const helpBtn = $("#btn-vim-help");
    if (helpBtn) {
      helpBtn.addEventListener("click", () => {
        showVimHelp();
      });
    }
  }

  // web/src/settings.js
  var settingsModalEl = null;
  var BUILTIN_SCHEMA = [
    {
      key: "editor.fontSize",
      title: "Font Size",
      description: "Controls the font size in pixels for the code viewer.",
      category: "Text Editor",
      type: "number",
      default: 13.5,
      min: 9,
      max: 32,
      step: 0.5
    },
    {
      key: "editor.fontFamily",
      title: "Font Family",
      description: "Controls the font family used in the code viewer.",
      category: "Text Editor",
      type: "string",
      default: '"JetBrains Mono", "Fira Code", "Cascadia Code", "SF Mono", Menlo, Consolas, ui-monospace, monospace'
    },
    {
      key: "editor.lineHeight",
      title: "Line Height",
      description: "Controls the line height in pixels for the code viewer.",
      category: "Text Editor",
      type: "number",
      default: 21,
      min: 14,
      max: 48,
      step: 1
    },
    {
      key: "editor.tabSize",
      title: "Tab Size",
      description: "The number of spaces a tab is equal to.",
      category: "Text Editor",
      type: "select",
      default: 4,
      options: ["2", "4", "8"]
    },
    {
      key: "editor.wordWrap",
      title: "Word Wrap",
      description: "Controls whether lines should wrap around or scroll horizontally.",
      category: "Text Editor",
      type: "select",
      default: "on",
      options: ["on", "off"]
    },
    {
      key: "editor.lineNumbers",
      title: "Line Numbers",
      description: "Controls the display of line numbers in the gutter.",
      category: "Text Editor",
      type: "select",
      default: "on",
      options: ["on", "off"]
    },
    {
      key: "editor.vimMode",
      title: "Vim Keybindings",
      description: "Enable Vim modal navigation (Normal mode, Visual mode, motions, search, and LSP shortcuts).",
      category: "Text Editor",
      type: "boolean",
      default: false
    },
    {
      key: "editor.cursorStyle",
      title: "Cursor Style",
      description: "Controls the cursor style in the code viewer.",
      category: "Text Editor",
      type: "select",
      default: "line",
      options: ["line", "block", "underline"]
    },
    {
      key: "editor.cursorBlinking",
      title: "Cursor Blinking",
      description: "Controls the cursor animation style.",
      category: "Text Editor",
      type: "select",
      default: "smooth",
      options: ["blink", "smooth", "solid"]
    },
    {
      key: "editor.renderLineHighlight",
      title: "Render Line Highlight",
      description: "Controls how the editor should render the current line highlight.",
      category: "Text Editor",
      type: "select",
      default: "line",
      options: ["line", "none"]
    },
    {
      key: "editor.occurrencesHighlight",
      title: "Occurrences Highlight",
      description: "Controls whether the editor should highlight occurrences of the selected word.",
      category: "Text Editor",
      type: "boolean",
      default: true
    },
    {
      key: "editor.scrollBeyondLastLine",
      title: "Scroll Beyond Last Line",
      description: "Controls whether the editor will scroll beyond the last line of the file.",
      category: "Text Editor",
      type: "boolean",
      default: true
    },
    {
      key: "editor.bracketPairColorization",
      title: "Bracket Pair Colorization",
      description: "Controls whether bracket pair colorization and matching is enabled.",
      category: "Text Editor",
      type: "boolean",
      default: true
    },
    {
      key: "editor.renderWhitespace",
      title: "Render Whitespace",
      description: "Controls how whitespace characters are rendered in the viewer.",
      category: "Text Editor",
      type: "select",
      default: "selection",
      options: ["none", "boundary", "selection", "all"]
    },
    {
      key: "editor.minimap.enabled",
      title: "Minimap Hits",
      description: "Controls whether search hit indicators are shown in the scroll minimap gutter.",
      category: "Text Editor",
      type: "boolean",
      default: true
    },
    {
      key: "workbench.colorTheme",
      title: "Color Theme",
      description: "Specifies the color theme used in the workbench.",
      category: "Workbench",
      type: "select",
      default: "github-dark",
      options: [
        "github-dark",
        "dark",
        "light",
        "catppuccin-mocha",
        "catppuccin-latte",
        "dracula",
        "gruvbox-dark",
        "gruvbox-light",
        "monokai",
        "nord",
        "one-dark",
        "rose-pine",
        "solarized-dark",
        "solarized-light"
      ]
    },
    {
      key: "diffEditor.renderSideBySide",
      title: "Diff Side By Side",
      description: "Controls whether the diff editor shows changes in split (side-by-side) or unified mode.",
      category: "Workbench",
      type: "boolean",
      default: true
    },
    {
      key: "diffEditor.ignoreTrimWhitespace",
      title: "Diff: Ignore Trim Whitespace",
      description: "Controls whether the diff viewer ignores changes in leading or trailing whitespace.",
      category: "Git & Diff",
      type: "boolean",
      default: true
    },
    {
      key: "git.gutterIndicators",
      title: "Git Gutter Indicators",
      description: "Controls whether changed line indicators are shown in the editor gutter.",
      category: "Git & Diff",
      type: "boolean",
      default: true
    },
    {
      key: "markdown.preview.open",
      title: "Markdown Preview",
      description: "Controls whether Markdown files open in rendered preview by default.",
      category: "Workbench",
      type: "boolean",
      default: true
    },
    {
      key: "explorer.compactFolders",
      title: "Compact Folders",
      description: "Controls whether the file tree renders single-child directory chains compactly.",
      category: "Files & Explorer",
      type: "boolean",
      default: true
    },
    {
      key: "explorer.autoReveal",
      title: "Auto Reveal Active File",
      description: "Controls whether the file explorer automatically scrolls to and reveals active tabs.",
      category: "Files & Explorer",
      type: "boolean",
      default: true
    },
    {
      key: "files.exclude",
      title: "Files Exclude Patterns",
      description: "Configure glob patterns for excluding files and folders from search and trees.",
      category: "Files & Explorer",
      type: "string",
      default: "**/.git, **/node_modules, **/target, **/.DS_Store"
    },
    {
      key: "search.smartCase",
      title: "Smart Case Search",
      description: "Searches case-insensitively when query is lowercase, and case-sensitively when uppercase characters exist.",
      category: "Search",
      type: "boolean",
      default: true
    },
    {
      key: "search.maxResults",
      title: "Max Search Results",
      description: "Controls the maximum number of results returned in workspace-wide searches.",
      category: "Search",
      type: "number",
      default: 1000,
      min: 50,
      max: 1e4,
      step: 50
    },
    {
      key: "lsp.enabled",
      title: "Language Server Protocol (LSP)",
      description: "Master switch for language server integrations (definitions, references, diagnostics).",
      category: "LSP & Intelligence",
      type: "boolean",
      default: true
    },
    {
      key: "lsp.hover.enabled",
      title: "Hover Documentation",
      description: "Controls whether hovercards with documentation and type signatures appear on hover.",
      category: "LSP & Intelligence",
      type: "boolean",
      default: true
    },
    {
      key: "agent.harness",
      title: "Coding Harness",
      description: "Coding agent harness invoked for code edits (e.g. claude, gemini, cursor-agent, agy, opencode, codex, aider, goose).",
      category: "Agent / AI",
      type: "string",
      default: ""
    },
    {
      key: "agent.timeoutSeconds",
      title: "Agent Timeout (Seconds)",
      description: "Controls the maximum execution time in seconds for agent edits before canceling.",
      category: "Agent / AI",
      type: "number",
      default: 120,
      min: 10,
      max: 600,
      step: 10
    },
    {
      key: "agent.autoAcceptEdits",
      title: "Auto Accept Agent Edits",
      description: "Controls whether agent-generated code diffs are accepted without manual confirmation.",
      category: "Agent / AI",
      type: "boolean",
      default: false
    }
  ];
  var settingsData = {
    settings: {},
    defaults: Object.fromEntries(BUILTIN_SCHEMA.map((s) => [s.key, s.default])),
    schema: BUILTIN_SCHEMA,
    raw: `{
}
`,
    path: "~/.px0/settings.json"
  };
  var activeSettingsCategory = "Commonly Used";
  var settingsViewMode = "ui";
  var settingsFilterQuery = "";
  var COMMONLY_USED_KEYS = new Set([
    "editor.fontSize",
    "workbench.colorTheme",
    "editor.wordWrap",
    "editor.lineNumbers",
    "editor.vimMode",
    "editor.tabSize",
    "diffEditor.renderSideBySide",
    "editor.cursorStyle",
    "explorer.autoReveal",
    "search.smartCase",
    "lsp.hover.enabled",
    "agent.harness"
  ]);
  async function loadSettings() {
    try {
      const data = await api("/api/settings");
      if (data && data.schema && data.schema.length > 0) {
        settingsData = data;
      } else if (data) {
        settingsData.settings = data.settings || {};
        settingsData.raw = data.raw || settingsData.raw;
        settingsData.path = data.path || settingsData.path;
        if (data.defaults)
          settingsData.defaults = { ...settingsData.defaults, ...data.defaults };
      }
      S2.settings = settingsData.settings || {};
      return settingsData;
    } catch (err) {
      console.warn("Using built-in settings schema (offline/fallback):", err);
      return settingsData;
    }
  }
  function applySettingLive(key, val) {
    if (!S2.settings)
      S2.settings = {};
    S2.settings[key] = val;
    switch (key) {
      case "editor.fontSize":
      case "editor.fontFamily":
      case "editor.lineHeight":
      case "editor.tabSize": {
        const fs = parseFloat(S2.settings["editor.fontSize"]) || 13.5;
        const ff = S2.settings["editor.fontFamily"] || "";
        const lh = parseFloat(S2.settings["editor.lineHeight"]) || 21;
        const ts = parseInt(S2.settings["editor.tabSize"], 10) || 4;
        applyEditorTypography(fs, ff, lh, ts);
        break;
      }
      case "editor.wordWrap": {
        const on = val === "on" || val === true;
        toggleWordWrap(on);
        break;
      }
      case "editor.lineNumbers": {
        const on = val === "on" || val === true;
        toggleLineNumbers(on);
        break;
      }
      case "editor.cursorStyle": {
        document.body.classList.remove("cursor-block", "cursor-underline");
        if (val === "block")
          document.body.classList.add("cursor-block");
        else if (val === "underline")
          document.body.classList.add("cursor-underline");
        break;
      }
      case "editor.cursorBlinking": {
        document.body.classList.remove("cursor-blink-smooth", "cursor-blink-solid", "cursor-blink-blink");
        if (val === "solid")
          document.body.classList.add("cursor-blink-solid");
        else if (val === "blink")
          document.body.classList.add("cursor-blink-blink");
        else
          document.body.classList.add("cursor-blink-smooth");
        break;
      }
      case "editor.renderLineHighlight": {
        document.body.classList.toggle("no-line-highlight", val === "none");
        break;
      }
      case "editor.scrollBeyondLastLine": {
        document.body.classList.toggle("no-scroll-beyond", val === false || val === "false");
        break;
      }
      case "git.gutterIndicators": {
        document.body.classList.toggle("hide-git-gutter", val === false || val === "false");
        break;
      }
      case "editor.minimap.enabled": {
        const minimap = $("#minimap-hits");
        if (minimap)
          minimap.style.display = val === false || val === "false" ? "none" : "";
        break;
      }
      case "workbench.colorTheme": {
        if (val)
          setTheme(val, true);
        break;
      }
      case "diffEditor.renderSideBySide": {
        const split = val === true || val === "true";
        setLayoutPref(split ? "split" : "unified");
        break;
      }
      case "markdown.preview.open": {
        S2.mdPreview = val === true || val === "true";
        try {
          localStorage.setItem("px0.mdPreview", S2.mdPreview ? "true" : "false");
        } catch {}
        break;
      }
      case "editor.vimMode": {
        setVimModeEnabled(val === true || val === "true", false);
        break;
      }
    }
  }
  function applyAllSettingsLive() {
    if (!S2.settings)
      return;
    for (const [k, v] of Object.entries(S2.settings)) {
      applySettingLive(k, v);
    }
  }
  function openSettings(mode = "ui") {
    if (!settingsModalEl)
      initSettingsDOM();
    settingsViewMode = mode === "json" ? "json" : "ui";
    settingsModalEl.hidden = false;
    updateSettingsHeader();
    if (settingsViewMode === "json") {
      showSettingsJSONView();
    } else {
      showSettingsUIView();
    }
    loadSettings().then(() => {
      updateSettingsHeader();
      if (settingsViewMode === "json") {
        showSettingsJSONView();
      } else {
        showSettingsUIView();
      }
    });
    const searchInput = $("#settings-search");
    if (searchInput && settingsViewMode === "ui") {
      setTimeout(() => searchInput.focus(), 50);
    }
  }
  function closeSettings() {
    if (settingsModalEl)
      settingsModalEl.hidden = true;
  }
  function isSettingsOpen() {
    return settingsModalEl && !settingsModalEl.hidden;
  }
  function updateSettingsHeader() {
    const pathEl = $("#settings-path");
    if (pathEl && settingsData.path) {
      pathEl.textContent = settingsData.path;
      pathEl.title = "Click to copy path: " + settingsData.path;
    }
    const btnUI = $("#settings-mode-ui");
    const btnJSON = $("#settings-mode-json");
    if (btnUI && btnJSON) {
      btnUI.classList.toggle("active", settingsViewMode === "ui");
      btnJSON.classList.toggle("active", settingsViewMode === "json");
    }
  }
  function showSettingsUIView() {
    settingsViewMode = "ui";
    updateSettingsHeader();
    $("#settings-ui-container").hidden = false;
    $("#settings-json-container").hidden = true;
    $("#settings-search-bar").hidden = false;
    renderSettingsNav();
    renderSettingsList();
  }
  function showSettingsJSONView() {
    settingsViewMode = "json";
    updateSettingsHeader();
    $("#settings-ui-container").hidden = true;
    $("#settings-json-container").hidden = false;
    $("#settings-search-bar").hidden = true;
    const rawEditor = $("#settings-raw-editor");
    if (rawEditor) {
      rawEditor.value = settingsData.raw || `{
}
`;
      rawEditor.focus();
    }
    const errEl = $("#settings-raw-error");
    if (errEl)
      errEl.hidden = true;
  }
  function getSettingCategories() {
    const cats = ["Commonly Used"];
    const seen = new Set(cats);
    for (const item of settingsData.schema || []) {
      const cat = item.category || item.Category;
      if (cat && !seen.has(cat)) {
        cats.push(cat);
        seen.add(cat);
      }
    }
    return cats;
  }
  function renderSettingsNav() {
    const nav = $("#settings-nav");
    if (!nav)
      return;
    const cats = getSettingCategories();
    nav.innerHTML = cats.map((cat) => {
      const active = cat === activeSettingsCategory ? " active" : "";
      return `<button class="settings-nav-item${active}" data-cat="${esc2(cat)}">${esc2(cat)}</button>`;
    }).join("");
  }
  function isSettingModified(key, val, defVal) {
    if (val === undefined || val === null)
      return false;
    if (defVal === undefined || defVal === null)
      return val !== "";
    if (typeof defVal === "number") {
      return parseFloat(val) !== parseFloat(defVal);
    }
    if (typeof defVal === "boolean") {
      return Boolean(val) !== Boolean(defVal);
    }
    return String(val) !== String(defVal);
  }
  function renderSettingsList() {
    const container = $("#settings-list");
    if (!container)
      return;
    const q = settingsFilterQuery.trim().toLowerCase();
    const schema = settingsData.schema || [];
    const currentSettings = settingsData.settings || {};
    const defaults = settingsData.defaults || {};
    let items = schema;
    if (q) {
      items = schema.filter((s) => {
        const title = (s.title || s.Title || "").toLowerCase();
        const key = (s.key || s.Key || "").toLowerCase();
        const desc = (s.description || s.Description || "").toLowerCase();
        const cat = (s.category || s.Category || "").toLowerCase();
        return title.includes(q) || key.includes(q) || desc.includes(q) || cat.includes(q);
      });
    } else if (activeSettingsCategory === "Commonly Used") {
      items = schema.filter((s) => COMMONLY_USED_KEYS.has(s.key || s.Key));
    } else {
      items = schema.filter((s) => (s.category || s.Category) === activeSettingsCategory);
    }
    if (items.length === 0) {
      container.innerHTML = `<div class="settings-empty">No matching settings found for "${esc2(q || activeSettingsCategory)}".</div>`;
      return;
    }
    const html = items.map((item) => {
      const key = item.key || item.Key;
      const title = item.title || item.Title || key;
      const desc = item.description || item.Description || "";
      const cat = item.category || item.Category || "General";
      const type = item.type || item.Type || "string";
      const itemDef = item.default !== undefined ? item.default : item.Default;
      const def = defaults[key] !== undefined ? defaults[key] : itemDef;
      const val = currentSettings[key] !== undefined ? currentSettings[key] : def;
      const modified = isSettingModified(key, currentSettings[key], def);
      const modClass = modified ? " is-modified" : "";
      let controlHtml = "";
      let aptValuesHtml = "";
      if (type === "boolean") {
        const checked = val === true || val === "true" ? "checked" : "";
        controlHtml = `
        <label class="settings-switch">
          <input type="checkbox" data-key="${esc2(key)}" ${checked}>
          <span class="settings-slider"></span>
        </label>`;
        const isT = val === true || val === "true";
        aptValuesHtml = `
        <div class="settings-apt-bar">
          <span class="settings-apt-label">Allowed Values:</span>
          <div class="settings-apt-pills">
            <button type="button" class="settings-pill-tag${isT ? " active" : ""}" data-set-key="${esc2(key)}" data-set-val="true" title="Set to true">true</button>
            <button type="button" class="settings-pill-tag${!isT ? " active" : ""}" data-set-key="${esc2(key)}" data-set-val="false" title="Set to false">false</button>
          </div>
        </div>`;
      } else if (type === "select") {
        const opts = item.options || item.Options || [];
        const optHtml = opts.map((o) => {
          const sel = String(o) === String(val) ? "selected" : "";
          return `<option value="${esc2(o)}" ${sel}>${esc2(o)}</option>`;
        }).join("");
        controlHtml = `<select class="settings-select" data-key="${esc2(key)}">${optHtml}</select>`;
        const pills = opts.map((o) => {
          const isSel = String(o) === String(val);
          return `<button type="button" class="settings-pill-tag${isSel ? " active" : ""}" data-set-key="${esc2(key)}" data-set-val="${esc2(String(o))}" title="Select ${esc2(String(o))}">${esc2(String(o))}</button>`;
        }).join("");
        aptValuesHtml = `
        <div class="settings-apt-bar">
          <span class="settings-apt-label">Options:</span>
          <div class="settings-apt-pills">
            ${pills}
          </div>
        </div>`;
      } else if (type === "number") {
        const min = item.min !== undefined ? item.min : item.Min;
        const max = item.max !== undefined ? item.max : item.Max;
        const step = item.step !== undefined ? item.step : item.Step;
        const minAttr = min !== undefined ? `min="${min}"` : "";
        const maxAttr = max !== undefined ? `max="${max}"` : "";
        const stepAttr = step !== undefined ? `step="${step}"` : 'step="1"';
        controlHtml = `<input type="number" class="settings-input settings-input-num" data-key="${esc2(key)}" value="${esc2(String(val))}" ${minAttr} ${maxAttr} ${stepAttr}>`;
        let numberPresets = [];
        if (key === "editor.fontSize")
          numberPresets = [12, 13, 13.5, 14, 16, 18];
        else if (key === "editor.lineHeight")
          numberPresets = [18, 20, 21, 24, 28];
        else if (key === "search.maxResults")
          numberPresets = [200, 500, 1000, 5000];
        else if (key === "agent.timeoutSeconds")
          numberPresets = [60, 120, 180, 300];
        const presetPills = numberPresets.length ? `
        <span class="settings-apt-label">Presets:</span>
        <div class="settings-apt-pills">
          ${numberPresets.map((n) => {
          const isSel = Number(val) === n;
          return `<button type="button" class="settings-pill-tag${isSel ? " active" : ""}" data-set-key="${esc2(key)}" data-set-val="${n}">${n}</button>`;
        }).join("")}
        </div>` : "";
        aptValuesHtml = `
        <div class="settings-apt-bar">
          <span class="settings-tag tag-range">Min: <b>${min !== undefined ? min : "—"}</b></span>
          <span class="settings-tag tag-range">Max: <b>${max !== undefined ? max : "—"}</b></span>
          ${step !== undefined ? `<span class="settings-tag tag-step">Step: <b>${step}</b></span>` : ""}
          ${presetPills}
        </div>`;
      } else {
        controlHtml = `<input type="text" class="settings-input" data-key="${esc2(key)}" value="${esc2(String(val || ""))}">`;
        let stringPresets = [];
        if (key === "agent.harness") {
          stringPresets = ["claude", "gemini", "cursor-agent", "agy", "aider"];
        }
        const presetPills = stringPresets.length ? `
        <div class="settings-apt-bar">
          <span class="settings-apt-label">Suggestions:</span>
          <div class="settings-apt-pills">
            ${stringPresets.map((s) => {
          const isSel = String(val) === s;
          return `<button type="button" class="settings-pill-tag${isSel ? " active" : ""}" data-set-key="${esc2(key)}" data-set-val="${esc2(s)}">${esc2(s)}</button>`;
        }).join("")}
          </div>
        </div>` : "";
        aptValuesHtml = presetPills;
      }
      const resetBtn = modified ? `<button class="settings-reset-btn" data-reset="${esc2(key)}" title="Reset to default (${esc2(String(def))})">Reset</button>` : "";
      const extraAction = key === "editor.vimMode" ? `
      <div style="margin: 6px 0 2px;">
        <button type="button" class="settings-btn-link btn-vim-cheatsheet-trigger" style="cursor:pointer;font-size:11.5px;display:inline-flex;align-items:center;gap:4px;color:var(--accent-fg);">
          <span>View Vim Keybindings Cheat Sheet</span><kbd class="footer-kbd" style="font-size:10px;">?</kbd>
        </button>
      </div>` : "";
      return `
      <div class="settings-card${modClass}" data-setting="${esc2(key)}">
        <div class="settings-card-left">
          <div class="settings-card-header">
            <span class="settings-card-title">${esc2(title)}</span>
            <span class="settings-card-key">${esc2(key)}</span>
            <span class="settings-tag tag-cat">${esc2(cat)}</span>
            <span class="settings-tag tag-type">${esc2(type)}</span>
          </div>
          <div class="settings-card-desc">${esc2(desc)}</div>
          ${aptValuesHtml}
          ${extraAction}
          <div class="settings-card-meta">
            <span class="settings-tag tag-current">Current: <b>${esc2(String(val))}</b></span>
            <span class="settings-tag tag-default">Default: <code>${esc2(String(def))}</code></span>
            ${modified ? `<span class="settings-tag tag-modified">Modified</span>` : ""}
            ${resetBtn}
          </div>
        </div>
        <div class="settings-card-right">
          ${controlHtml}
        </div>
      </div>
    `;
    }).join("");
    container.innerHTML = html;
  }
  async function handleSettingChange(key, value) {
    if (!settingsData.settings)
      settingsData.settings = {};
    settingsData.settings[key] = value;
    applySettingLive(key, value);
    renderSettingsList();
    try {
      const res = await apiPost("/api/settings", { [key]: value });
      if (res.raw)
        settingsData.raw = res.raw;
    } catch (err) {
      console.error(`Failed to save setting ${key}:`, err);
    }
  }
  async function handleResetSetting(key) {
    const def = settingsData.defaults ? settingsData.defaults[key] : undefined;
    if (def !== undefined) {
      await handleSettingChange(key, def);
    }
  }
  async function handleSaveRawSettings() {
    const rawEditor = $("#settings-raw-editor");
    const errEl = $("#settings-raw-error");
    if (!rawEditor)
      return;
    const rawText = rawEditor.value;
    try {
      JSON.parse(rawText);
      if (errEl)
        errEl.hidden = true;
    } catch (err) {
      if (errEl) {
        errEl.textContent = "JSON Syntax Error: " + err.message;
        errEl.hidden = false;
      }
      return;
    }
    try {
      const res = await apiPost("/api/settings", { raw: rawText });
      if (res.settings) {
        settingsData.settings = res.settings;
        S2.settings = res.settings;
        applyAllSettingsLive();
      }
      if (res.raw)
        settingsData.raw = res.raw;
      if (errEl) {
        errEl.textContent = "Settings saved successfully.";
        errEl.hidden = false;
        errEl.classList.add("success");
        setTimeout(() => {
          errEl.hidden = true;
          errEl.classList.remove("success");
        }, 2500);
      }
    } catch (err) {
      if (errEl) {
        errEl.textContent = "Failed to save: " + err.message;
        errEl.hidden = false;
      }
    }
  }
  function initSettingsDOM() {
    settingsModalEl = $("#settings-modal");
    if (!settingsModalEl)
      return;
    $("#settings-close")?.addEventListener("click", closeSettings);
    settingsModalEl.addEventListener("click", (e) => {
      if (e.target === settingsModalEl)
        closeSettings();
    });
    $("#settings-mode-ui")?.addEventListener("click", () => showSettingsUIView());
    $("#settings-mode-json")?.addEventListener("click", () => showSettingsJSONView());
    $("#settings-path")?.addEventListener("click", () => {
      if (settingsData.path) {
        navigator.clipboard.writeText(settingsData.path);
        const toast = $("#toast");
        if (toast) {
          toast.textContent = "Copied settings path to clipboard";
          toast.hidden = false;
          setTimeout(() => {
            toast.hidden = true;
          }, 2000);
        }
      }
    });
    const searchInput = $("#settings-search");
    if (searchInput) {
      searchInput.addEventListener("input", (e) => {
        settingsFilterQuery = e.target.value;
        renderSettingsList();
      });
      $("#settings-search-clear")?.addEventListener("click", () => {
        searchInput.value = "";
        settingsFilterQuery = "";
        renderSettingsList();
        searchInput.focus();
      });
    }
    $("#settings-nav")?.addEventListener("click", (e) => {
      const btn = e.target.closest(".settings-nav-item");
      if (!btn)
        return;
      activeSettingsCategory = btn.dataset.cat;
      settingsFilterQuery = "";
      if (searchInput)
        searchInput.value = "";
      renderSettingsNav();
      renderSettingsList();
    });
    const listEl2 = $("#settings-list");
    if (listEl2) {
      listEl2.addEventListener("change", (e) => {
        const target2 = e.target;
        const key = target2.dataset.key;
        if (!key)
          return;
        let value;
        if (target2.type === "checkbox") {
          value = target2.checked;
        } else if (target2.type === "number") {
          value = parseFloat(target2.value);
        } else {
          value = target2.value;
        }
        handleSettingChange(key, value);
      });
      listEl2.addEventListener("click", (e) => {
        const pill = e.target.closest(".settings-pill-tag");
        if (pill) {
          const key = pill.dataset.setKey;
          let value = pill.dataset.setVal;
          if (value === "true")
            value = true;
          else if (value === "false")
            value = false;
          else if (!isNaN(Number(value)) && value.trim() !== "")
            value = Number(value);
          if (key)
            handleSettingChange(key, value);
          return;
        }
        const resetBtn = e.target.closest(".settings-reset-btn");
        if (resetBtn) {
          const key = resetBtn.dataset.reset;
          if (key)
            handleResetSetting(key);
          return;
        }
        const vimHelpBtn = e.target.closest(".btn-vim-cheatsheet-trigger");
        if (vimHelpBtn) {
          showVimHelp();
          return;
        }
      });
    }
    $("#btn-settings-save-raw")?.addEventListener("click", handleSaveRawSettings);
    $("#btn-settings-reset-raw")?.addEventListener("click", () => {
      const rawEditor = $("#settings-raw-editor");
      if (rawEditor)
        rawEditor.value = settingsData.raw || `{
}
`;
      const errEl = $("#settings-raw-error");
      if (errEl)
        errEl.hidden = true;
    });
  }
  function initSettings() {
    initSettingsDOM();
    loadSettings().then(() => {
      applyAllSettingsLive();
    });
  }

  // web/src/agent.js
  var box = $("#agentbox");
  var tpl = $("#agentbox-tpl");
  var agentListEl = $("#agentbox-list");
  var batchBar = $("#agent-batch-bar");
  var batchCount = $("#agent-batch-count");
  var batchClear = $("#agent-batch-clear");
  var batchHarness = $("#agent-batch-harness");
  var batchModel = $("#agent-batch-model");
  var batchHint = $("#agent-batch-hint");
  var batchApply = $("#agent-batch-apply");
  var batchCancel = $("#agent-batch-cancel");
  var batchErr = $("#agent-batch-err");
  var sessions = new Map;
  var agentSeq = 0;
  var batchTimer = null;
  var batchJobId = null;
  var batchElapsed = "";
  var activeBatchTargets = null;
  var installed = () => (S2.meta?.agents || []).filter((h) => h.installed);
  var chosen = () => S2.meta && S2.meta.agent || "";
  var chosenModel = () => S2.meta && S2.meta.agentModel || "";
  var targetRef = ({ path, l1, l2 }) => path + ":" + (l1 === l2 ? l1 : l1 + "-" + l2);
  var rangesOverlap = (a, b) => a.path === b.path && a.l1 <= b.l2 && b.l1 <= a.l2;
  function applyAgentMeta() {
    for (const session of sessions.values()) {
      updateSessionMeta(session);
    }
    syncBatchMeta();
  }
  function updateSessionMeta(session) {
    if (!session.harnessSelect || !session.modelSelect)
      return;
    const ready = (S2.meta?.agents || []).filter((h) => h.installed);
    const currentHarness = chosen();
    const currentModel = chosenModel();
    session.harnessSelect.innerHTML = "";
    if (!ready.length) {
      const opt = document.createElement("option");
      opt.value = "";
      opt.textContent = "no harness";
      session.harnessSelect.appendChild(opt);
      session.harnessSelect.disabled = true;
      session.modelSelect.innerHTML = "";
      session.modelSelect.hidden = true;
      return;
    }
    for (const h of ready) {
      const opt = document.createElement("option");
      opt.value = h.name;
      opt.textContent = h.name;
      if (h.name === currentHarness)
        opt.selected = true;
      session.harnessSelect.appendChild(opt);
    }
    const isBusy = session.el.classList.contains("busy");
    session.harnessSelect.disabled = isBusy || !!(S2.meta && S2.meta.agentPinned);
    session.harnessSelect.title = S2.meta && S2.meta.agentPinned ? "Fixed for this run by -agent" : "Change the coding harness";
    const activeH = ready.find((h) => h.name === (session.harnessSelect.value || currentHarness)) || ready[0];
    session.modelSelect.innerHTML = "";
    const models = activeH?.models || [];
    if (models.length > 0) {
      for (const m of models) {
        const opt = document.createElement("option");
        opt.value = m;
        opt.textContent = m;
        if (m === currentModel)
          opt.selected = true;
        session.modelSelect.appendChild(opt);
      }
      session.modelSelect.hidden = false;
      session.modelSelect.disabled = isBusy;
      session.modelSelect.title = "Model for " + activeH.name;
    } else {
      session.modelSelect.hidden = true;
    }
  }
  async function loadAgentAsync() {
    try {
      const j = await api("/api/agent/harnesses");
      S2.meta.agents = j.harnesses || [];
      S2.meta.agent = j.selected || S2.meta.agent || "";
      S2.meta.agentModel = j.model || S2.meta.agentModel || "";
      S2.meta.agentPinned = !!j.pinned;
      applyAgentMeta();
    } catch {}
  }
  function syncBatchMeta() {
    if (!batchHarness || !batchModel)
      return;
    const ready = installed();
    const currentHarness = chosen();
    const currentModel = chosenModel();
    batchHarness.innerHTML = "";
    if (!ready.length) {
      const opt = document.createElement("option");
      opt.value = "";
      opt.textContent = "no harness";
      batchHarness.appendChild(opt);
      batchHarness.disabled = true;
      batchModel.innerHTML = "";
      batchModel.hidden = true;
      return;
    }
    for (const h of ready) {
      const opt = document.createElement("option");
      opt.value = h.name;
      opt.textContent = h.name;
      if (h.name === currentHarness)
        opt.selected = true;
      batchHarness.appendChild(opt);
    }
    const isBusy = !!batchJobId;
    batchHarness.disabled = isBusy || !!(S2.meta && S2.meta.agentPinned);
    batchHarness.title = S2.meta && S2.meta.agentPinned ? "Fixed for this run by -agent" : "Change the coding harness";
    const activeH = ready.find((h) => h.name === (batchHarness.value || currentHarness)) || ready[0];
    batchModel.innerHTML = "";
    const models = activeH?.models || [];
    if (models.length > 0) {
      for (const m of models) {
        const opt = document.createElement("option");
        opt.value = m;
        opt.textContent = m;
        if (m === currentModel)
          opt.selected = true;
        batchModel.appendChild(opt);
      }
      batchModel.hidden = false;
      batchModel.disabled = isBusy;
      batchModel.title = "Model for " + activeH.name;
    } else {
      batchModel.hidden = true;
    }
  }
  function getReadySessions() {
    return [...sessions.values()].filter((s) => !s.timer && !s.jobId);
  }
  function syncBatchBar() {
    if (!batchBar)
      return;
    const total = sessions.size;
    const ready = getReadySessions();
    const readyCount = ready.length;
    const runningCount = total - readyCount;
    box.classList.toggle("has-batch", total >= 2);
    if (total >= 2 || batchJobId) {
      batchBar.hidden = false;
      if (batchJobId) {
        if (batchCount) {
          batchCount.textContent = readyCount > 0 ? readyCount + " remaining (" + (activeBatchTargets?.length || 0) + " in batch)" : (activeBatchTargets?.length || 0) + " in batch";
        }
        if (batchApply)
          batchApply.hidden = true;
        if (batchCancel)
          batchCancel.hidden = false;
      } else {
        if (batchCount) {
          if (runningCount > 0) {
            batchCount.textContent = readyCount + " remaining (" + runningCount + " running)";
          } else {
            batchCount.textContent = readyCount + " comments";
          }
        }
        if (batchApply) {
          batchApply.hidden = false;
          batchApply.disabled = readyCount === 0;
          const btnLabel = runningCount > 0 ? "Apply Remaining (" + readyCount + ")" : "Apply All (" + readyCount + ")";
          batchApply.textContent = btnLabel;
          batchApply.title = btnLabel + " (" + keyLabel("Mod+Enter") + ")";
        }
        if (batchCancel)
          batchCancel.hidden = true;
        if (batchHint) {
          batchHint.textContent = readyCount > 0 ? keyLabel("Mod+Enter") + " to apply " + (runningCount > 0 ? "remaining" : "all") : runningCount > 0 ? runningCount + " running..." : "";
        }
      }
      syncBatchMeta();
    } else {
      batchBar.hidden = true;
    }
  }
  function anyInFlight() {
    if (batchTimer || batchJobId)
      return true;
    for (const s of sessions.values())
      if (s.timer || s.jobId)
        return true;
    return false;
  }
  function syncBoxVisibility() {
    box.hidden = sessions.size === 0;
    syncBatchBar();
  }
  function openAgentEdit(info) {
    if (!info)
      return;
    for (const s of sessions.values()) {
      if (rangesOverlap(s.target, info)) {
        showToast("!", "Overlaps the edit already open on " + targetRef(s.target));
        return;
      }
    }
    const session = createSession(info);
    sessions.set(session.id, session);
    syncAgentTargets();
    applyAgentMeta();
    syncBoxVisibility();
    if (chosen() && installed().some((h) => h.name === chosen())) {
      showCompose(session);
    } else {
      showPicker(session);
    }
  }
  function syncAgentTargets() {
    S2.agentTargets = [...sessions.values()].map((s) => ({
      id: s.id,
      path: s.target.path,
      l1: s.target.l1,
      l2: s.target.l2
    }));
    render();
    syncDiffAgentTargets();
  }
  function createSession(info) {
    const el = tpl.content.firstElementChild.cloneNode(true);
    const parent = agentListEl || box;
    const existing = [...parent.children];
    let inserted = false;
    for (const child of existing) {
      const s = [...sessions.values()].find((sess) => sess.el === child);
      if (s && s.target) {
        if (s.target.path === info.path && s.target.l1 > info.l1) {
          parent.insertBefore(el, child);
          inserted = true;
          break;
        }
      }
    }
    if (!inserted) {
      parent.appendChild(el);
    }
    const session = {
      id: ++agentSeq,
      target: info,
      timer: null,
      jobId: null,
      harness: "",
      el,
      refEl: el.querySelector(".agent-ref"),
      metaEl: el.querySelector(".agent-meta"),
      harnessSelect: el.querySelector(".agent-harness-select"),
      modelSelect: el.querySelector(".agent-model-select"),
      closeBtn: el.querySelector(".agent-close"),
      pickEl: el.querySelector(".agent-pick"),
      composeEl: el.querySelector(".agent-compose"),
      input: el.querySelector(".agent-input"),
      sendBtn: el.querySelector(".agent-send"),
      cancelBtn: el.querySelector(".agent-cancel"),
      hintEl: el.querySelector(".agent-hint"),
      errEl: el.querySelector(".agent-err")
    };
    wireSession(session);
    refreshRef(session);
    setBusy(session, false);
    resetHint(session);
    clearErr(session);
    session.input.value = "";
    el.scrollIntoView({ block: "nearest", behavior: "smooth" });
    session.input.focus();
    return session;
  }
  function wireSession(session) {
    session.sendBtn.addEventListener("click", () => submit(session));
    session.cancelBtn?.addEventListener("click", () => cancelSession(session));
    session.closeBtn.addEventListener("click", () => closeAgentEdit(session));
    if (session.refEl) {
      session.refEl.addEventListener("click", () => {
        openFile(session.target.path, { line: session.target.l1 });
      });
    }
    if (session.harnessSelect) {
      session.harnessSelect.addEventListener("change", async () => {
        const hName = session.harnessSelect.value;
        if (!hName)
          return;
        await select(hName, (msg) => showErr(session, msg));
        session.input.focus();
      });
    }
    if (session.modelSelect) {
      session.modelSelect.addEventListener("change", async () => {
        const hName = session.harnessSelect?.value || chosen();
        const mName = session.modelSelect.value;
        await select(hName, mName, (msg) => showErr(session, msg));
        session.input.focus();
      });
    }
    session.el.addEventListener("keydown", (e) => {
      e.stopPropagation();
      if (e.key === "Escape") {
        e.preventDefault();
        if (session.timer || session.jobId) {
          cancelSession(session);
        } else {
          closeAgentEdit(session);
        }
      } else if ((e[MOD] || e.metaKey || e.ctrlKey) && e.key === "Enter") {
        e.preventDefault();
        submitBatch();
      } else if (e.key === "Enter" && !e.shiftKey && !session.composeEl.hidden && !session.timer && !session.jobId) {
        e.preventDefault();
        submit(session);
      }
    });
  }
  async function cancelSession(session) {
    if (!session.timer && !session.jobId)
      return;
    if (session.timer) {
      clearTimeout(session.timer);
      session.timer = null;
    }
    const jobId = session.jobId;
    session.jobId = null;
    setBusy(session, false);
    resetHint(session);
    syncBatchBar();
    refreshStatusNote();
    showToast("!", "Cancelled edit on " + targetRef(session.target));
    if (jobId) {
      try {
        await apiPost("/api/agent/cancel", { id: jobId });
      } catch {}
    }
    session.input.focus();
  }
  function closeAgentEdit(session) {
    if (session.timer || session.jobId) {
      cancelSession(session);
    }
    sessions.delete(session.id);
    session.el.remove();
    syncBoxVisibility();
    syncAgentTargets();
  }
  function refreshRef(session) {
    const ref = targetRef(session.target);
    session.refEl.textContent = ref;
    session.refEl.title = ref + " (click to jump)";
  }
  function clearErr(session) {
    const errEl = session.errEl;
    if (!errEl)
      return;
    errEl.textContent = "";
    errEl.hidden = true;
  }
  function attachAlreadyRunningCancel(errContainer) {
    const row = document.createElement("div");
    row.className = "agent-err-actions";
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "agent-err-cancel-btn";
    btn.textContent = "Cancel in-flight edit";
    btn.title = "Stop and cancel running edits on the server";
    btn.onclick = async (e) => {
      e.preventDefault();
      e.stopPropagation();
      btn.disabled = true;
      btn.textContent = "Cancelling...";
      try {
        await apiPost("/api/agent/cancel", { id: 0 });
        showToast("✓", "Cancelled running edit");
        errContainer.hidden = true;
      } catch (err) {
        showToast("!", "Failed to cancel: " + err.message);
        btn.disabled = false;
        btn.textContent = "Cancel in-flight edit";
      }
    };
    row.appendChild(btn);
    errContainer.appendChild(row);
  }
  function showErr(session, msg, streams = []) {
    const errEl = session.errEl;
    if (!errEl)
      return;
    errEl.textContent = "";
    const head = document.createElement("div");
    head.className = "agent-err-msg";
    head.textContent = msg;
    errEl.appendChild(head);
    if (msg && msg.includes("already running")) {
      attachAlreadyRunningCancel(errEl);
    }
    for (const [label, text] of streams) {
      if (!text)
        continue;
      const name = document.createElement("div");
      name.className = "agent-err-label";
      name.textContent = label;
      const pre = document.createElement("pre");
      pre.className = "agent-err-out";
      pre.textContent = text;
      errEl.append(name, pre);
    }
    errEl.hidden = false;
  }
  function resetHint(session) {
    if (!session.hintEl)
      return;
    session.hintEl.textContent = "Enter to send, " + keyLabel("Mod+Enter") + " all, Esc to cancel";
  }
  function setBusy(session, busy, msg) {
    session.el.classList.toggle("busy", busy);
    session.input.disabled = busy;
    if (session.sendBtn)
      session.sendBtn.hidden = busy;
    if (session.cancelBtn)
      session.cancelBtn.hidden = !busy;
    session.closeBtn.disabled = false;
    if (session.harnessSelect)
      session.harnessSelect.disabled = busy || !!(S2.meta && S2.meta.agentPinned);
    if (session.modelSelect)
      session.modelSelect.disabled = busy;
    if (session.hintEl && msg)
      session.hintEl.textContent = msg;
  }
  function showCompose(session) {
    session.pickEl.hidden = true;
    if (session.metaEl)
      session.metaEl.hidden = false;
    session.composeEl.hidden = false;
    session.input.focus();
  }
  async function showPicker(session) {
    session.composeEl.hidden = true;
    if (session.metaEl)
      session.metaEl.hidden = true;
    session.pickEl.hidden = false;
    session.pickEl.innerHTML = '<div class="hint">Looking for coding harnesses…</div>';
    let list = S2.meta?.agents || [];
    let settingsPath = "";
    try {
      const j = await api("/api/agent/harnesses");
      list = j.harnesses || [];
      settingsPath = j.settings || "";
      S2.meta.agents = list;
      S2.meta.agent = j.selected || "";
      S2.meta.agentModel = j.model || "";
      S2.meta.agentPinned = !!j.pinned;
    } catch (e) {
      session.pickEl.innerHTML = '<div class="hint">Could not look for harnesses: ' + esc2(e.message) + "</div>";
      return;
    }
    const ready = list.filter((h) => h.installed);
    if (!ready.length) {
      showToast("!", "Could not find any coding harness like Claude Code, OpenCode, Codex, Antigravity, Aider, etc. Install one and restart px0.", 6000);
      session.pickEl.innerHTML = '<div class="hint" style="line-height: 1.5; padding: 4px 2px;">' + "Could not find any coding harness like <b>Claude Code</b>, <b>OpenCode</b>, <b>Codex</b>, <b>Antigravity</b> (<code>agy</code>), <b>Aider</b>, <b>Goose</b>, <b>Gemini CLI</b>, or <b>Cursor Agent</b>.<br><br>" + "Please install a coding harness, make sure it is on your <code>PATH</code>, and restart px0 after that.</div>";
      return;
    }
    session.pickEl.innerHTML = '<div class="hint">This harness will edit files in this workspace.</div>' + optionsHtml(ready, settingsPath);
    session.pickEl.querySelectorAll("[data-pick]").forEach((b) => {
      b.addEventListener("click", () => pick(session, b.dataset.pick));
    });
    session.pickEl.querySelectorAll(".agent-model-select").forEach((sel) => {
      sel.addEventListener("change", async (e) => {
        e.stopPropagation();
        await select(sel.dataset.harness, sel.value, (msg) => showErr(session, msg));
        showPicker(session);
      });
    });
  }
  function optionsHtml(ready, settingsPath) {
    let html = "";
    for (const h of ready) {
      const isSelected = h.name === chosen();
      html += '<div class="agent-opt-wrap">' + '<button class="agent-opt' + (isSelected ? " on" : "") + '" data-pick="' + esc2(h.name) + '">' + '<span class="agent-opt-name">' + esc2(h.name) + "</span>" + '<code class="agent-opt-cmd">' + esc2(h.cmd) + "</code></button>";
      if (isSelected && h.models && h.models.length > 0) {
        html += '<div class="agent-model-row">' + '<span class="agent-model-label">Model:</span>' + '<select class="agent-model-select" data-harness="' + esc2(h.name) + '">';
        for (const m of h.models) {
          const sel = m === (h.model || chosenModel()) ? " selected" : "";
          html += '<option value="' + esc2(m) + '"' + sel + ">" + esc2(m) + "</option>";
        }
        html += "</select></div>";
      }
      html += "</div>";
    }
    if (settingsPath)
      html += '<div class="agent-note">Remembered in ' + esc2(settingsPath) + "</div>";
    return html;
  }
  async function pick(session, name) {
    if (await select(name, (msg) => showErr(session, msg)))
      showCompose(session);
  }
  async function select(name, model, onError) {
    if (typeof model === "function") {
      onError = model;
      model = "";
    }
    try {
      const params = { name };
      if (model)
        params.model = model;
      const j = await apiPost("/api/agent/select", params);
      S2.meta.agent = j.selected || "";
      S2.meta.agentModel = j.model || "";
      S2.meta.agents = j.harnesses || S2.meta.agents;
      S2.meta.agentPinned = !!j.pinned;
    } catch (e) {
      if (onError)
        onError(e.message);
      return false;
    }
    applyAgentMeta();
    return true;
  }
  async function submit(session) {
    if (session.timer)
      return;
    clearErr(session);
    const instruction = session.input.value.trim();
    if (!instruction || !session.target)
      return;
    const params = { path: session.target.path, l1: session.target.l1, l2: session.target.l2, instruction };
    let job2;
    try {
      job2 = await apiPost("/api/agent/edit", params);
    } catch (e) {
      showErr(session, e.message);
      return;
    }
    session.jobId = job2.id;
    session.harness = job2.harness;
    hideSelectionBar();
    const initialNote = "Editing with " + (chosenModel() ? chosen() + " (" + chosenModel() + ")" : chosen()) + "...";
    setBusy(session, true, initialNote);
    syncBatchBar();
    refreshStatusNote();
    session.timer = setTimeout(() => tick(session), 400);
  }
  async function tick(session) {
    if (!session.jobId)
      return;
    let j;
    try {
      j = await api("/api/agent/job?id=" + session.jobId);
    } catch (e) {
      if (!session.jobId)
        return;
      session.timer = null;
      if (e.body && "running" in e.body) {
        await finish(session, e.body);
        refreshStatusNote();
        return;
      }
      setBusy(session, false);
      resetHint(session);
      syncBatchBar();
      refreshStatusNote();
      showErr(session, e.message);
      return;
    }
    if (!session.jobId)
      return;
    if (j.running) {
      session.harness = j.harness;
      session.elapsed = Math.round((j.ms || 0) / 1000) + "s";
      setBusy(session, true, "Editing with " + j.harness + "... " + session.elapsed);
      refreshStatusNote();
      session.timer = setTimeout(() => tick(session), 600);
      return;
    }
    session.timer = null;
    await finish(session, j);
    refreshStatusNote();
  }
  function setBatchBusy(busy, msg) {
    if (!batchBar)
      return;
    batchBar.classList.toggle("busy", busy);
    if (batchApply)
      batchApply.hidden = busy;
    if (batchCancel)
      batchCancel.hidden = !busy;
    if (batchClear)
      batchClear.disabled = busy;
    if (batchHarness)
      batchHarness.disabled = busy || !!(S2.meta && S2.meta.agentPinned);
    if (batchModel)
      batchModel.disabled = busy;
    if (batchHint) {
      if (msg)
        batchHint.textContent = msg;
      else
        batchHint.textContent = keyLabel("Mod+Enter") + " to apply all";
    }
  }
  function clearBatchErr() {
    if (!batchErr)
      return;
    batchErr.textContent = "";
    batchErr.hidden = true;
  }
  function showBatchErr(msg, streams = []) {
    if (!batchErr)
      return;
    batchErr.textContent = "";
    const head = document.createElement("div");
    head.className = "agent-err-msg";
    head.textContent = msg;
    batchErr.appendChild(head);
    if (msg && msg.includes("already running")) {
      attachAlreadyRunningCancel(batchErr);
    }
    for (const [label, text] of streams) {
      if (!text)
        continue;
      const name = document.createElement("div");
      name.className = "agent-err-label";
      name.textContent = label;
      const pre = document.createElement("pre");
      pre.className = "agent-err-out";
      pre.textContent = text;
      batchErr.append(name, pre);
    }
    batchErr.hidden = false;
  }
  async function submitBatch() {
    if (batchTimer || batchJobId)
      return;
    clearBatchErr();
    const ready = getReadySessions();
    const targets = [];
    for (const s of ready) {
      const ins = s.input.value.trim();
      if (ins && s.target) {
        targets.push({ session: s, item: { path: s.target.path, l1: s.target.l1, l2: s.target.l2, instruction: ins } });
      }
    }
    if (!targets.length) {
      if (ready.length > 0) {
        showToast("!", "Please enter an instruction for the remaining comment(s)");
        ready[0].input.focus();
      } else {
        showToast("!", "All open edits are already in progress");
      }
      return;
    }
    if (targets.length === 1) {
      submit(targets[0].session);
      return;
    }
    const harnessName = chosen();
    if (!harnessName) {
      showToast("!", "Please select a coding harness first");
      return;
    }
    let job2;
    try {
      job2 = await apiPostJson("/api/agent/batch", { edits: targets.map((t) => t.item) });
    } catch (e) {
      showBatchErr(e.message);
      return;
    }
    batchJobId = job2.id;
    activeBatchTargets = targets;
    batchElapsed = "";
    hideSelectionBar();
    const initialNote = "Batch editing " + targets.length + " items with " + (chosenModel() ? chosen() + " (" + chosenModel() + ")" : chosen()) + "...";
    setBatchBusy(true, initialNote);
    for (const t of targets) {
      setBusy(t.session, true, "Applying in batch...");
    }
    syncBatchBar();
    refreshStatusNote();
    batchTimer = setTimeout(() => tickBatch(targets), 400);
  }
  async function tickBatch(targets) {
    if (!batchJobId)
      return;
    let j;
    try {
      j = await api("/api/agent/job?id=" + batchJobId);
    } catch (e) {
      if (!batchJobId)
        return;
      batchTimer = null;
      if (e.body && "running" in e.body) {
        await finishBatch(targets, e.body);
        refreshStatusNote();
        return;
      }
      setBatchBusy(false);
      for (const t of targets) {
        setBusy(t.session, false);
        resetHint(t.session);
      }
      syncBatchBar();
      refreshStatusNote();
      showBatchErr(e.message);
      return;
    }
    if (!batchJobId)
      return;
    if (j.running) {
      batchElapsed = Math.round((j.ms || 0) / 1000) + "s";
      setBatchBusy(true, "Applying " + targets.length + " edits with " + j.harness + "... " + batchElapsed);
      refreshStatusNote();
      batchTimer = setTimeout(() => tickBatch(targets), 600);
      return;
    }
    batchTimer = null;
    await finishBatch(targets, j);
    refreshStatusNote();
  }
  async function finishBatch(targets, j) {
    const currentTargets = targets;
    batchJobId = null;
    batchElapsed = "";
    activeBatchTargets = null;
    setBatchBusy(false);
    if (j.error) {
      for (const t of currentTargets) {
        setBusy(t.session, false);
        resetHint(t.session);
      }
      syncBatchBar();
      showBatchErr((j.harness || "agent") + ": " + j.error, [
        ["stderr", (j.stderr || "").trim()],
        ["stdout", (j.stdout || j.log || "").trim()]
      ]);
      if (j.changed?.length)
        reloadWorkspace(null);
      return;
    }
    for (const t of currentTargets) {
      sessions.delete(t.session.id);
      t.session.el.remove();
    }
    syncBoxVisibility();
    syncAgentTargets();
    const changed = j.changed || [];
    if (!changed.length && j.tracked !== false) {
      showToast("✓", "Finished batch edit with no file changes");
      return;
    }
    const focusTarget = currentTargets[0]?.session?.target;
    if (!await reloadWorkspace(focusTarget, "Batch edited"))
      return;
    showToast("✓", !changed.length ? "Reloaded workspace" : changed.length === 1 ? "Updated " + changed[0] + " (" + currentTargets.length + " edits)" : "Updated " + changed.length + " files across " + currentTargets.length + " edits");
  }
  async function cancelBatch() {
    if (!batchTimer && !batchJobId)
      return;
    if (batchTimer) {
      clearTimeout(batchTimer);
      batchTimer = null;
    }
    const id = batchJobId;
    batchJobId = null;
    batchElapsed = "";
    setBatchBusy(false);
    if (activeBatchTargets) {
      for (const t of activeBatchTargets) {
        setBusy(t.session, false);
        resetHint(t.session);
      }
    }
    activeBatchTargets = null;
    syncBatchBar();
    refreshStatusNote();
    showToast("!", "Cancelled batch edit");
    if (id) {
      try {
        await apiPost("/api/agent/cancel", { id });
      } catch {}
    }
  }
  function clearAllEdits() {
    if (batchJobId)
      cancelBatch();
    for (const s of [...sessions.values()]) {
      closeAgentEdit(s);
    }
  }
  function refreshStatusNote() {
    const busySessions = [...sessions.values()].filter((s) => s.timer || s.jobId);
    const batchCount2 = batchJobId ? activeBatchTargets?.length || 0 : 0;
    const individualBusy = busySessions.filter((s) => !activeBatchTargets?.some((t) => t.session === s));
    if (batchJobId && individualBusy.length > 0) {
      setStatusNote("Batch editing " + batchCount2 + " items + " + individualBusy.length + " edit running... " + (batchElapsed || ""));
    } else if (batchJobId) {
      setStatusNote("Batch editing " + batchCount2 + " items with " + chosen() + "... " + (batchElapsed || ""));
    } else if (!individualBusy.length) {
      setStatusNote("");
    } else if (individualBusy.length === 1) {
      const s = individualBusy[0];
      setStatusNote("Editing with " + (s.harness || chosen()) + "... " + (s.elapsed || ""));
    } else {
      setStatusNote(individualBusy.length + " edits running...");
    }
  }
  async function finish(session, j) {
    const editTarget = session.target;
    if (j.error) {
      setBusy(session, false);
      resetHint(session);
      showErr(session, (j.harness || "agent") + ": " + j.error, [
        ["stderr", (j.stderr || "").trim()],
        ["stdout", (j.stdout || j.log || "").trim()]
      ]);
      if (j.changed?.length)
        reloadWorkspace(null);
      return;
    }
    sessions.delete(session.id);
    session.el.remove();
    syncBoxVisibility();
    syncAgentTargets();
    const changed = j.changed || [];
    if (!changed.length && j.tracked !== false) {
      showToast("✓", "Finished with no file changes");
      return;
    }
    if (!await reloadWorkspace(editTarget, "Edited"))
      return;
    showToast("✓", !changed.length ? "Reloaded the workspace" : changed.length === 1 ? "Updated " + changed[0] : "Updated " + changed.length + " files");
  }
  var reloadChain = Promise.resolve();
  function reloadWorkspace(focus, what = "Changed") {
    const run = async () => {
      try {
        await api("/api/reindex");
        await reloadOpenTabs();
        if (focus?.path) {
          await openFile(focus.path, { line: focus.l1, push: false });
        }
        await refreshTree();
      } catch (e) {
        showToast("!", what + ", but the reload failed: " + e.message);
        return false;
      }
      return true;
    };
    const result = reloadChain.then(run, run);
    reloadChain = result.then(() => {}, () => {});
    return result;
  }
  function initAgent() {
    if (!box || !tpl)
      return;
    setAgentHandler(openAgentEdit);
    if (batchApply)
      batchApply.addEventListener("click", () => submitBatch());
    if (batchCancel)
      batchCancel.addEventListener("click", () => cancelBatch());
    if (batchClear)
      batchClear.addEventListener("click", () => clearAllEdits());
    if (batchHarness) {
      batchHarness.addEventListener("change", async () => {
        const hName = batchHarness.value;
        if (!hName)
          return;
        await select(hName, (msg) => showBatchErr(msg));
      });
    }
    if (batchModel) {
      batchModel.addEventListener("change", async () => {
        const hName = batchHarness?.value || chosen();
        const mName = batchModel.value;
        await select(hName, mName, (msg) => showBatchErr(msg));
      });
    }
    document.addEventListener("click", (e) => {
      const row = e.target.closest(".row.agent-sel, .row.agent-anchor, .diff-row.agent-sel, .diff-row.agent-anchor, .diff-side.agent-sel, .diff-side.agent-anchor");
      if (!row)
        return;
      const line = +row.dataset.l;
      const d = S2.docs[S2.active];
      if (!d)
        return;
      for (const s of sessions.values()) {
        if (s.target.path === d.path && line >= s.target.l1 && line <= s.target.l2) {
          s.input.focus();
          s.el.scrollIntoView({ behavior: "smooth", block: "nearest" });
          break;
        }
      }
    });
    addEventListener("beforeunload", (e) => {
      if (!anyInFlight())
        return;
      e.preventDefault();
      e.returnValue = "";
    });
    api("/api/agent/job?id=0").then((j) => {
      if (j && j.running) {
        setStatusNote("In-flight edit running on " + (j.path || "workspace") + " (" + (j.harness || "agent") + ")", 6000);
      }
    }).catch(() => {});
  }

  // web/src/shortcuts.js
  var SHORTCUTS = [
    [["Mod+,"], "Open settings"],
    [["Mod+K"], "Quick search / palette"],
    [["Mod+P"], "Go to file"],
    [["Mod+Shift+P"], "Command palette"],
    [["Mod+Shift+O"], "Go to symbol"],
    [["Mod+Shift+F"], "Search in files"],
    [["Mod+Shift+R"], "Refresh workspace"],
    [["Mod+F"], "Find in file"],
    [["Mod+G"], "Go to line"],
    [["Mod+D"], "Toggle diff view (git)"],
    [["Alt+Z"], "Toggle word wrap"],
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
    [["Alt+C", "Alt+A"], "Copy selection ref / with context"],
    [["Alt+U"], "Find usages of selection"],
    [["Alt+E"], "Edit selection inline"],
    [["Right click"], "Selection actions at the pointer"],
    [["Mod+Home|Mod+Up", "Mod+End|Mod+Down"], "Top / bottom of file"],
    [["Home|Mod+Left", "End|Mod+Right"], "Start / end of line"],
    [["Left", "Right"], "Move caret along the line"],
    [["Esc"], "Dismiss"]
  ];
  function showHelp() {
    const h = $("#helpsheet");
    const ver = S2.meta?.version ? ` <span class="help-version">v${esc2(S2.meta.version)}</span>` : "";
    h.innerHTML = '<div class="help-card"><div class="help-header"><h2>Keyboard Shortcuts</h2>' + ver + '<button id="btn-switch-to-vim-help" class="settings-btn-link" style="margin-left:auto;font-size:12px;cursor:pointer;" title="View Vim Keybindings">View Vim Keybindings</button></div><dl class="help-grid">' + SHORTCUTS.map(([combos, v]) => "<dt>" + combos.map(keyCaps).filter(Boolean).join('<span class="key-or">/</span>') + "</dt>" + "<dd>" + esc2(v) + "</dd>").join("") + "</dl></div>";
    h.hidden = false;
    h.querySelector("#btn-switch-to-vim-help")?.addEventListener("click", (e) => {
      e.stopPropagation();
      h.hidden = true;
      showVimHelp();
    });
  }
  var inField = (el) => el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA");
  function initShortcuts() {
    $("#btn-theme")?.addEventListener("click", cycleTheme);
    $("#btn-settings")?.addEventListener("click", () => openSettings("ui"));
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
        showRightInspector("search");
        $("#q")?.select();
      } else if (act === "symbols")
        openPalette("symbol");
      else if (act === "find")
        openFind(S2.lastWord);
      else if (act === "goto")
        openPalette("line");
      else if (act === "wrap")
        toggleWordWrap();
      else if (act === "md-preview")
        togglePreview();
      else if (act === "palette")
        openPalette("command");
      else if (act === "settings")
        openSettings("ui");
      else if (act === "vim-help")
        showVimHelp();
      else if (act === "help")
        showHelp();
    });
    addEventListener("keydown", (e) => {
      const mod = e[MOD];
      if (e.key === "Escape") {
        const lb = $("#img-lightbox");
        if (lb && !lb.hidden) {
          lb.hidden = true;
          return;
        }
        if (!$("#vim-helpsheet")?.hidden) {
          closeVimHelp();
          return;
        }
        if (isSettingsOpen()) {
          closeSettings();
          return;
        }
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
      if (mod && (e.key === "," || e.key === "<")) {
        e.preventDefault();
        openSettings("ui");
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
        showRightInspector("search");
        $("#q")?.select();
        return;
      }
      if (mod && e.shiftKey && (e.key === "R" || e.key === "r")) {
        e.preventDefault();
        reindexWorkspace();
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
      if (e.altKey && !mod && !e.shiftKey && e.code === "KeyM") {
        e.preventDefault();
        togglePreview();
        return;
      }
      if (mod && !e.shiftKey && !e.altKey && e.key === "Enter") {
        const b = $("#agentbox");
        if (b && !b.hidden) {
          e.preventDefault();
          submitBatch();
          return;
        }
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
      if (handleVimKeyDown(e))
        return;
      if (e.key === "?") {
        e.preventDefault();
        showHelp();
        return;
      }
      const d = doc_();
      if (!d)
        return;
      if (d.isImage) {
        if (handleImageKey(e))
          e.preventDefault();
        return;
      }
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
      const shift = e.shiftKey;
      if (mod && e.key === "Home") {
        e.preventDefault();
        if (shift)
          caretToEdge(false, true);
        else
          toTop();
        return;
      }
      if (mod && e.key === "End") {
        e.preventDefault();
        if (shift)
          caretToEdge(true, true);
        else
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
        caretToEdge(e.key === "ArrowRight", shift);
        return;
      }
      if (mod && !isMac && (e.key === "ArrowLeft" || e.key === "ArrowRight")) {
        e.preventDefault();
        moveWord(e.key === "ArrowRight" ? 1 : -1, shift);
        return;
      }
      if (isMac && e.altKey && (e.key === "ArrowLeft" || e.key === "ArrowRight")) {
        e.preventDefault();
        moveWord(e.key === "ArrowRight" ? 1 : -1, shift);
        return;
      }
      if (!mod && (e.key === "ArrowDown" || e.key === "j")) {
        e.preventDefault();
        moveCursor(1, shift);
        return;
      }
      if (!mod && (e.key === "ArrowUp" || e.key === "k")) {
        e.preventDefault();
        moveCursor(-1, shift);
        return;
      }
      if (!mod && !e.altKey && e.key === "ArrowLeft") {
        e.preventDefault();
        moveCol(-1, shift);
        return;
      }
      if (!mod && !e.altKey && e.key === "ArrowRight") {
        e.preventDefault();
        moveCol(1, shift);
        return;
      }
      if (!mod && (e.key === "Home" || e.key === "End")) {
        e.preventDefault();
        caretToEdge(e.key === "End", shift);
        return;
      }
      if (e.key === "PageDown") {
        e.preventDefault();
        moveCursor(Math.floor(vp.clientHeight / LH2) - 2, shift);
        return;
      }
      if (e.key === "PageUp") {
        e.preventDefault();
        moveCursor(-(Math.floor(vp.clientHeight / LH2) - 2), shift);
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
    { name: withKeys("Preferences: Open Settings (UI) ({Mod+,})"), run: () => openSettings("ui") },
    { name: "Preferences: Open Settings (JSON)", run: () => openSettings("json") },
    { name: "Go to File…", run: () => openPalette("file") },
    { name: "Go to Symbol in File…", run: () => openPalette("symbol") },
    { name: "Go to Line…", run: () => openPalette("line") },
    { name: "Search in Files", run: () => showRightInspector("search") },
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
    { name: "Preferences: Toggle Vim Keybindings", run: () => setVimModeEnabled(!isVimEnabled(), true) },
    { name: "Help: Vim Keybindings Cheat Sheet", run: showVimHelp },
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
      return esc2(text);
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
      out += esc2(text[i]);
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
    palList.innerHTML = pal.items.map((it, i) => '<div class="pi' + (i === pal.sel ? " sel" : "") + '" data-i="' + i + '">' + '<span class="pn">' + (it.raw ? it.label : esc2(it.label)) + "</span>" + '<span class="pp">' + (it.raw ? it.sub : esc2(it.sub || "")) + "</span>" + (it.right ? '<span class="pr">' + esc2(it.right) + "</span>" : "") + "</div>").join("");
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

  // web/src/gitstream.js
  var eventSource = null;
  var reconnectTimer = null;
  function initGitStream() {
    if (!S2.meta?.git)
      return;
    connect();
    window.addEventListener("focus", () => {
      if (document.visibilityState === "visible") {
        if (!eventSource || eventSource.readyState === EventSource.CLOSED) {
          connect();
        }
        triggerRefresh();
      }
    });
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState === "hidden") {
        disconnect();
      } else {
        connect();
        triggerRefresh();
      }
    });
  }
  async function triggerRefresh() {
    if (!S2.meta?.git)
      return;
    try {
      const data = await apiPost("/api/git/refresh");
      await handleGitStatus(data);
    } catch (e) {}
  }
  function connect() {
    if (eventSource && eventSource.readyState !== EventSource.CLOSED)
      return;
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    try {
      eventSource = new EventSource("/api/git/stream");
      eventSource.addEventListener("git-status", async (e) => {
        try {
          const data = JSON.parse(e.data);
          await handleGitStatus(data);
        } catch (err) {}
      });
      eventSource.onerror = () => {
        disconnect();
        if (document.visibilityState === "visible") {
          reconnectTimer = setTimeout(connect, 3000);
        }
      };
    } catch (err) {}
  }
  function disconnect() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
  }
  async function handleGitStatus(data) {
    if (!data)
      return;
    if (data.gitChanges !== undefined)
      S2.meta.gitChanges = data.gitChanges;
    if (data.gitFiles !== undefined)
      S2.meta.gitFiles = data.gitFiles;
    updateSidebarToggleState();
    if (treeEl?.classList.contains("changed-only") && (!S2.meta?.gitChanges || S2.meta.gitChanges <= 0)) {
      await setSidebarMode("files");
    }
    const statuses = data.statuses || {};
    const dirtyDirs = data.dirtyDirs || {};
    await patchTreeGitStatus(statuses, dirtyDirs);
    for (let i = S2.tabs.length - 1;i >= 0; i--) {
      const t = S2.tabs[i];
      const code = statuses[t.path];
      const isDiff = !!code && code !== "U";
      const wasDiff = !!(t.diffMode || t.openedInDiffView);
      if (wasDiff && (t.diffAvailable || t.diffMode) && !isDiff) {
        closeTab(i);
      }
    }
    let tabsChanged = false;
    for (const t of S2.tabs) {
      const code = statuses[t.path];
      const isDiff = !!code && code !== "U";
      if (t.diffAvailable !== isDiff) {
        t.diffAvailable = isDiff;
        tabsChanged = true;
      }
    }
    if (tabsChanged) {
      drawTabs();
    }
    const curDoc = doc_();
    if (curDoc) {
      const curCode = statuses[curDoc.path];
      const hasDiff = !!curCode && curCode !== "U";
      if (curDoc.diffAvailable !== hasDiff || curCode) {
        curDoc.diffAvailable = hasDiff;
        await loadGutter(curDoc);
        render();
        if (curDoc.diffMode) {
          syncDiffView(true);
        }
        updateStatus();
      }
    }
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
  initAgent();
  initMetrics();
  initStatusFit();
  initSettings();
  initVim();
  initImageViewer();
  (async function boot() {
    try {
      initTheme();
      const wrapPref = localStorage.getItem("px0.wrap");
      S2.wrap = wrapPref !== null ? wrapPref === "true" : true;
      document.body.classList.toggle("word-wrap", S2.wrap);
      S2.lineNumbers = true;
      document.body.classList.remove("hide-lines");
      const mdPref = localStorage.getItem("px0.mdPreview");
      S2.mdPreview = mdPref !== null ? mdPref === "true" : true;
      updateEditorOptionControls();
    } catch {}
    applyKeyLabels();
    measure();
    S2.meta = await api("/api/meta");
    if (S2.meta.metrics)
      updateMetricsDisplay(S2.meta.metrics);
    updateSidebarToggleState();
    applyAgentMeta();
    document.title = S2.meta.name + " - px0";
    $("#root-name").textContent = S2.meta.name;
    $("#root-name").title = S2.meta.root;
    if (S2.meta.version) {
      const emptyVerEl = $("#empty-ver");
      if (emptyVerEl)
        emptyVerEl.textContent = "v" + S2.meta.version;
    }
    try {
      const savedDirs = JSON.parse(sessionStorage.getItem("px0.openDirs") || "[]");
      restoreOpenDirs(savedDirs);
    } catch {}
    await refreshTree();
    initGitStream();
    const hasGitChanges = !!(S2.meta?.git && S2.meta.gitChanges > 0);
    if (hasGitChanges) {
      await setSidebarMode("git");
    } else {
      setSidebarMode("files");
    }
    const params = new URLSearchParams(window.location.search);
    const initialPath = params.get("path");
    const initialLine = parseInt(params.get("line"), 10) || undefined;
    if (initialPath) {
      await openFile(initialPath, { line: initialLine });
      await revealFile(initialPath);
      try {
        const u = new URL(window.location.href);
        u.searchParams.delete("path");
        u.searchParams.delete("line");
        const cleanSearch = u.searchParams.toString();
        const cleanUrl = u.pathname + (cleanSearch ? "?" + cleanSearch : "") + u.hash;
        window.history.replaceState({}, "", cleanUrl);
      } catch {}
    } else {
      const restored = await restoreWorkspaceTabs();
      if (hasGitChanges) {
        const hasActiveDiff = S2.tabs[S2.active]?.diffAvailable;
        if (!hasActiveDiff) {
          const changedTabIdx = S2.tabs.findIndex((t) => t.diffAvailable);
          if (changedTabIdx >= 0) {
            switchTab(changedTabIdx);
          } else if (S2.meta.gitFiles && S2.meta.gitFiles.length > 0) {
            await openFile(S2.meta.gitFiles[0]);
            await revealFile(S2.meta.gitFiles[0]);
          }
        }
      }
    }
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
    loadAgentAsync();
  })();
})();
