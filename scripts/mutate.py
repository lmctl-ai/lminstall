#!/usr/bin/env python3
"""Small production-copy controls; each must fail a real Go test assertion."""
import pathlib,shutil,subprocess,tempfile
root=pathlib.Path(__file__).resolve().parents[1]
cases=[
 ('Hermes installed Rust','internal/install/manager.go','if r.Channel == "hermes" {','if false {'),
 ('Kimi archive binary hash','internal/install/native.go','if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), checksum) {','if false && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), checksum) {'),
 ('native receipt migration','internal/install/manager.go','&& rec.Channel != r.Channel {','&& false {'),
 ('Kimi Windows platform','internal/install/native.go','osName = "win32"','osName = "windows"'),
 ('Hermes availability mirror','internal/install/verified_download.go','if !downloadAvailabilityError(err) {','if true {'),
 ('npm working directory','internal/install/manager.go','e = run(a, dir)','e = run(a, "")'),
 ('DSH vendor source','internal/install/providers.json','https://github.com/deepseek-ai/deepseek-harness','https://deepseekdocs.com/en/docs/user-guide/cli'),
 ('Hermes PATH refresh','internal/install/hermes.go','if e := os.RemoveAll(bin); e != nil {','if e := error(nil); e != nil {'),
 ('catalog replacement','cmd/lminstall/main.go','manifest = install.Manifest{}','// deliberately retain embedded catalog'),
 ('missing release digest','internal/install/archive.go','if digest == "" {\n\t\tif r.Checksums == "" {','if false {\n\t\tif r.Checksums == "" {'),
 ('redirect downgrade','internal/install/archive.go','if next.URL.Scheme != "https" {','if false {'),
 ('stable launcher','internal/install/manager.go','if e = m.checkLauncher(p, rec.Bin); e != nil {','if false {'),
 ('release version','internal/install/archive.go','if e = m.verifySelectedVersion(ctx, p, r, rel, release.Tag); e != nil {','if false {'),
 ('update version','internal/install/update.go','if e = m.verifySelectedVersion(ctx, p, r, stagedBin, meta.Version); e != nil {','if e = m.verifySelectedVersion(ctx, p, r, stagedBin, meta.Version); false {'),
 ('Kimi catalog','internal/install/providers.json','"channel": "kimi"','"channel": "npm"'),
 ('Muse catalog','internal/install/providers.json','"channel": "muse"','"channel": "steps"'),
 ('Hermes catalog','internal/install/providers.json','"channel": "hermes"','"channel": "project"'),
 ('Kimi/Muse hashes','internal/install/native.go','"sha256:"+asset.Checksum','""'),
 ('Muse size','internal/install/native.go','if stat.Size() != asset.Size {','if false {'),
 ('Hermes UV hash','internal/install/hermes.go','"sha256:"+asset.SHA256','""'),
 ('unavailable exit','cmd/lminstall/main.go','code = 3','code = 0'),
 ('invalid CLI exit','cmd/lminstall/main.go','return 2','return 0'),
 ('owner marker creation','internal/install/manager.go','[]byte(id+"\\n"), 0600); e != nil {','[]byte(""), 0600); e != nil {'),
 ('idempotency','internal/install/manager.go','if op == "install" {','if false {'),
 ('version validation','internal/install/manager.go','if !versionNumber.MatchString(out) {','if false {'),
 ('ownership','internal/install/manager.go','if e != nil || string(b) != id+"\\n" {','if false && (e != nil || string(b) != id+"\\n") {'),
 ('checksum','internal/install/archive.go','if len(want) != len(hash.Sum(nil))*2 || !strings.EqualFold(want, hex.EncodeToString(hash.Sum(nil))) {','if false && (len(want) != len(hash.Sum(nil))*2 || !strings.EqualFold(want, hex.EncodeToString(hash.Sum(nil)))) {'),
 ('unbounded capture','internal/install/runner.go','return out.String(), nil','return out.String()[:min(len(out.String()),1024)], nil'),
 ('failure priority','cmd/lminstall/main.go','if code == 0 {','if true {'),
]
with tempfile.TemporaryDirectory(prefix='lminstall-mut-') as d:
 copy=pathlib.Path(d)/'source';shutil.copytree(root,copy,ignore=shutil.ignore_patterns('.git','build','__pycache__'))
 originals={name:(copy/name).read_text() for _,name,_,_ in cases}
 def run():return subprocess.run(['go','test','-count=1','./...'],cwd=copy,capture_output=True,text=True)
 r=run();assert r.returncode==0,r.stdout+r.stderr;print('BASELINE PASS',flush=True)
 for name,file,before,after in cases:
  for path,src in originals.items():(copy/path).write_text(src)
  assert originals[file].count(before)>=1,('anchor',name)
  (copy/file).write_text(originals[file].replace(before,after))
  r=run();assert r.returncode!=0,('SURVIVED',name)
  assert '--- FAIL: Test' in r.stdout and '[build failed]'not in r.stdout,r.stdout+r.stderr
  print('CAUGHT '+name,flush=True)
 print(f'PASS {len(cases)} mutants',flush=True)
