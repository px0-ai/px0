// web/src/reviewwalkthrough.js
import { $, $$, esc, api } from './state.js';
import { showToast } from './ui.js';

let walkthroughData = null;
let activeFilter = 'all'; // 'all' | 'fastReturns' | 'edgeCases' | 'coreLogic'
let activeSubTab = 'walkthrough'; // 'walkthrough' | 'stages'
let viewedFiles = new Set();
let observer = null;
let isProgrammaticScroll = false;
let scrollTimeout = null;

export function getWalkthroughSubTab() {
  return activeSubTab;
}

export function setWalkthroughSubTab(tab) {
  activeSubTab = tab;
  const btnWalkthrough = $('#btn-review-tab-walkthrough');
  const btnStages = $('#btn-review-tab-stages');
  const viewWalkthrough = $('#review-walkthrough-view');
  const viewStages = $('#review-stages-view');
  const toolbar = $('#review-walkthrough-toolbar');

  if (tab === 'walkthrough') {
    if (btnWalkthrough) {
      btnWalkthrough.classList.add('active');
      if (btnWalkthrough.setAttribute) btnWalkthrough.setAttribute('aria-selected', 'true');
    }
    if (btnStages) {
      btnStages.classList.remove('active');
      if (btnStages.setAttribute) btnStages.setAttribute('aria-selected', 'false');
    }
    if (viewWalkthrough) viewWalkthrough.hidden = false;
    if (viewStages) viewStages.hidden = true;
    if (toolbar) toolbar.hidden = false;

    // Load or refresh walkthrough if empty
    if (!walkthroughData) {
      loadReviewWalkthrough();
    }
  } else {
    if (btnWalkthrough) {
      btnWalkthrough.classList.remove('active');
      if (btnWalkthrough.setAttribute) btnWalkthrough.setAttribute('aria-selected', 'false');
    }
    if (btnStages) {
      btnStages.classList.add('active');
      if (btnStages.setAttribute) btnStages.setAttribute('aria-selected', 'true');
    }
    if (viewWalkthrough) viewWalkthrough.hidden = true;
    if (viewStages) viewStages.hidden = false;
    if (toolbar) toolbar.hidden = true;
  }
}

export function initReviewWalkthrough() {
  const btnWalkthrough = $('#btn-review-tab-walkthrough');
  const btnStages = $('#btn-review-tab-stages');
  if (btnWalkthrough) btnWalkthrough.onclick = () => setWalkthroughSubTab('walkthrough');
  if (btnStages) btnStages.onclick = () => setWalkthroughSubTab('stages');

  const btnRefresh = $('#btn-refresh-walkthrough');
  if (btnRefresh) {
    btnRefresh.onclick = () => {
      loadReviewWalkthrough(true);
    };
  }

  // Filter chips (All, Returns, Edge Cases, Core)
  const chips = $$('.review-chip');
  chips.forEach(chip => {
    chip.onclick = () => {
      chips.forEach(c => c.classList.remove('active'));
      chip.classList.add('active');
      activeFilter = chip.dataset.filter || 'all';
      applyWalkthroughFilter();
    };
  });
}

