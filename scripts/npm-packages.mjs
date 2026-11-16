#!/usr/bin/env node
// Build the npm packages for a native Go CLI from its release binaries: one small launcher package
// (@SCOPE/NAME, bin only) plus one package per platform (@SCOPE/NAME-OS-CPU) that holds the binary and is
// pulled in through optionalDependencies (the esbuild/lmplayer layout). No install scripts anywhere.
//
//   node npm-packages.mjs --name lmauth --version 0.1.10 --assets DIR --out DIR --license FILE \
//     --description TEXT --repo lmctl-ai/lmauth \
//     --target linux-x64=lmauth-linux-x64 --target windows-x64=lmauth-windows-x64.exe ...
//
// --target PLATFORM=ASSET: PLATFORM is os-cpu with os in linux|darwin|windows and cpu in x64|arm64 (npm names
// win32 "windows" in the package name only); ASSET is the file name inside --assets. Prints the publish order
// (platform packages first, launcher last) as JSON lines on stdout.
import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const args = process.argv.slice(2);
const opts = { target: [] };
for (let i = 0; i < args.length; i += 2) {
  const key = args[i]?.replace(/^--/, '');
  if (!key || args[i + 1] === undefined) fail(`missing value for ${args[i]}`);
  if (key === 'target') opts.target.push(args[i + 1]);
  else opts[key] = args[i + 1];
}
for (const k of ['name', 'version', 'assets', 'out', 'repo']) if (!opts[k]) fail(`--${k} is required`);
if (!/^\d+\.\d+\.\d+$/.test(opts.version)) fail(`version must be MAJOR.MINOR.PATCH, got ${opts.version}`);
if (!/^[a-z0-9-]+$/.test(opts.name)) fail(`bad name ${opts.name}`);
if (opts.target.length === 0) fail('at least one --target is required');
const scope = opts.scope ?? '@lmctl-ai';
const description = opts.description ?? `${opts.name} command line tool`;

const OS = { linux: 'linux', darwin: 'darwin', windows: 'win32' };
const CPU = { x64: 'x64', arm64: 'arm64' };

rmSync(opts.out, { recursive: true, force: true });
mkdirSync(opts.out, { recursive: true });

const platforms = [];
for (const spec of opts.target) {
  const eq = spec.indexOf('=');
  const platform = spec.slice(0, eq), asset = spec.slice(eq + 1);
  const [os, cpu, extra] = platform.split('-');
  if (eq < 0 || !OS[os] || !CPU[cpu] || extra) fail(`bad --target ${spec}`);
  const source = join(opts.assets, asset);
  if (!existsSync(source)) fail(`asset ${asset} not found in ${opts.assets}`);
  const exe = os === 'windows' ? `${opts.name}.exe` : opts.name;
  const pkgName = `${scope}/${opts.name}-${platform}`;
  const dir = join(opts.out, `${opts.name}-${platform}`);
  mkdirSync(join(dir, 'bin'), { recursive: true });
  copyFileSync(source, join(dir, 'bin', exe));
  if (os !== 'windows') chmodSync(join(dir, 'bin', exe), 0o755);
  if (opts.license) copyFileSync(opts.license, join(dir, 'LICENSE'));
  writeJson(join(dir, 'package.json'), {
    name: pkgName, version: opts.version, description: `${description} (${platform} binary)`,
    os: [OS[os]], cpu: [CPU[cpu]], files: ['bin', 'LICENSE'], license: 'MIT',
    repository: { type: 'git', url: `git+https://github.com/${opts.repo}.git` }, publishConfig: { access: 'public' },
  });
  writeFileSync(join(dir, 'README.md'), `# ${pkgName}\n\nThe ${platform} binary of [${scope}/${opts.name}](https://www.npmjs.com/package/${scope}/${opts.name}). Install that package instead.\n`);
  platforms.push({ platform, pkgName, os: OS[os], cpu: CPU[cpu], exe, dir });
}

const main = join(opts.out, opts.name);
mkdirSync(join(main, 'bin'), { recursive: true });
if (opts.license) copyFileSync(opts.license, join(main, 'LICENSE'));
const table = Object.fromEntries(platforms.map((p) => [`${p.os}-${p.cpu}`, { pkg: p.pkgName, exe: p.exe }]));
writeFileSync(join(main, 'bin', `${opts.name}.js`), launcher(opts.name, table), { mode: 0o755 });
chmodSync(join(main, 'bin', `${opts.name}.js`), 0o755);
writeJson(join(main, 'package.json'), {
  name: `${scope}/${opts.name}`, version: opts.version, description,
  bin: { [opts.name]: `bin/${opts.name}.js` }, files: ['bin', 'LICENSE', 'README.md'], license: 'MIT',
  engines: { node: '>=18' },
  optionalDependencies: Object.fromEntries(platforms.map((p) => [p.pkgName, opts.version])),
  repository: { type: 'git', url: `git+https://github.com/${opts.repo}.git` },
  homepage: `https://github.com/${opts.repo}#readme`, publishConfig: { access: 'public' },
});
writeFileSync(join(main, 'README.md'), `# ${scope}/${opts.name}\n\n${description}\n\n\`\`\`sh\nnpm install -g ${scope}/${opts.name}\n${opts.name} --help\n\`\`\`\n\nThe package holds a small launcher; the native binary comes from the matching platform package (${platforms.map((p) => p.platform).join(', ')}). Source and issues: https://github.com/${opts.repo}\n`);

for (const p of [...platforms, { pkgName: `${scope}/${opts.name}`, dir: main, platform: 'main' }]) {
  console.log(JSON.stringify({ name: p.pkgName, version: opts.version, dir: p.dir }));
}

function launcher(name, table) {
  return `#!/usr/bin/env node
'use strict';
// Starts the ${name} binary of this platform (installed as an optional dependency) and passes everything through.
const { spawnSync } = require('node:child_process');
const path = require('node:path');
const table = ${JSON.stringify(table, null, 2)};
const key = process.platform + '-' + process.arch;
const entry = table[key];
if (!entry) {
  console.error('${name}: no binary for ' + key + '; supported: ' + Object.keys(table).join(', '));
  process.exit(1);
}
let binary;
try {
  binary = path.join(path.dirname(require.resolve(entry.pkg + '/package.json')), 'bin', entry.exe);
} catch {
  console.error('${name}: the platform package ' + entry.pkg + ' is not installed (was it installed with --omit=optional?). Reinstall with optional dependencies enabled.');
  process.exit(1);
}
const result = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit' });
if (result.error) {
  console.error('${name}: cannot run ' + binary + ': ' + result.error.message);
  process.exit(1);
}
if (result.signal) process.kill(process.pid, result.signal);
process.exit(result.status ?? 1);
`;
}
function writeJson(file, value) { writeFileSync(file, JSON.stringify(value, null, 2) + '\n'); }
function fail(message) { console.error(`npm-packages: ${message}`); process.exit(2); }
