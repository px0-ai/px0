export function breadcrumbParts(path) {
  const parts = path.split('/').filter(Boolean);
  return { external: /^(?:\/|[A-Za-z]:\/)/.test(path), dirs: parts.slice(0, -1), name: parts[parts.length - 1] || '' };
}
