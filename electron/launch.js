#!/usr/bin/env node
// px0-ide launcher. Starts the Electron app detached from the terminal.
//
// On macOS the Cmd+Tab label and Dock name come from the app bundle's
// Info.plist, not from the window title, so each repo gets its own cloned
// bundle named "px0 - <repo>" (APFS clones, so they cost almost no disk).
const { spawn, execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const APP_DIR = __dirname;
const args = process.argv.slice(2);
const VALUE_FLAGS = new Set(['port', 'host', 'agent', 'base-path']);

function passthrough(bin) {
  const r = require('node:child_process').spawnSync(bin, args, { stdio: 'inherit' });
  process.exit(r.status ?? 1);
}

const bin = process.env.PX0_BIN || path.join(APP_DIR, process.platform === 'win32' ? 'px0.exe' : 'px0');
if (['--version', '-v', 'version', '--update', '--help', '-h'].includes(args[0])) passthrough(bin);

// First positional argument, the way px0's flag parsing sees it.
function targetArg() {
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (!a.startsWith('-')) return a;
    const name = a.replace(/^--?/, '');
    if (!name.includes('=') && VALUE_FLAGS.has(name)) i++;
  }
  return '.';
}

function repoName() {
  const target = targetArg();
  const pr = /^https?:\/\/[^/]+\/[^/]+\/([^/]+)\/(?:pull|pulls|merge_requests)\/\d+/.exec(target)
    || /^https?:\/\/[^/]+\/[^/]+\/([^/]+)\/-?\/?pull-requests\/\d+/.exec(target);
  if (pr) return pr[1];
  let p = path.resolve(target);
  if (!fs.existsSync(p)) p = path.resolve(target.replace(/:\d+$/, '')); // file:line
  try {
    p = fs.realpathSync(p);
    if (fs.statSync(p).isFile()) {
      const dir = path.dirname(p);
      try {
        p = execFileSync('git', ['-C', dir, 'rev-parse', '--show-toplevel'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim();
      } catch {
        p = dir;
      }
    }
  } catch {}
  return path.basename(p) || p;
}

const repo = repoName();
const title = `px0 - ${repo}`;
let exe = null;

if (process.platform === 'darwin') {
  try {
    exe = ensureMacBundle(repo, title);
  } catch (e) {
    process.stderr.write(`px0-ide: could not prepare per-repo app bundle (${e.message}); using stock Electron\n`);
  }
}
if (!exe) exe = require(path.join(APP_DIR, 'node_modules', 'electron')); // path to the electron binary

function ensureMacBundle(repo, title) {
  const electronDir = path.dirname(require.resolve('electron/package.json', { paths: [APP_DIR] }));
  const srcApp = path.join(electronDir, 'dist', 'Electron.app');
  const version = require(path.join(electronDir, 'package.json')).version;
  const slug = repo.replace(/[^A-Za-z0-9._-]/g, '_');
  const dir = path.join(APP_DIR, 'bundles', slug);
  const app = path.join(dir, `${title.replace(/[/:]/g, '-')}.app`);
  const marker = path.join(dir, '.version');
  const exeName = 'px0-ide';
  const exePath = path.join(app, 'Contents', 'MacOS', exeName);

  if (fs.existsSync(exePath) && fs.existsSync(marker) && fs.readFileSync(marker, 'utf8') === version) return exePath;

  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });
  try {
    execFileSync('cp', ['-cR', srcApp, app]); // APFS clone
  } catch {
    execFileSync('cp', ['-R', srcApp, app]);
  }
  const contents = path.join(app, 'Contents');
  fs.renameSync(path.join(contents, 'MacOS', 'Electron'), exePath);

  const plist = path.join(contents, 'Info.plist');
  const set = (k, v) => execFileSync('plutil', ['-replace', k, '-string', v, plist]);
  set('CFBundleName', title);
  set('CFBundleDisplayName', title);
  set('CFBundleExecutable', exeName);
  set('CFBundleIdentifier', `ai.px0.ide.${slug.replace(/[^A-Za-z0-9.-]/g, '-')}`);

  // Replace the Electron icon so Cmd+Tab shows px0 before the app has even started.
  try {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'px0-icon-'));
    const set_ = path.join(tmp, 'px0.iconset');
    fs.mkdirSync(set_);
    const png = path.join(APP_DIR, 'assets', 'icon.png');
    for (const s of [16, 32, 128, 256, 512]) {
      execFileSync('sips', ['-z', s, s, png, '--out', path.join(set_, `icon_${s}x${s}.png`)], { stdio: 'ignore' });
      execFileSync('sips', ['-z', s * 2, s * 2, png, '--out', path.join(set_, `icon_${s}x${s}@2x.png`)], { stdio: 'ignore' });
    }
    execFileSync('iconutil', ['-c', 'icns', set_, '-o', path.join(contents, 'Resources', 'electron.icns')]);
    fs.rmSync(tmp, { recursive: true, force: true });
  } catch {}

  // Editing the bundle invalidates its signature; re-sign ad hoc so macOS runs it cleanly.
  try {
    execFileSync('codesign', ['--force', '--deep', '--sign', '-', app], { stdio: 'ignore' });
  } catch {}
  fs.writeFileSync(marker, version);
  return exePath;
}

const log = fs.openSync(path.join(process.env.TMPDIR || os.tmpdir(), 'px0-ide.log'), 'w');
const child = spawn(exe, [APP_DIR, ...args], {
  detached: true,
  stdio: ['ignore', log, log],
  env: { ...process.env, PX0_BIN: bin, PX0_REPO_NAME: repo },
});
child.on('error', (e) => {
  process.stderr.write(`px0-ide: failed to start Electron: ${e.message}\n`);
  process.exit(1);
});
child.unref();
