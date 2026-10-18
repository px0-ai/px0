// web/src/newitem.js
import { $, apiPostJson } from './state.js';
import { showToast } from './ui.js';
import { openFile } from './tabs.js';
import { enterEditMode } from './editview.js';
import { triggerRefresh } from './gitstream.js';
import { drawTree, revealDir, treeEl } from './tree.js';

export function promptNewItem(type, targetDir = '') {
  const modal = $('#input-modal');
  const modalTitle = $('#modal-title');
  const modalDesc = $('#modal-desc');
  const modalInput = $('#modal-input');
  if (!modal || !modalTitle || !modalDesc || !modalInput) return;

  const currentType = type === 'dir' ? 'dir' : 'file';
  modal.dataset.type = currentType;
  const baseDir = targetDir ? targetDir.replace(/^\/+|\/+$/g, '') : '';
  const prefix = baseDir ? baseDir + '/' : '';

  modalTitle.textContent = currentType === 'dir' ? 'New Folder' : 'New File';
  modalDesc.textContent = prefix
    ? `Create inside ${prefix} (or specify path):`
    : 'Enter relative path from project root:';
  modalInput.value = prefix;
  modal.hidden = false;
  modalInput.focus();
  modalInput.setSelectionRange(modalInput.value.length, modalInput.value.length);
}

export function closeNewItemModal() {
  const modal = $('#input-modal');
  const modalInput = $('#modal-input');
  if (modal) modal.hidden = true;
  if (modalInput) modalInput.value = '';
}

async function submitNewItem() {
  const modal = $('#input-modal');
  const modalInput = $('#modal-input');
  const btnOk = $('#modal-btn-ok');
  if (!modal || !modalInput) return;

  const currentType = modal.dataset.type || 'file';
  const val = modalInput.value.trim().replace(/^\/+|\/+$/g, '');
  if (!val) {
    showToast('!', 'Path cannot be empty');
    return;
  }

  if (btnOk) btnOk.disabled = true;
  try {
    const res = await apiPostJson('/api/fs/new', { path: val, type: currentType });
    closeNewItemModal();
    showToast('✓', (currentType === 'dir' ? 'Created folder ' : 'Created ') + res.path);

    const parentDir = val.includes('/') ? val.substring(0, val.lastIndexOf('/')) : '';
    await drawTree('', treeEl, 0);
    if (parentDir) {
      await revealDir(parentDir);
    }

    triggerRefresh();

    if (currentType === 'file') {
      await openFile(res.path);
      await enterEditMode();
    }
  } catch (err) {
    showToast('!', 'Failed to create: ' + (err.message || 'unknown error'));
  } finally {
    if (btnOk) btnOk.disabled = false;
  }
}

export function initNewItem() {
  const modal = $('#input-modal');
  if (!modal) return;

  $('#btn-new-file')?.addEventListener('click', () => promptNewItem('file'));
  $('#btn-new-folder')?.addEventListener('click', () => promptNewItem('dir'));

  $('#modal-btn-cancel')?.addEventListener('click', closeNewItemModal);
  $('#modal-btn-ok')?.addEventListener('click', submitNewItem);

  const modalInput = $('#modal-input');
  modalInput?.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      submitNewItem();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      closeNewItemModal();
    }
  });

  modal.addEventListener('click', (e) => {
    if (e.target === modal) closeNewItemModal();
  });
}
