// web/src/reviewflow.js
import { $, $$, S, esc, api, apiPostJson, keyLabel } from './state.js';
import { openFile } from './tabs.js';
import { showToast } from './ui.js';
import { layout } from './renderer.js';
import { thrMd } from './thread.js';
import { onStream, setStreamTopic, watchThread } from './stream.js';
import { initReviewWalkthrough, loadReviewWalkthrough, getWalkthroughSubTab, setWalkthroughSubTab } from './reviewwalkthrough.js';

let currentMode = 'code'; // 'code' | 'review'
let currentLayout = (typeof localStorage !== 'undefined' && localStorage.getItem('px0-review-layout')) || 'vertical';
let reviewConfig = {
  name: 'PR Review Workflow',
  stages: [
    {
      id: 'summary',
      title: 'What this PR does',
      question: 'What does this PR do and what problem does it solve?',
      prompt: 'Analyze this pull request\'s diff and description. Provide a concise explanation of what this PR accomplishes, what problem it solves, and list the key components and files changed. Cite file paths and line ranges where relevant.',
      passPreviousContext: false
    },
    {
      id: 'performance',
      title: 'Performance Impact',
      question: 'Are there any performance bottlenecks, lock contentions, or leaks?',
      prompt: 'Review the changes for performance implications: computational complexity, memory allocations, potential blocking I/O operations, database/network bottlenecks, or mutex/lock contention. Identify any hotspots with exact file:line references.',
      passPreviousContext: true
    },
    {
      id: 'architecture',
      title: 'Architecture & Quality',
      question: 'How does this impact design modularity, edge cases, and maintainability?',
      prompt: 'Evaluate the software architecture, design modularity, readability, maintainability, and error handling. Note any anti-patterns, edge cases that might fail silently, or breaking API changes.',
      passPreviousContext: true
    },
    {
      id: 'testing',
      title: 'Testing & Edge Cases',
      question: 'Are there missing tests or verification steps?',
      prompt: 'Check test coverage for the changes. Are new code paths, error states, and edge conditions tested? Suggest specific tests or verification checks if coverage is incomplete.',
      passPreviousContext: true
    }
  ]
};
let currentRun = null;
let activeStageIdx = 0;
let stageThread = null; // { id, t, release }: the stage thread shown, and its live feed
let lastJumpContext = null; // { path, line, stageTitle }

const statusIcons = {
  pending: '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>',
  running: '<svg class="spin" viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="2" x2="12" y2="6"/><line x1="12" y1="18" x2="12" y2="22"/><line x1="4.93" y1="4.93" x2="7.76" y2="7.76"/><line x1="16.24" y1="16.24" x2="19.07" y2="19.07"/><line x1="2" y1="12" x2="6" y2="12"/><line x1="18" y1="12" x2="22" y2="12"/><line x1="4.93" y1="19.07" x2="7.76" y2="16.24"/><line x1="16.24" y1="7.76" x2="19.07" y2="4.93"/></svg>',
  completed: '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="9 12 11 14 15 10"/></svg>',
  failed: '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>',
  stopped: '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><rect x="9" y="9" width="6" height="6"/></svg>'
};

// Safe property getters supporting both lower and upper case keys from Go JSON
function getDef(st) {
  return st?.def || st?.Def || st || {};
}
function getTitle(d) {
  return d?.title || d?.Title || '';
}
function getQuestion(d) {
  return d?.question || d?.Question || '';
}
function getPrompt(d) {
  return d?.prompt || d?.Prompt || '';
}
function getStatus(st) {
  return (st?.status || st?.Status || 'pending').toLowerCase();
}
function getThreadId(st) {
  return st?.threadId || st?.ThreadID || '';
}
function getStages(obj) {
  return obj?.stages || obj?.Stages || [];
}

function getWorkflowStages() {
  if (reviewConfig.stages && Array.isArray(reviewConfig.stages) && reviewConfig.stages.length > 0) {
    return reviewConfig.stages;
  }
  const wf = reviewConfig.workflows?.[0] || reviewConfig.Workflows?.[0];
  if (wf) {
    return getStages(wf);
  }
  return [];
}

function getWorkflowName() {
  return reviewConfig.name || reviewConfig.workflows?.[0]?.name || reviewConfig.Workflows?.[0]?.Name || 'PR Review Workflow';
}

