import { apiPostJson } from './state.js';

let pending = Promise.resolve();

export function saveSessionUI(key, value) {
  pending = pending.catch(() => {}).then(() =>
    apiPostJson('/api/session', { ui: { [key]: value } })
  ).catch(() => {});
  return pending;
}
