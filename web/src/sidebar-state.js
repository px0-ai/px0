// Shared sidebar visibility state, kept DOM-small so it can be regression tested.
export function syncSidebarButton(body, button) {
  if (button) button.hidden = !body.classList.contains('side-hidden');
}

export function toggleSidebarState(body, button) {
  body.classList.toggle('side-hidden');
  syncSidebarButton(body, button);
  return body.classList.contains('side-hidden');
}
