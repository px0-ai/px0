(() => {
  // web/src/state.js
  var $ = (s, r = document) => r.querySelector(s);
  var $$ = (s, r = document) => [...r.querySelectorAll(s)];
  var esc = (s) => s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
  var request = async (method, path, params, opts = {}) => {
    const u = new URL(path, location.origin);
    for (const [k, v] of Object.entries(params || {}))
      if (v !== undefined && v !== "")
        u.searchParams.set(k, v);
    const r = await fetch(u, { method, ...opts });
    const j = await r.json();
    if (j.error)
      throw Object.assign(new Error(j.error), { body: j });
    return j;
  };
  var api = (path, params, opts) => request("GET", path, params, opts);
  var apiPost = (path, params, opts) => request("POST", path, params, opts);
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
        iconHtml = '<span class="toast-chip">' + esc(accentText) + "</span>";
      }
    }
    toastEl.innerHTML = iconHtml + '<span class="toast-msg">' + esc(text) + "</span>";
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
    const first = Math.max(0, Math.floor(top / LH) - OVERSCAN);
    const count = Math.ceil(vp.clientHeight / LH) + OVERSCAN * 2;
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
  var sidebarToggle = null;
  function setSidebarToggle(fn) {
    sidebarToggle = fn;
  }
  function showPanel(name) {
    document.body.classList.remove("side-hidden");
    layout();
    render();
  }
  var sideMenu = $("#side-menu");
  var sideMenuBtn = $("#btn-side-menu");
  function closeSideMenu() {
    if (sideMenu && !sideMenu.hidden)
      sideMenu.hidden = true;
    sideMenuBtn?.setAttribute("aria-expanded", "false");
  }
  function placeSideMenu(anchor) {
    const { offsetWidth: w, offsetHeight: h } = sideMenu;
    if (anchor.el) {
      const r = anchor.el.getBoundingClientRect();
      sideMenu.style.left = Math.max(4, Math.min(r.right - w, innerWidth - w - 4)) + "px";
      sideMenu.style.top = r.bottom + 4 + "px";
    } else {
      sideMenu.style.left = Math.max(4, anchor.x + w > innerWidth - 4 ? anchor.x - w : anchor.x) + "px";
      sideMenu.style.top = Math.max(4, anchor.y + h > innerHeight - 4 ? anchor.y - h : anchor.y) + "px";
    }
  }
  function openSideMenu(anchor) {
    if (!sideMenu)
      return;
    const onRight = document.body.classList.contains("side-right");
    sideMenu.replaceChildren();
    const item = document.createElement("button");
    item.className = "side-menu-item";
    item.setAttribute("role", "menuitem");
    item.textContent = onRight ? "Move Sidebar to Left" : "Move Sidebar to Right";
    sideMenu.append(item);
    sideMenu.hidden = false;
    placeSideMenu(anchor);
    sideMenuBtn?.setAttribute("aria-expanded", "true");
  }
  function initPanels() {
    $("#btn-reindex").addEventListener("click", async () => {
      const j = await api("/api/reindex");
      S2.meta.files = j.files;
      S2.meta.indexMs = j.indexMs;
      treeEl.innerHTML = "";
      openDirs.clear();
      await drawTree("", treeEl, 0);
      await reloadOpenTabs();
      updateStatus();
      showToast("✓", "Workspace reindexed");
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
        const raw = document.body.classList.contains("side-right") ? innerWidth - e.clientX : e.clientX;
        $("#side").style.width = Math.max(170, Math.min(620, raw)) + "px";
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
    sideMenuBtn?.addEventListener("click", (e) => {
      e.stopPropagation();
      if (sideMenu && !sideMenu.hidden)
        closeSideMenu();
      else
        openSideMenu({ el: sideMenuBtn });
    });
    document.addEventListener("contextmenu", (e) => {
      if (sideMenu && sideMenu.contains(e.target)) {
        e.preventDefault();
        return;
      }
      if (!e.target.closest("#side") || e.target.closest("a"))
        return;
      e.preventDefault();
      openSideMenu({ x: e.clientX, y: e.clientY });
    });
    addEventListener("keydown", (e) => {
      if (e.key === "Escape")
        closeSideMenu();
    });
    if (sideMenu) {
      sideMenu.addEventListener("mousedown", (e) => e.preventDefault());
      sideMenu.addEventListener("click", (e) => {
        if (!e.target.closest(".side-menu-item"))
          return;
        closeSideMenu();
        if (sidebarToggle)
          sidebarToggle();
        showToast("✓", "Sidebar moved to " + (document.body.classList.contains("side-right") ? "right" : "left"));
      });
      document.addEventListener("mousedown", (e) => {
        if (sideMenu.hidden)
          return;
        if (sideMenu.contains(e.target) || sideMenuBtn && sideMenuBtn.contains(e.target))
          return;
        closeSideMenu();
      }, true);
      addEventListener("resize", closeSideMenu);
      addEventListener("blur", closeSideMenu);
      document.addEventListener("scroll", closeSideMenu, true);
    }
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
        resultsEl.innerHTML = '<div class="hint">' + esc(e.message) + "</div>";
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
        const raw = document.body.classList.contains("side-right") ? e.clientX : window.innerWidth - e.clientX;
        const w = Math.max(200, Math.min(700, raw));
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
    const y = (d.cur - 1) * LH;
    if (y < vp.scrollTop)
      vp.scrollTop = y - LH;
    else if (y > vp.scrollTop + vp.clientHeight - LH * 2)
      vp.scrollTop = y - vp.clientHeight + LH * 3;
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
      hint("Click a function name in the editor, then press <b>" + esc(keyLabel("Alt+Shift+H")) + "</b>.");
      return;
    }
    const my = ++callSeq;
    T = null;
    $("#right-calls-target").textContent = at.word;
    hint('Tracing calls for "' + esc(at.word) + '"…');
    setStatusNote("call trail for " + at.word + "…", 8000);
    let j;
    try {
      j = await api("/api/lsp/calls", { path: d.path, line: at.line, col: at.col, wait: S2.lsp.state === "ready" ? 1e4 : 30000 });
    } catch (e) {
      if (my === callSeq) {
        updateStatus();
        setStatusNote("");
        hint('Could not trace "' + esc(at.word) + '": ' + esc(explain(e.message)));
      }
      return;
    }
    if (my !== callSeq)
      return;
    setLspState(j);
    updateStatus();
    setStatusNote("");
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
    hovercard.innerHTML = (j.signature ? '<div class="sig">' + j.signature + "</div>" : "") + (j.doc ? '<div class="doc">' + esc(j.doc) + "</div>" : "") + '<div class="actions">' + '<button id="hc-copy-ref" title="Copy file and line reference">Copy Ref</button>' + '<button id="hc-copy-ai" title="Copy snippet with file path and line numbers">Copy with Context</button>' + '<button id="hc-find-refs" title="Find all usages across codebase">Usages</button>' + '<button id="hc-calls" title="' + withKeys("Trace callers and callees ({Alt+Shift+H})") + '">Calls</button>' + "</div>" + '<div class="foot"><b>' + esc(j.server || "lsp") + "</b>" + "<span>" + withKeys("{Mod+Click} definition") + "</span>" + "<span>" + withKeys("{Shift+F12} references") + "</span></div>";
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
      el.classList.toggle("agent-sel", inAgent);
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
    el.innerHTML = esc(text || "") || "&nbsp;";
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
      setDiffMode(doc_()?.diffMode || layoutPref());
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
    const hasDiff = !!(d && d.diffAvailable);
    const isDiffOn = !!(d && d.diffMode);
    const currentLayout = d && d.diffMode || layoutPref();
    const dsw = $("#diff-switch");
    if (dsw) {
      dsw.hidden = !hasDiff;
      document.body.classList.toggle("diff-tab", hasDiff);
      const btn = $("#diff-btn");
      if (btn) {
        btn.classList.toggle("on", hasDiff && isDiffOn);
        btn.title = withKeys(`Show changes against HEAD, ${currentLayout === "unified" ? "unified" : "split"} ({Mod+D})`);
      }
      $("#diff-source")?.classList.toggle("on", hasDiff && !isDiffOn);
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
    if (!current)
      return false;
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

  // web/src/tabs.js
  var closedTabs = [];
  var MAX_CLOSED = 20;
  async function openFile(path, opts = {}) {
    const { line, push = true, col } = opts;
    let idx = S2.tabs.findIndex((t) => t.path === path);
    if (idx < 0) {
      let j;
      const start = line ? Math.max(0, Math.floor((line - 1) / CHUNK) * CHUNK) : 0;
      try {
        j = await api("/api/file", { path, start, count: CHUNK });
      } catch (e) {
        setStatusNote(path + ": " + e.message, 4000);
        return;
      }
      if (j.image) {
        showImage(path);
        return;
      }
      const hasDiff = !!j.diffAvailable;
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
        diffMode: hasDiff ? layoutPref() || "split" : null,
        diffAvailable: hasDiff,
        diffDismissed: false
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
    const prev = doc_();
    if (prev && prev !== S2.tabs[idx])
      prev.scrollTop = vp.scrollTop;
    if (prev !== S2.tabs[idx]) {
      clearSelectAll();
      clearFind();
    }
    S2.active = idx;
    const d = S2.tabs[idx];
    $("#empty").hidden = true;
    hideImage();
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
      if (j.available && d.diffMode === null && !d.diffDismissed) {
        d.diffMode = layoutPref() || "split";
        if (doc_() === d) {
          syncDiffView();
          syncPreview();
        }
      }
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
      if (j.image)
        continue;
      const keep = tgt.oldDoc;
      const hasDiff = !!j.diffAvailable;
      const newCur = Math.max(1, Math.min(keep.cur || 1, j.total));
      const diffMode = hasDiff ? keep.diffMode || null : null;
      const d = {
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
        diffScroll: keep === activeDoc && keep.diffMode ? diffScrollTop() : 0
      };
      for (let k = 0;k < j.lines.length; k++) {
        d.lines[j.start + k] = j.lines[k];
      }
      d.lsp = j.lsp || { state: "off", server: "" };
      S2.tabs[idx] = d;
      if (j.refine)
        refineChunk(d, tgt.start / CHUNK);
      loadGutter(d);
    }
    const d = doc_();
    if (d) {
      S2.lsp.state = d.lsp && d.lsp.state || "off";
      S2.lsp.server = d.lsp && d.lsp.server || "";
      S2.lsp.missing = d.lsp && d.lsp.missing || "";
      warmLSP(d);
      syncPreview();
      syncDiffView();
      layout();
      vp.scrollTop = d.scrollTop;
      render();
      if ($("#panel-outline")?.classList.contains("active"))
        loadOutline();
    }
    drawTabs();
    drawCrumbs();
    updateStatus();
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
    if (prev)
      prev.scrollTop = vp.scrollTop;
    S2.active = i;
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
      key: "workbench.sideBar.location",
      title: "Sidebar Position",
      description: "Controls which side of the editor the file explorer sidebar is shown on.",
      category: "Workbench",
      type: "select",
      default: "left",
      options: ["left", "right"]
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
    },
    {
      key: "telemetry.enabled",
      title: "Telemetry",
      description: "Enable anonymous usage metrics to help improve px0.",
      category: "Security & Privacy",
      type: "boolean",
      default: true
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
    "workbench.sideBar.location",
    "editor.wordWrap",
    "editor.lineNumbers",
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
      case "workbench.sideBar.location": {
        document.body.classList.toggle("side-right", val === "right");
        try {
          localStorage.setItem("px0.side", val === "right" ? "right" : "left");
        } catch {}
        layout();
        render();
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
      return `<button class="settings-nav-item${active}" data-cat="${esc(cat)}">${esc(cat)}</button>`;
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
      container.innerHTML = `<div class="settings-empty">No matching settings found for "${esc(q || activeSettingsCategory)}".</div>`;
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
          <input type="checkbox" data-key="${esc(key)}" ${checked}>
          <span class="settings-slider"></span>
        </label>`;
        const isT = val === true || val === "true";
        aptValuesHtml = `
        <div class="settings-apt-bar">
          <span class="settings-apt-label">Allowed Values:</span>
          <div class="settings-apt-pills">
            <button type="button" class="settings-pill-tag${isT ? " active" : ""}" data-set-key="${esc(key)}" data-set-val="true" title="Set to true">true</button>
            <button type="button" class="settings-pill-tag${!isT ? " active" : ""}" data-set-key="${esc(key)}" data-set-val="false" title="Set to false">false</button>
          </div>
        </div>`;
      } else if (type === "select") {
        const opts = item.options || item.Options || [];
        const optHtml = opts.map((o) => {
          const sel = String(o) === String(val) ? "selected" : "";
          return `<option value="${esc(o)}" ${sel}>${esc(o)}</option>`;
        }).join("");
        controlHtml = `<select class="settings-select" data-key="${esc(key)}">${optHtml}</select>`;
        const pills = opts.map((o) => {
          const isSel = String(o) === String(val);
          return `<button type="button" class="settings-pill-tag${isSel ? " active" : ""}" data-set-key="${esc(key)}" data-set-val="${esc(String(o))}" title="Select ${esc(String(o))}">${esc(String(o))}</button>`;
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
        controlHtml = `<input type="number" class="settings-input settings-input-num" data-key="${esc(key)}" value="${esc(String(val))}" ${minAttr} ${maxAttr} ${stepAttr}>`;
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
          return `<button type="button" class="settings-pill-tag${isSel ? " active" : ""}" data-set-key="${esc(key)}" data-set-val="${n}">${n}</button>`;
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
        controlHtml = `<input type="text" class="settings-input" data-key="${esc(key)}" value="${esc(String(val || ""))}">`;
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
          return `<button type="button" class="settings-pill-tag${isSel ? " active" : ""}" data-set-key="${esc(key)}" data-set-val="${esc(s)}">${esc(s)}</button>`;
        }).join("")}
          </div>
        </div>` : "";
        aptValuesHtml = presetPills;
      }
      const resetBtn = modified ? `<button class="settings-reset-btn" data-reset="${esc(key)}" title="Reset to default (${esc(String(def))})">Reset</button>` : "";
      return `
      <div class="settings-card${modClass}" data-setting="${esc(key)}">
        <div class="settings-card-left">
          <div class="settings-card-header">
            <span class="settings-card-title">${esc(title)}</span>
            <span class="settings-card-key">${esc(key)}</span>
            <span class="settings-tag tag-cat">${esc(cat)}</span>
            <span class="settings-tag tag-type">${esc(type)}</span>
          </div>
          <div class="settings-card-desc">${esc(desc)}</div>
          ${aptValuesHtml}
          <div class="settings-card-meta">
            <span class="settings-tag tag-current">Current: <b>${esc(String(val))}</b></span>
            <span class="settings-tag tag-default">Default: <code>${esc(String(def))}</code></span>
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
  async function updateSetting(key, value) {
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
      await updateSetting(key, def);
    }
  }
  function toggleSidebarPosition() {
    const cur = S2.settings && S2.settings["workbench.sideBar.location"] || "left";
    updateSetting("workbench.sideBar.location", cur === "right" ? "left" : "right");
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
    const listEl = $("#settings-list");
    if (listEl) {
      listEl.addEventListener("change", (e) => {
        const target = e.target;
        const key = target.dataset.key;
        if (!key)
          return;
        let value;
        if (target.type === "checkbox") {
          value = target.checked;
        } else if (target.type === "number") {
          value = parseFloat(target.value);
        } else {
          value = target.value;
        }
        updateSetting(key, value);
      });
      listEl.addEventListener("click", (e) => {
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
            updateSetting(key, value);
          return;
        }
        const resetBtn = e.target.closest(".settings-reset-btn");
        if (resetBtn) {
          const key = resetBtn.dataset.reset;
          if (key)
            handleResetSetting(key);
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
    setSidebarToggle(toggleSidebarPosition);
    initSettingsDOM();
    loadSettings().then(() => {
      applyAllSettingsLive();
    });
  }

  // web/src/shortcuts.js
  var SHORTCUTS = [
    [["Mod+,"], "Open settings"],
    [["Mod+K"], "Quick search / palette"],
    [["Mod+P"], "Go to file"],
    [["Mod+Shift+P"], "Command palette"],
    [["Mod+Shift+O"], "Go to symbol"],
    [["Mod+Shift+F"], "Search in files"],
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
    const ver = S2.meta?.version ? ` <span class="help-version">v${esc(S2.meta.version)}</span>` : "";
    h.innerHTML = '<div class="help-card"><div class="help-header"><h2>Keyboard Shortcuts</h2>' + ver + '</div><dl class="help-grid">' + SHORTCUTS.map(([combos, v]) => "<dt>" + combos.map(keyCaps).filter(Boolean).join('<span class="key-or">/</span>') + "</dt>" + "<dd>" + esc(v) + "</dd>").join("") + "</dl></div>";
    h.hidden = false;
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
      else if (act === "help")
        showHelp();
    });
    addEventListener("keydown", (e) => {
      const mod = e[MOD];
      if (e.key === "Escape") {
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
        moveCursor(Math.floor(vp.clientHeight / LH) - 2, shift);
        return;
      }
      if (e.key === "PageUp") {
        e.preventDefault();
        moveCursor(-(Math.floor(vp.clientHeight / LH) - 2), shift);
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
    { name: "Toggle Sidebar Position", run: toggleSidebarPosition },
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

  // web/src/agent.js
  var box = $("#agentbox");
  var tpl = $("#agentbox-tpl");
  var sessions = new Map;
  var agentSeq = 0;
  var installed = () => (S2.meta?.agents || []).filter((h) => h.installed);
  var chosen = () => S2.meta && S2.meta.agent || "";
  var chosenModel = () => S2.meta && S2.meta.agentModel || "";
  var targetRef = ({ path, l1, l2 }) => path + ":" + (l1 === l2 ? l1 : l1 + "-" + l2);
  var rangesOverlap = (a, b) => a.path === b.path && a.l1 <= b.l2 && b.l1 <= a.l2;
  function applyAgentMeta() {
    for (const session of sessions.values()) {
      updateSessionMeta(session);
    }
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
  function anyInFlight() {
    for (const s of sessions.values())
      if (s.timer)
        return true;
    return false;
  }
  function syncBoxVisibility() {
    box.hidden = sessions.size === 0;
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
    box.prepend(el);
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
    session.input.focus();
    return session;
  }
  function wireSession(session) {
    session.sendBtn.addEventListener("click", () => submit(session));
    session.cancelBtn?.addEventListener("click", () => cancelSession(session));
    session.closeBtn.addEventListener("click", () => closeAgentEdit(session));
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
      } else if (e.key === "Enter" && !e.shiftKey && !session.composeEl.hidden && !session.timer) {
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
    session.refEl.title = ref;
  }
  function clearErr(session) {
    const errEl = session.errEl;
    if (!errEl)
      return;
    errEl.textContent = "";
    errEl.hidden = true;
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
    session.hintEl.textContent = "Enter to send, Esc to cancel";
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
      session.pickEl.innerHTML = '<div class="hint">Could not look for harnesses: ' + esc(e.message) + "</div>";
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
      html += '<div class="agent-opt-wrap">' + '<button class="agent-opt' + (isSelected ? " on" : "") + '" data-pick="' + esc(h.name) + '">' + '<span class="agent-opt-name">' + esc(h.name) + "</span>" + '<code class="agent-opt-cmd">' + esc(h.cmd) + "</code></button>";
      if (isSelected && h.models && h.models.length > 0) {
        html += '<div class="agent-model-row">' + '<span class="agent-model-label">Model:</span>' + '<select class="agent-model-select" data-harness="' + esc(h.name) + '">';
        for (const m of h.models) {
          const sel = m === (h.model || chosenModel()) ? " selected" : "";
          html += '<option value="' + esc(m) + '"' + sel + ">" + esc(m) + "</option>";
        }
        html += "</select></div>";
      }
      html += "</div>";
    }
    if (settingsPath)
      html += '<div class="agent-note">Remembered in ' + esc(settingsPath) + "</div>";
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
    let job;
    try {
      job = await apiPost("/api/agent/edit", params);
    } catch (e) {
      showErr(session, e.message);
      return;
    }
    session.jobId = job.id;
    session.harness = job.harness;
    hideSelectionBar();
    const initialNote = "Editing with " + (chosenModel() ? chosen() + " (" + chosenModel() + ")" : chosen()) + "...";
    setBusy(session, true, initialNote);
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
  function refreshStatusNote() {
    const busy = [...sessions.values()].filter((s) => s.timer);
    if (!busy.length) {
      setStatusNote("");
    } else if (busy.length === 1) {
      const s = busy[0];
      setStatusNote("Editing with " + (s.harness || chosen()) + "... " + (s.elapsed || ""));
    } else {
      setStatusNote(busy.length + " edits running...");
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
        await drawTree("", treeEl, 0);
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
    addEventListener("beforeunload", (e) => {
      if (!anyInFlight())
        return;
      e.preventDefault();
      e.returnValue = "";
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
  initAgent();
  initMetrics();
  initStatusFit();
  initSettings();
  (async function boot() {
    try {
      initTheme();
      const wrapPref = localStorage.getItem("px0.wrap");
      S2.wrap = wrapPref !== null ? wrapPref === "true" : true;
      document.body.classList.toggle("word-wrap", S2.wrap);
      document.body.classList.toggle("side-right", localStorage.getItem("px0.side") === "right");
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
    if (S2.meta.git) {
      const b = $("#btn-changed");
      if (b)
        b.hidden = false;
    }
    applyAgentMeta();
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