export function getMode() {
  return currentMode;
}

export function getLayout() {
  return currentLayout;
}

export function setLayout(l) {
  currentLayout = l === 'horizontal' ? 'horizontal' : 'vertical';
  if (typeof localStorage !== 'undefined') {
    localStorage.setItem('px0-review-layout', currentLayout);
  }

  const reviewView = $('#review-view');
  if (reviewView) {
    reviewView.classList.remove('layout-vertical', 'layout-horizontal');
    reviewView.classList.add(`layout-${currentLayout}`);
  }

  const iconVert = $('#icon-layout-vert');
  const iconHoriz = $('#icon-layout-horiz');
  if (iconVert && iconHoriz) {
    iconVert.hidden = currentLayout === 'horizontal';
    iconHoriz.hidden = currentLayout !== 'horizontal';
  }
}

export function setMode(mode) {
  if (mode === currentMode) return;
  currentMode = mode;

  const btnCode = $('#btn-mode-code');
  const btnReview = $('#btn-mode-review');
  const reviewView = $('#review-view');
  const editor = $('#editor');
  const returnPill = $('#review-return-pill');

  if (mode === 'review') {
    document.body.classList.add('in-review-mode');
    if (btnCode) btnCode.classList.remove('active');
    if (btnReview) btnReview.classList.add('active');
    if (editor) editor.hidden = true;
    if (reviewView) reviewView.hidden = false;
    if (returnPill) returnPill.hidden = true;
    setLayout(currentLayout);
    setWalkthroughSubTab(getWalkthroughSubTab());
    if (getWalkthroughSubTab() === 'walkthrough') {
      loadReviewWalkthrough();
    }
    renderPipeline();
    renderActiveStage();
    loadWorkflowStatus();
    connectWorkflowStream();
  } else {
    document.body.classList.remove('in-review-mode');
    if (btnReview) btnReview.classList.remove('active');
    if (btnCode) btnCode.classList.add('active');
    if (reviewView) reviewView.hidden = true;
    if (editor) editor.hidden = false;
    disconnectWorkflowStream();
    layout();
    if (lastJumpContext && returnPill) {
      returnPill.hidden = false;
      const lbl = $('#review-return-label');
      if (lbl) lbl.textContent = `Jumped from: ${lastJumpContext.stageTitle || 'Review'}`;
    }
  }
}

export function disconnectWorkflowStream() {
  setStreamTopic('review', false);
  releaseStageThread();
}

function followStageThread(st) {
  st.release = watchThread(st.id);
  st.live = true;
}

function releaseStageThread() {
  stageThread?.release();
  stageThread = null;
}

export function initReviewFlow() {
  initReviewWalkthrough();
  wireModeControls();
  wireLayoutControls();
  wirePipelineActions();
  wireComposeActions();
  wireHyperlinks();
  wireInlineStageEditor();
  wireAddStageModal();
  wireWorkflowNameEditor();
  wireStreams();
}

function wireModeControls() {
  const btnCode = $('#btn-mode-code');
  const btnReview = $('#btn-mode-review');
  if (btnCode) btnCode.onclick = () => setMode('code');
  if (btnReview) btnReview.onclick = () => setMode('review');

  const returnBtn = $('#btn-return-to-review');
  if (returnBtn) {
    returnBtn.onclick = () => setMode('review');
  }
  const dismissBtn = $('#btn-dismiss-review-return');
  if (dismissBtn) {
    dismissBtn.onclick = () => {
      lastJumpContext = null;
      const pill = $('#review-return-pill');
      if (pill) pill.hidden = true;
    };
  }

  // Keyboard shortcuts: Mod+Shift+V (Review Mode), Mod+Shift+C (Code Mode)
  document.addEventListener('keydown', (e) => {
    const mod = e.metaKey || e.ctrlKey;
    if (mod && e.shiftKey && (e.key === 'V' || e.key === 'v')) {
      e.preventDefault();
      setMode(currentMode === 'review' ? 'code' : 'review');
    } else if (mod && e.shiftKey && (e.key === 'C' || e.key === 'c')) {
      e.preventDefault();
      setMode('code');
    }
  });
}