export async function loadReviewWalkthrough(showFeedback = false) {
  const leftContent = $('#review-understanding-content');
  const rightContent = $('#review-diff-content');

  if (showFeedback && showToast) {
    showToast('Refreshing walkthrough & diff...', 1500);
  }

  if (leftContent && (!walkthroughData || showFeedback)) {
    leftContent.innerHTML = '<div class="review-empty-state"><div class="review-spinner"></div><p>Analyzing code changes & guardrails...</p></div>';
  }
  if (rightContent && (!walkthroughData || showFeedback)) {
    rightContent.innerHTML = '<div class="review-empty-state"><div class="review-spinner"></div><p>Rendering diff view...</p></div>';
  }

  try {
    const data = await api('/api/review/walkthrough');
    walkthroughData = data;
    renderWalkthroughView(data);
    if (showFeedback && showToast) {
      showToast(`Walkthrough updated: ${data.totalFiles || 0} files analyzed.`, 2000);
    }
  } catch (err) {
    // In headless test environments without an active HTTP server, ignore network errors
    if (typeof process !== 'undefined' && (process.env?.NODE_ENV === 'test' || process.env?.BUN_TEST)) {
      return;
    }
    if (leftContent) {
      leftContent.innerHTML = `<div class="review-empty-state"><p class="review-err-text">Failed to load code understanding: ${esc(err.message || String(err))}</p></div>`;
    }
    if (rightContent) {
      rightContent.innerHTML = `<div class="review-empty-state"><p class="review-err-text">Failed to load diffs.</p></div>`;
    }
  }
}

function renderWalkthroughView(data) {
  const leftContent = $('#review-understanding-content');
  const rightContent = $('#review-diff-content');
  const filesBadge = $('#review-walkthrough-files-badge');
  const filesNav = $('#review-understanding-files-nav');
  const statHeading = $('#review-understanding-stat');

  const files = data.files || [];
  const totalFiles = data.totalFiles || files.length;
  const additions = data.totalAdditions || 0;
  const deletions = data.totalDeletions || 0;

  if (filesBadge) {
    filesBadge.textContent = `${totalFiles} file${totalFiles === 1 ? '' : 's'}`;
  }

  let totalReturns = 0;
  let totalEdge = 0;
  let totalCore = 0;

  for (const f of files) {
    const und = f.understanding || {};
    totalReturns += (und.fastReturns || []).length;
    totalEdge += (und.edgeCases || []).length;
    totalCore += (und.coreLogic || []).length;
  }

  const countReturns = $('#wt-count-returns');
  const countEdge = $('#wt-count-edge');
  const countCore = $('#wt-count-core');
  if (countReturns) countReturns.textContent = totalReturns;
  if (countEdge) countEdge.textContent = totalEdge;
  if (countCore) countCore.textContent = totalCore;

  if (statHeading) {
    statHeading.innerHTML = `<strong>${totalFiles}</strong> files &middot; <span class="diff-stat-add">+${additions}</span> <span class="diff-stat-del">-${deletions}</span> &middot; ${data.baseRef || 'HEAD'} &rarr; ${data.headRef || 'now'}`;
  }

  // Quick navigation chips for files
  if (filesNav) {
    filesNav.innerHTML = '';
    files.forEach((f, idx) => {
      const chip = document.createElement('button');
      chip.className = 'review-file-nav-chip' + (idx === 0 ? ' active' : '');
      chip.dataset.path = f.path;
      const basename = f.path.split('/').pop();
      chip.innerHTML = `<span class="review-file-nav-name" title="${esc(f.path)}">${esc(basename)}</span><span class="review-file-nav-status ${f.status || 'M'}">${esc(f.status || 'M')}</span>`;
      chip.onclick = () => {
        scrollToDiffFile(f.path);
        highlightActiveFileCard(f.path);
      };
      filesNav.appendChild(chip);
    });
  }

  if (files.length === 0) {
    if (leftContent) {
      leftContent.innerHTML = `
        <div class="review-empty-state">
          <svg viewBox="0 0 24 24" width="32" height="32" fill="none" stroke="currentColor" stroke-width="1.8"><polyline points="20 6 9 17 4 12"/></svg>
          <h3>Working Tree is Clean</h3>
          <p>No modified files or uncommitted diffs found against ${esc(data.baseRef || 'HEAD')}.</p>
        </div>`;
    }
    if (rightContent) {
      rightContent.innerHTML = `
        <div class="review-empty-state">
          <svg viewBox="0 0 24 24" width="32" height="32" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>
          <h3>No Diffs to Display</h3>
          <p>Make local changes or switch branches to inspect diffs and code understanding.</p>
        </div>`;
    }
    return;
  }

  // Render Left Pane: Understanding Cards
  renderUnderstandingCards(files);

  // Render Right Pane: Multi-file Diff with Sticky Headers
  renderDiffBlocks(files);

  // Set up Scrollytelling Synchronization via IntersectionObserver
  setupScrollSynchronization();
}

