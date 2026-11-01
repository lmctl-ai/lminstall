#!/usr/bin/env python3
"""Small production-copy controls; each must fail a real Go test assertion."""
import pathlib,shutil,subprocess,tempfile
root=pathlib.Path(__file__).resolve().parents[1]
cases=[
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
  assert originals[file].count(before)==1,('anchor',name)
  (copy/file).write_text(originals[file].replace(before,after))
  r=run();assert r.returncode!=0,('SURVIVED',name)
  assert '--- FAIL: Test' in r.stdout and '[build failed]'not in r.stdout,r.stdout+r.stderr
  print('CAUGHT '+name,flush=True)
 print('PASS 6 mutants',flush=True)
