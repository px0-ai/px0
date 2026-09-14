// web/src/vim-status.js — Vim status bar (mode + command line).
import { $, S, doc_ } from './state.js';
const bar = $('#vim-bar');
const modeEl = $('#vim-mode');
const cmdEl = $('#vim-cmd');
const posEl = $('#vim-pos');

const MODE_LABEL = {
  normal: 'NORMAL',
  insert: 'INSERT',
  visual: 'VISUAL',
  command: 'COMMAND',
};

export function updateVimStatus() {
  if (!bar) return;
  const on = !!S.vim;
  bar.hidden = !on;
  document.body.classList.toggle('vim-on', on);
  if (!on) return;

  const mode = S.vimMode;
  if (modeEl) {
    modeEl.textContent = '-- ' + (MODE_LABEL[mode] || mode.toUpperCase()) + ' --';
    modeEl.dataset.mode = mode;
  }

  if (cmdEl) {
    if (mode === 'command') {
      cmdEl.textContent = ':' + (S.vimCmd || '');
      cmdEl.hidden = false;
    } else {
      cmdEl.textContent = '';
      cmdEl.hidden = true;
    }
  }

  const d = doc_();
  if (posEl && d) {
    const col = (d.col || 0) + 1;
    posEl.textContent = d.name + (d.dirty ? ' [+]' : '') + '  ' + d.cur + ':' + col;
  } else if (posEl) {
    posEl.textContent = '';
  }
}
