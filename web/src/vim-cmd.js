// web/src/vim-cmd.js — Vim ex commands (:w, :q, :wq, …).
import { doc_ } from './state.js';
import { showToast } from './ui.js';
import { saveFile, saveAllFiles, quitTab, quitAll } from './edit.js';

export function runExCommand(line) {
  const cmd = line.trim();
  if (!cmd) return;

  const bang = cmd.endsWith('!');
  const base = bang ? cmd.slice(0, -1) : cmd;

  if (base === 'w') {
    saveFile(doc_());
    return;
  }
  if (base === 'wa') {
    saveAllFiles();
    return;
  }
  if (base === 'q') {
    quitTab(bang);
    return;
  }
  if (base === 'qa') {
    quitAll(bang);
    return;
  }
  if (base === 'wq' || base === 'x') {
    const d = doc_();
    if (d?.dirty) saveFile(d).then(() => quitTab(true));
    else quitTab(true);
    return;
  }
  if (base === 'help') {
    showToast('', ':w :wa :q :q! :qa :qa! :wq :x');
    return;
  }
  showToast('!', 'Unknown command: ' + cmd);
}