function wireLayoutControls() {
  const btnToggle = $('#btn-toggle-review-layout');
  if (btnToggle) {
    btnToggle.onclick = () => {
      setLayout(currentLayout === 'vertical' ? 'horizontal' : 'vertical');
    };
  }
  setLayout(currentLayout);
}

function wireWorkflowNameEditor() {
  const nameEl = $('#review-workflow-name');
  if (!nameEl) return;
  nameEl.title = 'Click to edit workflow name';
  nameEl.onclick = () => {
    const currentName = getWorkflowName();
    const newName = prompt('Enter workflow name:', currentName);
    if (newName && newName.trim() && newName.trim() !== currentName) {
      reviewConfig.name = newName.trim();
      if (reviewConfig.workflows?.[0]) {
        reviewConfig.workflows[0].name = reviewConfig.name;
      }
      nameEl.textContent = reviewConfig.name;
      saveConfigToServer();
    }
  };
}

async function loadWorkflowStatus() {
  try {
    const data = await api('/api/review-workflow/status');
    if (data.config) {
      reviewConfig = data.config;
      // Ensure reviewConfig.stages is always populated
      if (!reviewConfig.stages || !Array.isArray(reviewConfig.stages) || reviewConfig.stages.length === 0) {
        reviewConfig.stages = getWorkflowStages();
      }
    }
    if (data.run) {
      currentRun = data.run;
      const runIdx = typeof currentRun.activeStage === 'number' ? currentRun.activeStage : currentRun.ActiveStage;
      if (typeof runIdx === 'number') {
        activeStageIdx = runIdx;
      }
    }
    renderPipeline();
    renderActiveStage();
    updateBackgroundStatusDot();
  } catch (err) {
    console.error('Failed to load review workflow status:', err);
  }
}

function connectWorkflowStream() {
  if (currentMode !== 'review') return;
  setStreamTopic('review', true);
}

// Workflow and stage-thread events arrive on the tab's shared event stream
// (stream.js); these handlers stay registered and the topics turn them on.
function wireStreams() {
  onStream('review:snapshot', (run) => {
    if (!run) return;
    currentRun = run;
    renderPipeline();
    renderActiveStage();
    updateBackgroundStatusDot();
  });

  const handleUpdate = (data) => {
    if (!data) return;
    if (data.stages || data.Stages) {
      currentRun = data;
    } else if (data.stage && currentRun) {
      const runStages = currentRun.stages || currentRun.Stages;
      if (runStages && runStages[data.stageIndex]) {
        runStages[data.stageIndex] = data.stage;
      }
      currentRun.activeStage = data.stageIndex;
    }
    renderPipeline();
    renderActiveStage();
    updateBackgroundStatusDot();
  };
  for (const name of ['run_started', 'stage_started', 'stage_updated', 'stage_completed', 'stage_failed', 'run_completed', 'run_stopped']) {
    onStream('review:' + name, handleUpdate);
  }

  // The stage transcript is patched from the events themselves: refetching
  // the whole thread per delta sent a request per streamed token.
  let paintRAF = 0;
  const paint = () => {
    if (paintRAF) return;
    paintRAF = requestAnimationFrame(() => {
      paintRAF = 0;
      if (stageThread?.t) renderThreadTranscript(stageThread.t);
    });
  };
  const turnOf = (d) => {
    const turns = stageThread?.t?.turns || stageThread?.t?.Turns || [];
    return turns.find(x => (x.id || x.ID) === d.turn);
  };
  onStream('thread:thread', ({ id, data }) => {
    if (stageThread?.id !== id) return;
    stageThread.t = data;
    paint();
  });
  onStream('thread:delta', ({ id, data: d }) => {
    if (stageThread?.id !== id) return;
    const turn = turnOf(d);
    if (!turn) return;
    turn.reply = (turn.reply || '') + d.text;
    paint();
  });
  onStream('thread:tool', ({ id, data: d }) => {
    if (stageThread?.id !== id) return;
    const turn = turnOf(d);
    if (!turn) return;
    (turn.tools = turn.tools || []).push(d.text);
    paint();
  });
}

