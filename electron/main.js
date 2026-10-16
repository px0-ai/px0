// px0 app mode: a thin Electron shell around the px0 server.
// It spawns the px0 binary, waits for the URL it prints, and shows it in a
// window titled "px0 - <reponame>".
const { app, BrowserWindow, shell, nativeImage, dialog } = require('electron');
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const ICON = path.join(__dirname, 'assets', process.platform === 'win32' ? 'icon.ico' : 'icon.png');
const ANSI = /\x1b\[[0-9;]*[A-Za-z]/g;

app.setName('px0');

// Args after the app path are forwarded to px0 (target dir/file, PR URL, flags).
const userArgs = process.argv.slice(app.isPackaged ? 1 : 2);

function findBinary() {
  const exe = process.platform === 'win32' ? 'px0.exe' : 'px0';
  const candidates = [
    process.env.PX0_BIN,
    path.join(process.resourcesPath || '', exe),
    path.join(__dirname, '..', exe),
  ].filter(Boolean);
  return candidates.find((p) => fs.existsSync(p)) || exe; // fall back to PATH
}

// Repo name for the title: PR URL wins, otherwise the workspace directory name.
function repoName(workspace) {
  if (process.env.PX0_REPO_NAME) return process.env.PX0_REPO_NAME; // set by launch.js
  for (const a of userArgs) {
    const m = /^https?:\/\/[^/]+\/[^/]+\/([^/]+)\/(?:pull|pulls|merge_requests)\/\d+/.exec(a)
      || /^https?:\/\/[^/]+\/[^/]+\/([^/]+)\/-?\/?pull-requests\/\d+/.exec(a);
    if (m) return m[1];
  }
  return path.basename(workspace) || workspace;
}

let child = null;
let win = null;

function startServer() {
  return new Promise((resolve, reject) => {
    const args = ['--no-open', '--no-color', '--port', '0', ...userArgs];
    child = spawn(findBinary(), args, { stdio: ['ignore', 'pipe', 'pipe'] });
    let buf = '';
    let url = '';
    let workspace = '';
    let settled = false;
    const done = (err) => {
      if (settled) return;
      settled = true;
      err ? reject(err) : resolve({ url, workspace });
    };
    const onData = (chunk) => {
      buf += chunk.toString().replace(ANSI, '');
      const lines = buf.split(/\r?\n/);
      buf = lines.pop();
      for (const line of lines) {
        const ws = /^\s*workspace:\s+(.+)$/.exec(line);
        if (ws) workspace = ws[1].trim();
        const u = /^\s*url:\s+(http\S+)/.exec(line);
        if (u) {
          url = u[1];
          done();
        }
      }
    };
    child.stdout.on('data', onData);
    child.stderr.on('data', (c) => {
      process.stderr.write(c);
      onData(c);
    });
    child.on('error', (e) => done(new Error(`could not start px0 (${e.message}). Build it with "make build" or set PX0_BIN.`)));
    child.on('exit', (code) => {
      done(new Error(`px0 exited early (code ${code})`));
      child = null;
      if (win) app.quit(); // server died while running
    });
  });
}

function createWindow({ url, workspace }) {
  const title = `px0 - ${repoName(workspace)}`;
  win = new BrowserWindow({
    width: 1400,
    height: 900,
    title,
    icon: ICON,
    backgroundColor: '#18181b',
    autoHideMenuBar: true,
    webPreferences: { contextIsolation: true, sandbox: true },
  });
  // Keep our title; the web page would otherwise replace it.
  win.on('page-title-updated', (e) => e.preventDefault());
  win.webContents.setWindowOpenHandler(({ url: target }) => {
    shell.openExternal(target);
    return { action: 'deny' };
  });
  win.webContents.on('will-navigate', (e, target) => {
    if (new URL(target).origin !== new URL(url).origin) {
      e.preventDefault();
      shell.openExternal(target);
    }
  });
  win.on('closed', () => { win = null; });
  win.loadURL(url);
  win.setTitle(title);
}

function stopServer() {
  if (child) {
    child.kill('SIGTERM');
    child = null;
  }
}

app.whenReady().then(async () => {
  if (process.platform === 'darwin' && app.dock) app.dock.setIcon(nativeImage.createFromPath(ICON));
  try {
    createWindow(await startServer());
  } catch (err) {
    dialog.showErrorBox('px0', err.message);
    app.quit();
  }
});

app.on('window-all-closed', () => app.quit());
app.on('before-quit', stopServer);
process.on('exit', stopServer);
