// web/src/ui.js
import { $, esc } from './state.js';

export const vp = $('#viewport');
export const sizer = $('#sizer');
export const rowsEl = $('#rows');
export const editor = $('#editor');
export const toastEl = $('#toast');

let toastTimer = 0;
let toastLeaveTimer = 0;

export function showToast(accentText, text, duration = 2200) {
  if (!toastEl) return;
  clearTimeout(toastTimer);
  clearTimeout(toastLeaveTimer);

  toastEl.classList.remove('toast-hide');

  let iconHtml = '';
  if (accentText) {
    if (accentText === '✓') {
      iconHtml = '<span class="toast-icon toast-icon-ok"><svg viewBox="0 0 16 16" width="11" height="11" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M3.5 8.5l3 3 6-6"/></svg></span>';
    } else if (accentText === '!') {
      iconHtml = '<span class="toast-icon toast-icon-warn"><svg viewBox="0 0 16 16" width="11" height="11" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="8" y1="4" x2="8" y2="9"/><circle cx="8" cy="12.5" r="0.6" fill="currentColor"/></svg></span>';
    } else {
      iconHtml = '<span class="toast-chip">' + esc(accentText) + '</span>';
    }
  }

  toastEl.innerHTML = iconHtml + '<span class="toast-msg">' + esc(text) + '</span>';
  toastEl.hidden = false;

  toastTimer = setTimeout(() => {
    toastEl.classList.add('toast-hide');
    toastLeaveTimer = setTimeout(() => {
      toastEl.hidden = true;
      toastEl.classList.remove('toast-hide');
    }, 180);
  }, duration);
}

export async function copyToClipboard(text, notify = 'Copied') {
  try {
    await navigator.clipboard.writeText(text);
    showToast('✓', notify);
  } catch {
    // Fallback for non-https/restricted contexts
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand('copy');
      showToast('✓', notify);
    } catch (err) {
      showToast('!', 'Failed to copy to clipboard');
    }
    document.body.removeChild(ta);
  }
}
