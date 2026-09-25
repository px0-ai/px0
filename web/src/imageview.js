// web/src/imageview.js
import { $, S, doc_, esc } from './state.js';
import { on } from './bus.js';
import { updateStatus, fmtBytes } from './status.js';

let ivInit = false;
let isPanning = false;
let panStart = { x: 0, y: 0 };
let panOrigin = { x: 0, y: 0 };

export function isImageViewing(d = doc_()) {
  return !!(d && d.isImage);
}

export function syncImageView() {
  const d = doc_();
  const imgView = $('#imgview');
  if (!imgView) return;

  if (isImageViewing(d)) {
    $('#empty').hidden = true;
    imgView.hidden = false;
    renderImageView(d);
  } else {
    imgView.hidden = true;
  }
}

export function renderImageView(d) {
  if (!ivInit) initImageViewer();
  const img = $('#imgview-img');
  const canvas = $('#imgview-canvas');
  if (!img || !canvas) return;

  const rawUrl = new URL('api/raw?path=' + encodeURIComponent(d.path), document.baseURI || location.href).href;
  if (img.dataset.curPath !== d.path) {
    img.dataset.curPath = d.path;
    img.src = rawUrl;
  }

  // Initialize doc defaults
  if (d.imageFit === undefined) d.imageFit = true;
  if (d.imageScale === undefined) d.imageScale = 1;
  if (d.imagePanX === undefined) d.imagePanX = 0;
  if (d.imagePanY === undefined) d.imagePanY = 0;
  if (d.imageBg === undefined) d.imageBg = 'checker';
  if (d.imagePixelated === undefined && d.imageMeta) {
    // Default tiny icons (<= 64px) to pixelated; others to smooth
    d.imagePixelated = d.imageMeta.width <= 64 && d.imageMeta.height <= 64;
  }

  const onLoaded = () => {
    d.imageMeta = {
      width: img.naturalWidth,
      height: img.naturalHeight,
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

export function applyImageTransform(d = doc_()) {
  if (!d || !d.isImage) return;
  const canvas = $('#imgview-canvas');
  const img = $('#imgview-img');
  const vp = $('#imgview-viewport');
  if (!canvas || !img || !vp) return;

  const natW = d.imageMeta?.width || img.naturalWidth || 100;
  const natH = d.imageMeta?.height || img.naturalHeight || 100;

  let currentScale = d.imageScale || 1;
  if (d.imageFit) {
    const vpW = Math.max(100, vp.clientWidth - 64);
    const vpH = Math.max(100, vp.clientHeight - 64);
    const fitScale = Math.min(vpW / natW, vpH / natH);
    // Don't upscale tiny icons beyond 100% on initial fit
    currentScale = natW <= vpW && natH <= vpH ? 1 : fitScale;
    d.imageScale = currentScale;
    d.imagePanX = 0;
    d.imagePanY = 0;
  }

  canvas.style.transform = `translate(${d.imagePanX || 0}px, ${d.imagePanY || 0}px) scale(${currentScale})`;
  canvas.className = 'bg-' + (d.imageBg || 'checker');

  img.classList.toggle('render-pixelated', !!d.imagePixelated);
  img.classList.toggle('render-smooth', !d.imagePixelated);

  // Update HUD
  const zoomLabel = $('#iv-zoom-label');
  if (zoomLabel) {
    zoomLabel.textContent = d.imageFit ? `Fit (${Math.round(currentScale * 100)}%)` : `${Math.round(currentScale * 100)}%`;
  }

  const bgBtn = $('#iv-bg');
  if (bgBtn) {
    bgBtn.textContent = d.imageBg === 'dark' ? 'Dark' : (d.imageBg === 'light' ? 'Light' : 'Checker');
  }

  const pixelBtn = $('#iv-pixel');
  if (pixelBtn) {
    pixelBtn.textContent = d.imagePixelated ? 'Pixelated' : 'Smooth';
    pixelBtn.classList.toggle('active', !!d.imagePixelated);
  }

  const metaEl = $('#iv-meta');
  if (metaEl) {
    metaEl.textContent = `${natW} × ${natH} px · ${fmtBytes(d.size || 0)}`;
  }

  updateStatus();
}

export function zoomImage(delta, factor = 1.25) {
  const d = doc_();
  if (!d || !d.isImage) return;

  d.imageFit = false;
  if (delta > 0) {
    d.imageScale = Math.min(32, (d.imageScale || 1) * factor);
  } else {
    d.imageScale = Math.max(0.05, (d.imageScale || 1) / factor);
  }
  applyImageTransform(d);
}

export function fitImage() {
  const d = doc_();
  if (!d || !d.isImage) return;
  d.imageFit = true;
  d.imagePanX = 0;
  d.imagePanY = 0;
  applyImageTransform(d);
}

export function actualSizeImage() {
  const d = doc_();
  if (!d || !d.isImage) return;
  d.imageFit = false;
  d.imageScale = 1;
  d.imagePanX = 0;
  d.imagePanY = 0;
  applyImageTransform(d);
}

export function cycleImageBg() {
  const d = doc_();
  if (!d || !d.isImage) return;
  const modes = ['checker', 'dark', 'light'];
  const curIdx = modes.indexOf(d.imageBg || 'checker');
  d.imageBg = modes[(curIdx + 1) % modes.length];
  applyImageTransform(d);
}

export function toggleImagePixelated() {
  const d = doc_();
  if (!d || !d.isImage) return;
  d.imagePixelated = !d.imagePixelated;
  applyImageTransform(d);
}

export function panImage(dx, dy) {
  const d = doc_();
  if (!d || !d.isImage) return;
  d.imageFit = false;
  d.imagePanX = (d.imagePanX || 0) + dx;
  d.imagePanY = (d.imagePanY || 0) + dy;
  applyImageTransform(d);
}

export function handleImageKey(e) {
  const d = doc_();
  if (!d || !d.isImage) return false;

  if (e.key === '+' || e.key === '=') {
    zoomImage(1);
    return true;
  }
  if (e.key === '-' || e.key === '_') {
    zoomImage(-1);
    return true;
  }
  if (e.key === '0') {
    fitImage();
    return true;
  }
  if (e.key === '1') {
    actualSizeImage();
    return true;
  }
  if (e.key === 'b' || e.key === 'B') {
    cycleImageBg();
    return true;
  }
  if (e.key === 'p' || e.key === 'P') {
    toggleImagePixelated();
    return true;
  }
  if (e.key === 'ArrowUp') {
    panImage(0, 40);
    return true;
  }
  if (e.key === 'ArrowDown') {
    panImage(0, -40);
    return true;
  }
  if (e.key === 'ArrowLeft') {
    panImage(40, 0);
    return true;
  }
  if (e.key === 'ArrowRight') {
    panImage(-40, 0);
    return true;
  }
  return false;
}

export function initImageViewer() {
  if (ivInit) return;
  ivInit = true;

  const vp = $('#imgview-viewport');
  const hud = $('#imgview-hud');
  if (!vp) return;

  // Zoom buttons
  $('#iv-zoom-in')?.addEventListener('click', (e) => { e.stopPropagation(); zoomImage(1); });
  $('#iv-zoom-out')?.addEventListener('click', (e) => { e.stopPropagation(); zoomImage(-1); });
  $('#iv-zoom-label')?.addEventListener('click', (e) => {
    e.stopPropagation();
    const d = doc_();
    if (d?.imageFit) actualSizeImage(); else fitImage();
  });
  $('#iv-fit')?.addEventListener('click', (e) => { e.stopPropagation(); fitImage(); });
  $('#iv-100')?.addEventListener('click', (e) => { e.stopPropagation(); actualSizeImage(); });
  $('#iv-bg')?.addEventListener('click', (e) => { e.stopPropagation(); cycleImageBg(); });
  $('#iv-pixel')?.addEventListener('click', (e) => { e.stopPropagation(); toggleImagePixelated(); });

  // Panning with mouse drag
  vp.addEventListener('mousedown', (e) => {
    if (e.target.closest('#imgview-hud') || e.button !== 0) return;
    const d = doc_();
    if (!d || !d.isImage) return;

    isPanning = true;
    panStart = { x: e.clientX, y: e.clientY };
    panOrigin = { x: d.imagePanX || 0, y: d.imagePanY || 0 };
    vp.classList.add('panning');
    e.preventDefault();
  });

  window.addEventListener('mousemove', (e) => {
    if (!isPanning) return;
    const d = doc_();
    if (!d || !d.isImage) return;

    const dx = e.clientX - panStart.x;
    const dy = e.clientY - panStart.y;
    if (Math.abs(dx) > 3 || Math.abs(dy) > 3) {
      d.imageFit = false;
    }
    d.imagePanX = panOrigin.x + dx;
    d.imagePanY = panOrigin.y + dy;
    applyImageTransform(d);
  });

  window.addEventListener('mouseup', () => {
    if (!isPanning) return;
    isPanning = false;
    vp.classList.remove('panning');
  });

  // Wheel zoom
  vp.addEventListener('wheel', (e) => {
    const d = doc_();
    if (!d || !d.isImage) return;
    e.preventDefault();

    const factor = e.ctrlKey || e.metaKey ? 1.15 : (Math.abs(e.deltaY) > 50 ? 1.25 : 1.1);
    if (e.deltaY < 0) {
      zoomImage(1, factor);
    } else {
      zoomImage(-1, factor);
    }
  }, { passive: false });

  // Window resize updates fit
  window.addEventListener('resize', () => {
    const d = doc_();
    if (d?.isImage && d.imageFit) {
      applyImageTransform(d);
    }
  });

  on('tab:activated', () => syncImageView());
  on('tabs:cleared', () => syncImageView());
}