function updateBackgroundStatusDot() {
  const dot = $('#review-badge-status');
  if (!dot) return;
  const status = currentRun?.status || currentRun?.Status;
  const isRunning = status === 'running';
  dot.hidden = !isRunning;
  if (isRunning) {
    dot.classList.add('pulse');
  } else {
    dot.classList.remove('pulse');
  }
}

function renderPipeline() {
  const container = $('#review-stages-container');
  if (!container) return;

  const stagesList = getWorkflowStages();
  if (stagesList.length === 0) {
    container.innerHTML = '<div class="review-empty">No stages in review workflow</div>';
    return;
  }

  const nameEl = $('#review-workflow-name');
  if (nameEl) nameEl.textContent = getWorkflowName();

  const runStages = currentRun ? getStages(currentRun) : null;
  const items = (runStages && runStages.length === stagesList.length)
    ? runStages
    : stagesList.map(s => ({ def: s, status: 'pending' }));

  container.innerHTML = '';
  items.forEach((st, idx) => {
    const def = getDef(st);
    const title = getTitle(def) || `Stage ${idx + 1}`;
    const question = getQuestion(def) || '';
    const status = getStatus(st);
    const isActive = idx === activeStageIdx;

    const card = document.createElement('div');
    card.className = `review-stage-card ${status} ${isActive ? 'active' : ''}`;
    card.dataset.index = String(idx);

    const iconHtml = statusIcons[status] || statusIcons.pending;

    card.innerHTML = `
      <div class="review-stage-card-icon">${iconHtml}</div>
      <div class="review-stage-card-body">
        <div class="review-stage-card-num">Stage ${idx + 1}</div>
        <div class="review-stage-card-title">${esc(title)}</div>
        <div class="review-stage-card-desc" title="${esc(question)}">${esc(question)}</div>
      </div>
      <div class="review-stage-card-actions">
        <span class="review-stage-card-tag">${status}</span>
        <button type="button" class="mini review-stage-card-edit-btn" title="Edit this stage">✏️</button>
        <button type="button" class="mini review-stage-remove-btn" title="Remove stage from workflow" aria-label="Remove stage">&times;</button>
      </div>
    `;

    card.onclick = (e) => {
      if (e.target.closest('.review-stage-remove-btn') || e.target.closest('.review-stage-card-edit-btn')) return;
      activeStageIdx = idx;
      renderPipeline();
      renderActiveStage();
    };

    const editBtn = card.querySelector('.review-stage-card-edit-btn');
    if (editBtn) {
      editBtn.onclick = (e) => {
        e.stopPropagation();
        activeStageIdx = idx;
        renderPipeline();
        renderActiveStage();
        openInlineStageEditor();
      };
    }

    const removeBtn = card.querySelector('.review-stage-remove-btn');
    if (removeBtn) {
      removeBtn.onclick = async (e) => {
        e.stopPropagation();
        await removeStage(idx);
      };
    }

    container.appendChild(card);
  });

  // Update Run / Stop buttons
  const isRunning = (currentRun?.status || currentRun?.Status) === 'running';
  const btnRun = $('#btn-run-review-flow');
  const btnStop = $('#btn-stop-review-flow');
  if (btnRun) btnRun.hidden = isRunning;
  if (btnStop) btnStop.hidden = !isRunning;
}

async function removeStage(idx) {
  const stages = getWorkflowStages();
  if (stages.length <= 1) {
    showToast('Workflow must have at least one stage');
    return;
  }
  const def = stages[idx];
  const removedTitle = getTitle(def) || `Stage ${idx + 1}`;
  stages.splice(idx, 1);
  reviewConfig.stages = stages;
  if (reviewConfig.workflows?.[0]) {
    reviewConfig.workflows[0].stages = stages;
  }

  if (activeStageIdx >= stages.length) {
    activeStageIdx = Math.max(0, stages.length - 1);
  }
  try {
    await saveConfigToServer();
    showToast(`Removed stage: ${removedTitle}`, 'info', 2000);
    renderPipeline();
    renderActiveStage();
  } catch (err) {
    showToast('!', err.message);
  }
}

async function saveConfigToServer() {
  const stages = getWorkflowStages();
  const payload = {
    name: getWorkflowName(),
    stages: stages,
    workflows: [
      {
        id: 'standard',
        name: getWorkflowName(),
        stages: stages
      }
    ],
    activeId: 'standard'
  };
  await apiPostJson('/api/review-workflow/config', payload);
}