function renderUnderstandingCards(files) {
  const leftContent = $('#review-understanding-content');
  if (!leftContent) return;
  leftContent.innerHTML = '';

  files.forEach((file) => {
    const card = document.createElement('article');
    card.className = 'review-file-card';
    card.id = `card-file-${sanitizeId(file.path)}`;
    card.dataset.filePath = file.path;

    const und = file.understanding || { fastReturns: [], edgeCases: [], coreLogic: [], summary: '' };
    const returns = und.fastReturns || [];
    const edges = und.edgeCases || [];
    const core = und.coreLogic || [];

    // Header
    const head = document.createElement('header');
    head.className = 'review-card-head';

    const titleGroup = document.createElement('div');
    titleGroup.className = 'review-card-title-group';

    const pathEl = document.createElement('h3');
    pathEl.className = 'review-card-path';
    pathEl.textContent = file.path;
    pathEl.title = file.path;

    const badge = document.createElement('span');
    badge.className = 'review-card-badge';
    badge.innerHTML = `<span class="diff-stat-add">+${file.additions}</span> <span class="diff-stat-del">-${file.deletions}</span>`;

    titleGroup.append(pathEl, badge);

    const jumpBtn = document.createElement('button');
    jumpBtn.className = 'mini review-card-jump-btn';
    jumpBtn.title = 'Jump to file diff';
    jumpBtn.innerHTML = '<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2"><polyline points="9 18 15 12 9 6"/></svg>';
    jumpBtn.onclick = () => scrollToDiffFile(file.path);

    head.append(titleGroup, jumpBtn);
    card.append(head);

    // Summary statement
    if (und.summary) {
      const summaryEl = document.createElement('div');
      summaryEl.className = 'review-card-summary';
      summaryEl.textContent = und.summary;
      card.append(summaryEl);
    }

    // Body of items
    const body = document.createElement('div');
    body.className = 'review-card-body';

    // 1. Fast Returns & Bailouts
    if (returns.length > 0) {
      const secReturns = createInsightSection(file.path, '⚡ Fast Returns & Guardrails', 'fastReturns', returns, 'badge-returns');
      body.append(secReturns);
    }

    // 2. Edge Cases & Errors
    if (edges.length > 0) {
      const secEdges = createInsightSection(file.path, '⚠️ Edge Cases & Failure Exits', 'edgeCases', edges, 'badge-edge');
      body.append(secEdges);
    }

    // 3. Core Logic
    if (core.length > 0) {
      const secCore = createInsightSection(file.path, '🎯 Core Logic & Happy Path', 'coreLogic', core, 'badge-core');
      body.append(secCore);
    }

    if (returns.length === 0 && edges.length === 0 && core.length === 0) {
      const emptyNote = document.createElement('div');
      emptyNote.className = 'review-card-empty-note';
      emptyNote.textContent = 'Standard modification with clean linear execution flow.';
      body.append(emptyNote);
    }

    card.append(body);
    leftContent.append(card);
  });
}

