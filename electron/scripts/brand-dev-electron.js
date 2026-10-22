// macOS only: rename the dev Electron bundle so Cmd+Tab and the Dock say "px0"
// instead of "Electron". Best effort; failures are ignored.
if (process.platform !== 'darwin') process.exit(0);
const { execFileSync } = require('node:child_process');
const path = require('node:path');
try {
  const electronDir = path.dirname(require.resolve('electron/package.json'));
  const plist = path.join(electronDir, 'dist', 'Electron.app', 'Contents', 'Info.plist');
  for (const key of ['CFBundleName', 'CFBundleDisplayName']) {
    execFileSync('plutil', ['-replace', key, '-string', 'px0', plist]);
  }
} catch {}