function renderActiveStage() {
  const stages = getWorkflowStages();
  if (stages.length === 0) return;

  if (activeStageIdx >= stages.length) {
    activeStageIdx = 0;
  }

  const runStages = currentRun ? getStages(currentRun) : null;
  const currentStageState = runStages ? runStages[activeStageIdx] : null;
  const def = getDef(currentStageState) || stages[activeStageIdx] || {};

  const badge = $('#review-stage-badge');
  const title = $('#review-stage-title');
  const tag = $('#review-stage-status-tag');
  const question = $('#review-question-text');

  const titleText = getTitle(def) || `Stage ${activeStageIdx + 1}`;
  const questionText = getQuestion(def) || getPrompt(def) || '';

  if (badge) badge.textContent = `Stage ${activeStageIdx + 1} of ${stages.length}`;
  if (title) title.textContent = titleText;
  if (question) question.textContent = questionText;

  const status = currentStageState ? getStatus(currentStageState) : 'pending';
  if (tag) {
    tag.textContent = status;
    tag.className = `review-status-tag ${status}`;
  }

  // Hide inline editor when changing stages
  const inlineEditor = $('#review-stage-inline-editor');
  const questionBanner = $('#review-question-banner');
  if (inlineEditor) inlineEditor.hidden = true;
  if (questionBanner) questionBanner.hidden = false;

  const threadId = currentStageState ? getThreadId(currentStageState) : '';
  const msgsEl = $('#review-thread-msgs');
  if (!msgsEl) return;

  if (!threadId) {
    msgsEl.innerHTML = `
      <div class="review-thread-placeholder">
        <p>This stage has not been executed yet.</p>
        <button id="btn-start-single-stage" class="footer-btn">Run Stage ${activeStageIdx + 1} Now</button>
      </div>
    `;
    const startSingle = $('#btn-start-single-stage');
    if (startSingle) {
      startSingle.onclick = () => runWorkflow(activeStageIdx);
    }
    return;
  }

  loadAndStreamStageThread(threadId);
}

function openInlineStageEditor() {
  const inlineEditor = $('#review-stage-inline-editor');
  const questionBanner = $('#review-question-banner');
  if (!inlineEditor) return;

  const stages = getWorkflowStages();
  const def = stages[activeStageIdx] || {};

  const titleIn = $('#inline-edit-stage-title');
  const qIn = $('#inline-edit-stage-q');
  const promptIn = $('#inline-edit-stage-prompt');

  if (titleIn) titleIn.value = getTitle(def);
  if (qIn) qIn.value = getQuestion(def);
  if (promptIn) promptIn.value = getPrompt(def);

  inlineEditor.hidden = false;
  if (questionBanner) questionBanner.hidden = true;
  if (qIn) qIn.focus();
}

function wireInlineStageEditor() {
  const btnEditInline = $('#btn-edit-stage-inline');
  const btnQuickEditQ = $('#btn-quick-edit-q');
  const btnSave = $('#btn-save-stage-inline');
  const btnCancel = $('#btn-cancel-stage-inline');

  if (btnEditInline) btnEditInline.onclick = openInlineStageEditor;
  if (btnQuickEditQ) btnQuickEditQ.onclick = openInlineStageEditor;

  if (btnCancel) {
    btnCancel.onclick = () => {
      const inlineEditor = $('#review-stage-inline-editor');
      const questionBanner = $('#review-question-banner');
      if (inlineEditor) inlineEditor.hidden = true;
      if (questionBanner) questionBanner.hidden = false;
    };
  }

  if (btnSave) {
    btnSave.onclick = async () => {
      const stages = getWorkflowStages();
      const def = stages[activeStageIdx];
      if (!def) return;

      const titleIn = $('#inline-edit-stage-title');
      const qIn = $('#inline-edit-stage-q');
      const promptIn = $('#inline-edit-stage-prompt');

      const newTitle = (titleIn?.value || '').trim() || getTitle(def);
      const newQ = (qIn?.value || '').trim();
      const newPrompt = (promptIn?.value || '').trim();

      def.title = newTitle;
      def.Title = newTitle;
      def.question = newQ;
      def.Question = newQ;
      def.prompt = newPrompt;
      def.Prompt = newPrompt;

      try {
        await saveConfigToServer();
        showToast('Stage updated successfully', 'success', 2000);
        const inlineEditor = $('#review-stage-inline-editor');
        const questionBanner = $('#review-question-banner');
        if (inlineEditor) inlineEditor.hidden = true;
        if (questionBanner) questionBanner.hidden = false;
        renderPipeline();
        renderActiveStage();
      } catch (err) {
        showToast('!', err.message);
      }
    };
  }
}

