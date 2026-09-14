// web/src/image.js
// Image tabs: binary images (png/jpg/svg/...) open as real tabs — same tab bar,
// same Alt+W / cross / Ctrl+Tab / Alt+1..9 handling as code — but never as code.
// The doc carries { image:true, size } with no lines/chunks/gutter/LSP/outline;
// a single reused <img> shows /api/raw for the active tab, following the same
// overlay-over-#viewport pattern as the Markdown preview and diff view.
import { $, doc_ } from './state.js';

export const isImage = (d = doc_()) => !!(d && d.image);

const view = () => $('#imgview');
const img = () => $('#img');

export function syncImage() {
  const d = doc_();
  const v = view();
  if (!v) return;
  if (!isImage(d)) {
    v.hidden = true;
    return;
  }
  const el = img();
  const src = '/api/raw?path=' + encodeURIComponent(d.path);
  // Same path re-selected (e.g. tab switch back): keep the decoded bitmap.
  if (el && el.dataset.path !== d.path) {
    el.dataset.path = d.path;
    el.alt = d.name || d.path;
    el.src = src;
  }
  if (typeof d.imgScroll === 'number') v.scrollTop = d.imgScroll;
  else v.scrollTop = 0;
  v.hidden = false;
}