function createInsightSection(filePath, headingText, filterCategory, items, badgeClass) {
  const sec = document.createElement('div');
  sec.className = `review-insight-sec sec-${filterCategory}`;
  sec.dataset.category = filterCategory;

  const h = document.createElement('h4');
  h.className = 'review-insight-heading';
  h.innerHTML = `<span>${esc(headingText)}</span> <span class="review-insight-count ${badgeClass}">${items.length}</span>`;
  sec.append(h);

  const list = document.createElement('div');
  list.className = 'review-insight-list';

  items.forEach(item => {
    const itemEl = document.createElement('div');
    itemEl.className = 'review-insight-item';
    itemEl.id = `insight-${sanitizeId(filePath)}-${item.line}`;
    itemEl.dataset.filePath = filePath;
    itemEl.dataset.line = item.line;

    const row = document.createElement('div');
    row.className = 'review-insight-top';

    const pill = document.createElement('button');
    pill.className = 'review-line-pill';
    pill.textContent = item.endLine && item.endLine !== item.line ? `L${item.line}–${item.endLine}` : `L${item.line}`;
    pill.title = 'Click to scroll diff to this line';
    pill.onclick = (e) => {
      e.stopPropagation();
      scrollToDiffLine(filePath, item.line);
    };

    const kindBadge = document.createElement('span');
    kindBadge.className = `review-kind-badge kind-${item.kind || 'general'}`;
    kindBadge.textContent = item.kind || 'guard';

    const expl = document.createElement('span');
    expl.className = 'review-insight-expl';
    expl.textContent = item.explanation || '';

    row.append(pill, kindBadge, expl);

    const code = document.createElement('pre');
    code.className = 'review-insight-code';
    code.textContent = item.text || '';

    itemEl.append(row, code);

    // Clicking the item container also scrolls
    itemEl.onclick = () => scrollToDiffLine(filePath, item.line);

    list.append(itemEl);
  });

  sec.append(list);
  return sec;
}

function renderDiffBlocks(files) {
  const rightContent = $('#review-diff-content');
  if (!rightContent) return;
  rightContent.innerHTML = '';

  files.forEach(file => {
    const block = document.createElement('div');
    block.className = 'review-diff-file-block';
    block.id = `diff-file-${sanitizeId(file.path)}`;
    block.dataset.filePath = file.path;

    // Sticky File Header
    const stickyHeader = document.createElement('div');
    stickyHeader.className = 'review-diff-file-sticky-header';

    const left = document.createElement('div');
    left.className = 'review-sticky-head-left';

    const fileIcon = document.createElement('span');
    fileIcon.className = 'review-file-icon';
    fileIcon.innerHTML = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>';

    const pathSpan = document.createElement('span');
    pathSpan.className = 'review-sticky-head-path';
    pathSpan.textContent = file.path;

    const statsSpan = document.createElement('span');
    statsSpan.className = 'review-sticky-head-stats';
    statsSpan.innerHTML = `<span class="diff-stat-add">+${file.additions}</span> <span class="diff-stat-del">-${file.deletions}</span>`;

    left.append(fileIcon, pathSpan, statsSpan);

    const right = document.createElement('div');
    right.className = 'review-sticky-head-right';

    // Viewed checkbox
    const viewedLabel = document.createElement('label');
    viewedLabel.className = 'review-viewed-toggle';
    const viewedCheckbox = document.createElement('input');
    viewedCheckbox.type = 'checkbox';
    viewedCheckbox.checked = viewedFiles.has(file.path);
    viewedCheckbox.onchange = () => {
      if (viewedCheckbox.checked) {
        viewedFiles.add(file.path);
        block.classList.add('is-viewed');
      } else {
        viewedFiles.delete(file.path);
        block.classList.remove('is-viewed');
      }
      updateViewedBadge(file.path);
    };

    const viewedText = document.createElement('span');
    viewedText.textContent = 'Viewed';
    viewedLabel.append(viewedCheckbox, viewedText);

    right.append(viewedLabel);
    stickyHeader.append(left, right);
    block.append(stickyHeader);

    // Hunks Table
    const hunksTable = document.createElement('div');
    hunksTable.className = 'review-diff-table';

    const hunks = file.hunks || [];
    if (hunks.length === 0) {
      const emptyHunks = document.createElement('div');
      emptyHunks.className = 'review-diff-no-hunks';
      emptyHunks.textContent = 'No diff hunks to display.';
      hunksTable.append(emptyHunks);
    } else {
      hunks.forEach((hunk, hIdx) => {
        const hunkHead = document.createElement('div');
        hunkHead.className = 'review-diff-hunk-header';
        hunkHead.textContent = `@@ -${hunk.oldStart || 0} +${hunk.newStart || 0} @@ ${hunk.section || ''}`;
        hunksTable.append(hunkHead);

        (hunk.rows || []).forEach(row => {
          const rowEl = document.createElement('div');
          rowEl.className = `review-diff-row diff-type-${row.type || 'ctx'}`;
          const lineNum = row.newLine || row.oldLine || 0;
          rowEl.id = `diff-line-${sanitizeId(file.path)}-${lineNum}`;
          rowEl.dataset.filePath = file.path;
          rowEl.dataset.line = lineNum;

          const oldCol = document.createElement('span');
          oldCol.className = 'diff-col-num diff-col-old';
          oldCol.textContent = row.oldLine ? String(row.oldLine) : '';

          const newCol = document.createElement('span');
          newCol.className = 'diff-col-num diff-col-new';
          newCol.textContent = row.newLine ? String(row.newLine) : '';

          const signCol = document.createElement('span');
          signCol.className = 'diff-col-sign';
          signCol.textContent = row.type === 'add' ? '+' : row.type === 'del' ? '-' : ' ';

          const codeCol = document.createElement('span');
          codeCol.className = 'diff-col-code';
          codeCol.textContent = row.text || '';

          rowEl.append(oldCol, newCol, signCol, codeCol);
          hunksTable.append(rowEl);
        });
      });
    }

    block.append(hunksTable);
    rightContent.append(block);
  });
}