async function loadAndStreamStageThread(threadId) {
  const msgsEl = $('#review-thread-msgs');
  if (!msgsEl) return;

  // Workflow updates re-render the active stage often; the same thread is
  // already followed, so just repaint what we have.
  if (stageThread?.id === threadId && stageThread.t) {
    if (currentMode === 'review' && !stageThread.live) followStageThread(stageThread);
    renderThreadTranscript(stageThread.t);
    return;
  }

  releaseStageThread();
  const cur = stageThread = { id: threadId, t: null, live: false, release: () => {} };
  // Follow the thread only while actively viewing it in review mode.
  if (currentMode === 'review') followStageThread(cur);

  try {
    const t = await api(`/api/threads/get?id=${encodeURIComponent(threadId)}`);
    if (stageThread !== cur) return;
    if (!cur.t) cur.t = t; // a stream snapshot may already have landed
    renderThreadTranscript(cur.t);
  } catch (err) {
    if (stageThread !== cur) return;
    msgsEl.innerHTML = `<div class="review-err">Failed to load stage thread: ${esc(err.message)}</div>`;
  }
}

function renderThreadTranscript(t) {
  const msgsEl = $('#review-thread-msgs');
  if (!msgsEl || !t) return;

  let html = '';
  const turns = t.turns || t.Turns || [];
  turns.forEach((turn, i) => {
    const prompt = turn.prompt || turn.Prompt || '';
    const reply = turn.reply || turn.Reply || '';
    const running = turn.running || turn.Running;
    const model = turn.model || turn.Model || '';
    const error = turn.error || turn.Error;
    const tools = turn.tools || turn.Tools || [];

    html += `
      <div class="thr-msg user">
        <div class="thr-role">${i === 0 ? 'Review Question' : 'Follow-up'}</div>
        <div class="thr-body">${thrMd(prompt)}</div>
      </div>
    `;

    if (tools.length > 0) {
      html += `
        <div class="thr-tools">
          <div class="thr-tools-title">Analysis Steps (${tools.length})</div>
          <div class="thr-tools-list">
            ${tools.map(tl => `<div class="thr-tool-item">• ${esc(tl)}</div>`).join('')}
          </div>
        </div>
      `;
    }

    if (reply || running) {
      html += `
        <div class="thr-msg assistant ${running ? 'running' : ''}">
          <div class="thr-role">Review Analysis ${model ? `(${esc(model)})` : ''}</div>
          <div class="thr-body">${reply ? thrMd(reply) : '<span class="thr-loading">Analyzing changes...</span>'}</div>
        </div>
      `;
    }

    if (error) {
      html += `<div class="thr-err">Error: ${esc(error)}</div>`;
    }
  });

  msgsEl.innerHTML = html;
  msgsEl.scrollTop = msgsEl.scrollHeight;
}

function wirePipelineActions() {
  const btnRun = $('#btn-run-review-flow');
  if (btnRun) btnRun.onclick = () => runWorkflow(0);

  const btnStop = $('#btn-stop-review-flow');
  if (btnStop) btnStop.onclick = stopWorkflow;

  const btnRerun = $('#btn-rerun-stage');
  if (btnRerun) btnRerun.onclick = () => runWorkflow(activeStageIdx);
}

