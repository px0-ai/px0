// Searchable model picker. Model discovery stays on the server; this only
// filters the IDs already present in the current harness metadata.

let nextPickerId = 0;

export function fuzzyFilter(values, query) {
  const items = Array.isArray(values) ? values : [];
  const q = String(query || '').trim().toLowerCase();
  if (!q) {
    return items.map((value, index) => ({ value, index, score: 0, positions: [] }));
  }

  const matches = [];
  items.forEach((value, index) => {
    const text = String(value).toLowerCase();
    const direct = text.indexOf(q);
    if (direct >= 0) {
      matches.push({ value, index, score: 1000 - direct, positions: directPositions(q, direct) });
      return;
    }

    const positions = [];
    let cursor = 0;
    for (const ch of q) {
      const found = text.indexOf(ch, cursor);
      if (found < 0) return;
      positions.push(found);
      cursor = found + 1;
    }
    const spread = positions[positions.length - 1] - positions[0] - q.length + 1;
    matches.push({ value, index, score: 500 - spread * 4 - positions[0], positions });
  });

  matches.sort((a, b) => b.score - a.score || a.index - b.index);
  return matches;
}

function directPositions(query, start) {
  const positions = [];
  for (let i = 0; i < query.length; i++) positions.push(start + i);
  return positions;
}

