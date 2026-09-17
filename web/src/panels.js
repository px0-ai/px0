// web/src/panels.js
import { $, $$, S, api } from './state.js';
import { layout, render } from './renderer.js';
import { updateStatus } from './status.js';
import { loadOutline } from './outline.js';
import { treeEl, openDirs, drawTree } from './tree.js';
import { reloadOpenTabs } from './tabs.js';
import { showToast } from './ui.js';

/* The sidebar's "move to the other side" action lives in settings.js, which
   registers it here on load. Keeping the dependency one-way (as selbar/agent
   do) avoids importing settings from the sidebar module and keeps the two from
   forming a cycle. */
let sidebarToggle = null;
export function setSidebarToggle(fn) { sidebarToggle = fn; }

export function showPanel(name) {
  document.body.classList.remove('side-hidden');
  layout();
  render();
}

const sideMenu = $('#side-menu');
const sideMenuBtn = $('#btn-side-menu');

function closeSideMenu() {
  if (sideMenu && !sideMenu.hidden) sideMenu.hidden = true;
  sideMenuBtn?.setAttribute('aria-expanded', 'false');
}

/* Place the menu right-aligned under the ⋯ button, or at the pointer, flipping
   at the window's edges. */
function placeSideMenu(anchor) {
  const w = sideMenu.offsetWidth, h = sideMenu.offsetHeight;
  if (anchor.el) {
    const r = anchor.el.getBoundingClientRect();
    sideMenu.style.left = Math.max(4, Math.min(r.right - w, innerWidth - w - 4)) + 'px';
    sideMenu.style.top = (r.bottom + 4) + 'px';
  } else {
    sideMenu.style.left = Math.max(4, anchor.x + w > innerWidth - 4 ? anchor.x - w : anchor.x) + 'px';
    sideMenu.style.top = Math.max(4, anchor.y + h > innerHeight - 4 ? anchor.y - h : anchor.y) + 'px';
  }
}

/* The sidebar's settings menu. The header's ⋯ button opens it under the button;
   a right click on the panel opens it at the pointer. */
function openSideMenu(anchor) {
  if (!sideMenu) return;
  const onRight = document.body.classList.contains('side-right');
  sideMenu.replaceChildren();
  const item = document.createElement('button');
  item.className = 'side-menu-item';
  item.setAttribute('role', 'menuitem');
  item.textContent = onRight ? 'Move Sidebar to Left' : 'Move Sidebar to Right';
  sideMenu.append(item);
  sideMenu.hidden = false;
  placeSideMenu(anchor);
  sideMenuBtn?.setAttribute('aria-expanded', 'true');
}

export function initPanels() {
  $('#btn-reindex').addEventListener('click', async () => {
    const j = await api('/api/reindex');
    S.meta.files = j.files; S.meta.indexMs = j.indexMs;
    treeEl.innerHTML = ''; openDirs.clear();
    await drawTree('', treeEl, 0);
    // Reindex is a refresh: re-fetch open tabs quietly in place without tab switching.
    await reloadOpenTabs();
    updateStatus();
    showToast('✓', 'Workspace reindexed');
  });

  /* sidebar resize */
  (() => {
    const rz = $('#resizer'); let dragging = false;
    rz.addEventListener('mousedown', e => { dragging = true; rz.classList.add('drag'); e.preventDefault(); });
    addEventListener('mousemove', e => {
      if (!dragging) return;
      const raw = document.body.classList.contains('side-right') ? innerWidth - e.clientX : e.clientX;
      $('#side').style.width = Math.max(170, Math.min(620, raw)) + 'px';
    });
    addEventListener('mouseup', () => { if (dragging) { dragging = false; rz.classList.remove('drag'); layout(); render(); } });
  })();

  /* The ⋯ button in the header toggles the menu; a right click on the sidebar
     opens it at the pointer. Footer links keep the browser's own menu, so
     "open in new tab" still works on them. */
  sideMenuBtn?.addEventListener('click', e => {
    e.stopPropagation();
    if (sideMenu && !sideMenu.hidden) closeSideMenu();
    else openSideMenu({ el: sideMenuBtn });
  });
  document.addEventListener('contextmenu', e => {
    if (sideMenu && sideMenu.contains(e.target)) { e.preventDefault(); return; }
    if (!e.target.closest('#side') || e.target.closest('a')) return;
    e.preventDefault();
    openSideMenu({ x: e.clientX, y: e.clientY });
  });
  addEventListener('keydown', e => { if (e.key === 'Escape') closeSideMenu(); });

  if (sideMenu) {
    sideMenu.addEventListener('mousedown', e => e.preventDefault());
    sideMenu.addEventListener('click', e => {
      if (!e.target.closest('.side-menu-item')) return;
      closeSideMenu();
      if (sidebarToggle) sidebarToggle();
      showToast('✓', 'Sidebar moved to ' + (document.body.classList.contains('side-right') ? 'right' : 'left'));
    });
    document.addEventListener('mousedown', e => {
      if (sideMenu.hidden) return;
      if (sideMenu.contains(e.target) || (sideMenuBtn && sideMenuBtn.contains(e.target))) return;
      closeSideMenu();
    }, true);
    addEventListener('resize', closeSideMenu);
    addEventListener('blur', closeSideMenu);
    document.addEventListener('scroll', closeSideMenu, true);
  }
}
