// web/src/bus.js
/**
 * @fileoverview Lightweight, zero-dependency event bus for decoupling frontend subsystems.
 * Replaces direct cross-module imperative calls with clean publish-subscribe semantics.
 */

/**
 * @type {Map<string, Set<Function>>}
 */
const registry = new Map();

/**
 * Subscribe to an application lifecycle or domain event.
 * @param {string} event - The event name (e.g. 'tab:activated', 'theme:changed')
 * @param {Function} handler - Callback to execute when event is emitted
 * @returns {() => void} Function that unsubscribes the listener
 */
export function on(event, handler) {
  if (!registry.has(event)) {
    registry.set(event, new Set());
  }
  const set = registry.get(event);
  set.add(handler);
  return () => {
    set.delete(handler);
    if (set.size === 0) registry.delete(event);
  };
}

/**
 * Subscribe to an event exactly once.
 * @param {string} event - The event name
 * @param {Function} handler - Callback to execute once
 * @returns {() => void} Function that unsubscribes the listener
 */
export function once(event, handler) {
  const unsub = on(event, (data) => {
    unsub();
    handler(data);
  });
  return unsub;
}

/**
 * Unsubscribe a specific handler from an event.
 * @param {string} event - The event name
 * @param {Function} handler - Callback to remove
 */
export function off(event, handler) {
  const set = registry.get(event);
  if (set) {
    set.delete(handler);
    if (set.size === 0) registry.delete(event);
  }
}

/**
 * Emit an event synchronously to all registered listeners.
 * Errors in individual listeners are isolated and logged to avoid breaking the caller.
 * @param {string} event - The event name
 * @param {any} [data] - Optional payload for listeners
 */
export function emit(event, data) {
  const set = registry.get(event);
  if (!set || set.size === 0) return;
  // Copy to array to avoid mutation during iteration
  const handlers = [...set];
  for (const handler of handlers) {
    try {
      handler(data);
    } catch (err) {
      console.error(`[bus] Error in listener for "${event}":`, err);
    }
  }
}

/**
 * Clear all event listeners (useful for testing and workspace re-initialization).
 */
export function clearBus() {
  registry.clear();
}