function updateViewedBadge(filePath) {
  const card = $(`#card-file-${sanitizeId(filePath)}`);
  if (!card) return;
  if (viewedFiles.has(filePath)) {
    card.classList.add('is-viewed');
  } else {
    card.classList.remove('is-viewed');
  }
}

function applyWalkthroughFilter() {
  const sections = $$('.review-insight-sec');
  sections.forEach(sec => {
    if (activeFilter === 'all') {
      sec.hidden = false;
    } else {
      sec.hidden = sec.dataset.category !== activeFilter;
    }
  });
}

export function scrollToDiffFile(filePath) {
  const target = $(`#diff-file-${sanitizeId(filePath)}`);
  const pane = $('#review-diff-pane');
  if (target && pane) {
    markProgrammaticScroll();
    const paneRect = pane.getBoundingClientRect();
    const targetRect = target.getBoundingClientRect();
    const targetScrollTop = pane.scrollTop + (targetRect.top - paneRect.top);
    pane.scrollTo({ top: Math.max(0, targetScrollTop), behavior: 'smooth' });
    highlightActiveFileCard(filePath, true);
  }
}

export function scrollToDiffLine(filePath, line) {
  const pane = $('#review-diff-pane');
  if (!pane) return;

  let target = $(`#diff-line-${sanitizeId(filePath)}-${line}`);
  if (!target) {
    try {
      target = pane.querySelector(`[data-file-path="${filePath}"][data-line="${line}"]`);
    } catch (e) {
      // Ignore querySelector syntax errors on paths
    }
  }
  if (!target) {
    scrollToDiffFile(filePath);
    return;
  }

  markProgrammaticScroll();
  const paneRect = pane.getBoundingClientRect();
  const targetRect = target.getBoundingClientRect();
  const targetScrollTop = pane.scrollTop + (targetRect.top - paneRect.top) - (pane.clientHeight / 3);
  pane.scrollTo({ top: Math.max(0, targetScrollTop), behavior: 'smooth' });

  target.classList.add('diff-row-flash');
  setTimeout(() => {
    target.classList.remove('diff-row-flash');
  }, 1800);

  highlightActiveInsight(filePath, line);
  highlightActiveFileCard(filePath, true);
}