function wireComposeActions() {
  const input = $('#review-compose-input');
  const sendBtn = $('#review-compose-send');

  const send = async () => {
    if (!input) return;
    const msg = input.value.trim();
    if (!msg) return;

    const runStages = currentRun ? getStages(currentRun) : null;
    const currentStageState = runStages ? runStages[activeStageIdx] : null;
    const threadId = currentStageState ? getThreadId(currentStageState) : '';
    if (!threadId) {
      showToast('Run the stage first before asking follow-up questions.');
      return;
    }

    input.value = '';
    sendBtn.disabled = true;
    try {
      await apiPostJson('/api/threads/send', { id: threadId, message: msg });
    } catch (err) {
      showToast('!', err.message);
    } finally {
      sendBtn.disabled = false;
    }
  };

  if (sendBtn) sendBtn.onclick = send;
  if (input) {
    input.onkeydown = (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        send();
      }
    };
  }
}

async function runWorkflow(stageIndex = 0) {
  try {
    const res = await apiPostJson('/api/review-workflow/run', {
      workflowId: 'standard',
      stageIndex: stageIndex
    });
    if (res.run) {
      currentRun = res.run;
      activeStageIdx = stageIndex;
      if (currentMode === 'review') {
        connectWorkflowStream();
      }
      renderPipeline();
      renderActiveStage();
      updateBackgroundStatusDot();
    }
  } catch (err) {
    showToast('!', 'Failed to start review workflow: ' + err.message);
  }
}

async function stopWorkflow() {
  try {
    const res = await apiPostJson('/api/review-workflow/stop', {});
    if (res.run) {
      currentRun = res.run;
      renderPipeline();
      renderActiveStage();
      updateBackgroundStatusDot();
    }
  } catch (err) {
    showToast('!', err.message);
  }
}

function wireHyperlinks() {
  document.addEventListener('click', (e) => {
    const link = e.target.closest('a.thr-link, .review-code-ref');
    if (!link || !link.dataset.path) return;

    e.preventDefault();
    const path = link.dataset.path;
    const line = parseInt(link.dataset.line, 10) || 1;

    const stages = getWorkflowStages();
    const def = stages[activeStageIdx];
    const stageTitle = def ? getTitle(def) : 'Review';
    lastJumpContext = { path, line, stageTitle };

    setMode('code');
    openFile(path, { line });

    const pill = $('#review-return-pill');
    if (pill) {
      pill.hidden = false;
      const lbl = $('#review-return-label');
      if (lbl) lbl.textContent = `Jumped from: ${stageTitle}`;
    }
  });
}

function wireAddStageModal() {
  const modal = $('#review-add-stage-modal');
  const addBtn = $('#btn-add-review-stage');
  const closeBtn = $('#add-stage-close');
  const cancelBtn = $('#add-stage-cancel');
  const submitBtn = $('#add-stage-submit');

  const titleIn = $('#add-stage-title-input');
  const qIn = $('#add-stage-q-input');
  const promptIn = $('#add-stage-prompt-input');

  const openAddModal = () => {
    if (!modal) return;
    if (titleIn) titleIn.value = '';
    if (qIn) qIn.value = '';
    if (promptIn) promptIn.value = '';
    modal.hidden = false;
    if (titleIn) titleIn.focus();
  };

  const closeAddModal = () => {
    if (modal) modal.hidden = true;
  };

  if (addBtn) addBtn.onclick = openAddModal;
  if (closeBtn) closeBtn.onclick = closeAddModal;
  if (cancelBtn) cancelBtn.onclick = closeAddModal;

  if (submitBtn) {
    submitBtn.onclick = async () => {
      const title = (titleIn?.value || '').trim();
      const q = (qIn?.value || '').trim();
      const prompt = (promptIn?.value || '').trim();

      if (!title) {
        showToast('Please provide a stage title');
        return;
      }

      const stages = getWorkflowStages();
      const newStage = {
        id: `stage-${Date.now()}`,
        title: title,
        question: q || title,
        prompt: prompt || (q ? `Analyze the PR changes specifically for: ${q}. Highlight affected lines.` : `Review the PR for ${title}.`),
        passPreviousContext: true
      };

      stages.push(newStage);
      reviewConfig.stages = stages;
      if (reviewConfig.workflows?.[0]) {
        reviewConfig.workflows[0].stages = stages;
      }
      activeStageIdx = stages.length - 1;

      try {
        await saveConfigToServer();
        showToast(`Added stage: ${title}`, 'success', 2000);
        closeAddModal();
        renderPipeline();
        renderActiveStage();
      } catch (err) {
        showToast('!', err.message);
      }
    };
  }
}