export function createModelPicker(host, options = {}) {
  if (!host) return null;

  const id = ++nextPickerId;
  const initialValues = host.options ? [...host.options].map(option => option.value).filter(Boolean) : [];
  const initialValue = host.value || '';
  const root = document.createElement('div');
  root.className = 'agent-model-picker';
  if (host.id) root.id = host.id;
  for (const [key, value] of Object.entries(host.dataset || {})) root.dataset[key] = value;

  const trigger = document.createElement('button');
  trigger.type = 'button';
  trigger.className = 'agent-model-trigger';
  trigger.setAttribute('aria-haspopup', 'listbox');
  trigger.setAttribute('aria-expanded', 'false');
  trigger.setAttribute('aria-controls', `agent-model-menu-${id}`);

  const valueEl = document.createElement('span');
  valueEl.className = 'agent-model-value';
  const caret = document.createElement('span');
  caret.className = 'agent-model-caret';
  caret.setAttribute('aria-hidden', 'true');
  caret.textContent = '▾';
  trigger.append(valueEl, caret);
  root.appendChild(trigger);

  const menu = document.createElement('div');
  menu.className = 'agent-model-menu';
  menu.id = `agent-model-menu-${id}`;
  menu.hidden = true;

  const searchId = `agent-model-search-${id}`;
  const label = document.createElement('label');
  label.className = 'agent-model-search-label';
  label.htmlFor = searchId;
  label.textContent = 'Search models';

  const search = document.createElement('input');
  search.className = 'agent-model-search';
  search.id = searchId;
  search.type = 'search';
  search.name = 'model-search';
  search.autocomplete = 'off';
  search.spellcheck = false;
  search.placeholder = 'Type to filter';
  search.setAttribute('role', 'combobox');
  search.setAttribute('aria-autocomplete', 'list');
  search.setAttribute('aria-controls', `agent-model-list-${id}`);
  search.setAttribute('aria-expanded', 'false');

  const list = document.createElement('div');
  list.className = 'agent-model-list';
  list.id = `agent-model-list-${id}`;
  list.setAttribute('role', 'listbox');
  list.setAttribute('aria-label', 'Available models');

  const empty = document.createElement('div');
  empty.className = 'agent-model-empty';
  empty.textContent = 'No matching models';
  empty.hidden = true;

  menu.append(label, search, list, empty);
  document.body.appendChild(menu);
  host.replaceWith(root);

  const picker = {
    root,
    trigger,
    menu,
    search,
    list,
    empty,
    value: initialValue,
    values: [...new Set(initialValues)],
    matches: [],
    active: 0,
    disabled: false,
    isOpen: false,
    label: options.label || 'Model',
    onChange: options.onChange || (() => {}),
  };

  const onTriggerClick = () => {
    if (picker.disabled) return;
    if (picker.isOpen) picker.close(true);
    else picker.open();
  };
  const onTriggerKeydown = (e) => {
    if (e.key === 'Escape') {
      if (!picker.isOpen) return;
      e.stopPropagation();
      e.preventDefault();
      picker.close(true);
      return;
    }
    e.stopPropagation();
  };
  const onSearchInput = () => {
    picker.active = 0;
    picker.renderList();
    picker.position();
  };
  const onSearchKeydown = (e) => {
    e.stopPropagation();
    if (e.key === 'ArrowDown' || (e.ctrlKey && e.key === 'n')) {
      e.preventDefault();
      picker.move(1);
    } else if (e.key === 'ArrowUp' || (e.ctrlKey && e.key === 'p')) {
      e.preventDefault();
      picker.move(-1);
    } else if (e.key === 'Home' && picker.matches.length) {
      e.preventDefault();
      picker.active = 0;
      picker.renderActive();
    } else if (e.key === 'End' && picker.matches.length) {
      e.preventDefault();
      picker.active = picker.matches.length - 1;
      picker.renderActive();
    } else if (e.key === 'Enter' && picker.matches.length) {
      e.preventDefault();
      void picker.choose(picker.matches[picker.active].value);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      picker.close(true);
    } else if (e.key === 'Tab') {
      picker.close(false);
    }
  };
  const onDocumentPointerDown = (e) => {
    if (!picker.isOpen) return;
    if (root.contains(e.target) || menu.contains(e.target)) return;
    picker.close(false);
  };
  const onViewportChange = (event) => {
    if (!picker.isOpen) return;
    const target = event?.target;
    if (event?.type === 'scroll' && target instanceof Node && (target === menu || menu.contains(target))) return;
    if (event?.type === 'scroll') picker.position();
    else picker.close(false);
  };

  trigger.addEventListener('click', onTriggerClick);
  trigger.addEventListener('keydown', onTriggerKeydown);
  search.addEventListener('input', onSearchInput);
  search.addEventListener('keydown', onSearchKeydown);
  document.addEventListener('pointerdown', onDocumentPointerDown);
  addEventListener('resize', onViewportChange);
  addEventListener('scroll', onViewportChange, true);

  const visibilityObserver = typeof MutationObserver === 'function'
    ? new MutationObserver(() => {
      if (picker.isOpen && (!root.isConnected || root.getClientRects().length === 0)) picker.close(false);
    })
    : null;
  if (visibilityObserver) {
    for (let node = root; node && node !== document.body; node = node.parentElement) {
      visibilityObserver.observe(node, { attributes: true, attributeFilter: ['hidden', 'class', 'style'] });
    }
  }

  picker.setOptions = (nextValues, selected = '', state = {}) => {
    const values = Array.isArray(nextValues) ? nextValues.filter(v => typeof v === 'string' && v) : [];
    picker.values = [...new Set(values)];
    if (selected && !picker.values.includes(selected)) picker.values.unshift(selected);
    picker.value = selected || picker.values[0] || '';
    if (state.disabled !== undefined) picker.disabled = !!state.disabled;
    if (state.title !== undefined) trigger.title = state.title;
    picker.renderTrigger();
    if (picker.disabled) picker.close(false);
    if (picker.isOpen) {
      picker.renderList();
      picker.position();
    }
    return picker;
  };

  picker.setDisabled = (disabled) => {
    picker.disabled = !!disabled;
    trigger.disabled = picker.disabled;
    root.classList.toggle('disabled', picker.disabled);
    if (picker.disabled) picker.close(false);
  };

  picker.setVisible = (visible) => {
    root.hidden = !visible;
    if (!visible) picker.close(false);
  };

  picker.focus = () => trigger.focus();

  picker.destroy = () => {
    picker.close(false);
    trigger.removeEventListener('click', onTriggerClick);
    trigger.removeEventListener('keydown', onTriggerKeydown);
    search.removeEventListener('input', onSearchInput);
    search.removeEventListener('keydown', onSearchKeydown);
    document.removeEventListener('pointerdown', onDocumentPointerDown);
    removeEventListener('resize', onViewportChange);
    removeEventListener('scroll', onViewportChange, true);
    visibilityObserver?.disconnect();
    menu.remove();
    root.remove();
  };

  picker.open = () => {
    if (picker.disabled || !picker.values.length) return;
    picker.isOpen = true;
    picker.search.value = '';
    const currentIndex = picker.values.indexOf(picker.value);
    picker.active = currentIndex >= 0 ? currentIndex : 0;
    picker.menu.hidden = false;
    picker.trigger.setAttribute('aria-expanded', 'true');
    picker.search.setAttribute('aria-expanded', 'true');
    picker.renderList();
    picker.position();
    picker.search.focus();
  };

  picker.close = (restoreFocus) => {
    if (!picker.isOpen) return;
    picker.isOpen = false;
    picker.menu.hidden = true;
    picker.trigger.setAttribute('aria-expanded', 'false');
    picker.search.setAttribute('aria-expanded', 'false');
    picker.search.removeAttribute('aria-activedescendant');
    if (restoreFocus) picker.trigger.focus();
  };

  picker.renderTrigger = () => {
    valueEl.textContent = picker.value || 'No models';
    trigger.setAttribute('aria-label', picker.value ? picker.label + ': ' + picker.value : picker.label);
    trigger.disabled = picker.disabled;
    root.classList.toggle('disabled', picker.disabled);
  };

  picker.renderList = () => {
    picker.matches = fuzzyFilter(picker.values, picker.search.value);
    picker.active = Math.max(0, Math.min(picker.active, picker.matches.length - 1));
    picker.list.replaceChildren();
    picker.empty.hidden = picker.matches.length !== 0;
    picker.search.setAttribute('aria-expanded', picker.isOpen ? 'true' : 'false');

    picker.matches.forEach((match, index) => {
      const option = document.createElement('button');
      option.type = 'button';
      option.className = 'agent-model-option';
      option.id = `agent-model-option-${id}-${index}`;
      option.dataset.value = match.value;
      option.setAttribute('role', 'option');
      option.setAttribute('aria-selected', match.value === picker.value ? 'true' : 'false');
      option.tabIndex = -1;
      renderHighlighted(option, match.value, match.positions);
      option.addEventListener('click', () => void picker.choose(match.value));
      picker.list.appendChild(option);
    });
    picker.renderActive();
  };

  picker.renderActive = () => {
    const options = [...picker.list.children];
    options.forEach((option, index) => {
      const active = index === picker.active;
      option.classList.toggle('active', active);
      if (active) {
        picker.search.setAttribute('aria-activedescendant', option.id);
        option.scrollIntoView({ block: 'nearest' });
      }
    });
    if (!options.length) picker.search.removeAttribute('aria-activedescendant');
  };

  picker.move = (delta) => {
    if (!picker.matches.length) return;
    picker.active = (picker.active + delta + picker.matches.length) % picker.matches.length;
    picker.renderActive();
  };

  picker.choose = async (value) => {
    picker.close(true);
    try {
      const result = await picker.onChange(value);
      if (result !== false) {
        picker.value = value;
        picker.renderTrigger();
      }
    } catch {}
  };

  picker.position = () => {
    if (!picker.isOpen) return;
    if (!root.isConnected || root.getClientRects().length === 0) {
      picker.close(false);
      return;
    }
    const rect = picker.trigger.getBoundingClientRect();
    const edge = 8;
    const gap = 4;
    const below = Math.max(0, innerHeight - rect.bottom - gap - edge);
    const above = Math.max(0, rect.top - gap - edge);
    const placeAbove = below < 180 && above > below;
    const available = Math.max(80, placeAbove ? above : below);
    const width = Math.max(240, rect.width);
    const left = Math.max(edge, Math.min(rect.left, innerWidth - width - edge));
    picker.menu.style.left = `${left}px`;
    picker.menu.style.width = `${Math.min(width, innerWidth - edge * 2)}px`;
    picker.menu.style.maxHeight = `${Math.min(260, available)}px`;
    const height = Math.min(picker.menu.scrollHeight, Math.min(260, available));
    const top = placeAbove
      ? Math.max(edge, rect.top - gap - height)
      : Math.max(edge, Math.min(rect.bottom + gap, innerHeight - edge - height));
    picker.menu.style.top = `${top}px`;
  };

  picker.renderTrigger();
  return picker;
}

function renderHighlighted(element, value, positions) {
  if (!positions || !positions.length) {
    element.textContent = value;
    return;
  }
  const marked = new Set(positions);
  let start = 0;
  for (let i = 0; i <= value.length; i++) {
    if (i === value.length || marked.has(i) !== marked.has(start)) {
      const part = value.slice(start, i);
      if (marked.has(start)) {
        const mark = document.createElement('mark');
        mark.textContent = part;
        element.appendChild(mark);
      } else {
        element.appendChild(document.createTextNode(part));
      }
      start = i;
    }
  }
}