function markProgrammaticScroll() {
  isProgrammaticScroll = true;
  if (scrollTimeout) clearTimeout(scrollTimeout);
  scrollTimeout = setTimeout(() => {
    isProgrammaticScroll = false;
  }, 800);
}

function highlightActiveFileCard(filePath, shouldScrollLeft = false) {
  const cards = $$('.review-file-card');
  cards.forEach(c => c.classList.remove('active-file-card'));
  const activeCard = $(`#card-file-${sanitizeId(filePath)}`);
  if (activeCard) {
    activeCard.classList.add('active-file-card');
    if (shouldScrollLeft) {
      const listPane = $('#review-understanding-content');
      if (listPane) {
        const paneRect = listPane.getBoundingClientRect();
        const cardRect = activeCard.getBoundingClientRect();
        if (cardRect.top < paneRect.top || cardRect.bottom > paneRect.bottom) {
          const targetScroll = listPane.scrollTop + (cardRect.top - paneRect.top) - 20;
          listPane.scrollTo({ top: Math.max(0, targetScroll), behavior: 'smooth' });
        }
      }
    }
  }

  // Update quick nav chips
  const chips = $$('.review-file-nav-chip');
  chips.forEach(chip => {
    if (chip.dataset.path === filePath) {
      chip.classList.add('active');
    } else {
      chip.classList.remove('active');
    }
  });
}

function highlightActiveInsight(filePath, line) {
  const items = $$('.review-insight-item');
  items.forEach(it => it.classList.remove('active-insight-item'));
  const targetInsight = $(`#insight-${sanitizeId(filePath)}-${line}`);
  if (targetInsight) {
    targetInsight.classList.add('active-insight-item');
    const listPane = $('#review-understanding-content');
    if (listPane) {
      const paneRect = listPane.getBoundingClientRect();
      const itemRect = targetInsight.getBoundingClientRect();
      if (itemRect.top < paneRect.top || itemRect.bottom > paneRect.bottom) {
        const targetScroll = listPane.scrollTop + (itemRect.top - paneRect.top) - 40;
        listPane.scrollTo({ top: Math.max(0, targetScroll), behavior: 'smooth' });
      }
    }
  }
}

function setupScrollSynchronization() {
  if (observer) {
    observer.disconnect();
    observer = null;
  }

  const diffPane = $('#review-diff-pane');
  if (!diffPane) return;

  const fileBlocks = $$('.review-diff-file-block');
  if (fileBlocks.length === 0) return;

  // Primary: Scroll event with requestAnimationFrame for smooth, guaranteed active file sync
  let scrollTicking = false;
  diffPane.onscroll = () => {
    if (isProgrammaticScroll) return;
    if (!scrollTicking) {
      requestAnimationFrame(() => {
        syncActiveFileOnScroll();
        scrollTicking = false;
      });
      scrollTicking = true;
    }
  };
}

function syncActiveFileOnScroll() {
  const diffPane = $('#review-diff-pane');
  if (!diffPane) return;
  const blocks = $$('.review-diff-file-block');
  if (blocks.length === 0) return;

  const paneRect = diffPane.getBoundingClientRect();
  const anchorY = paneRect.top + 70; // 70px below top (just below sticky header)

  let activeBlock = blocks[0];
  for (let i = 0; i < blocks.length; i++) {
    const b = blocks[i];
    const r = b.getBoundingClientRect();
    if (r.top <= anchorY && r.bottom > anchorY) {
      activeBlock = b;
      break;
    } else if (r.top > anchorY) {
      break;
    }
    activeBlock = b;
  }

  if (activeBlock?.dataset.filePath) {
    highlightActiveFileCard(activeBlock.dataset.filePath, false);
  }
}

function sanitizeId(str) {
  return String(str || '').replace(/[^a-zA-Z0-9_-]/g, '-');
}
