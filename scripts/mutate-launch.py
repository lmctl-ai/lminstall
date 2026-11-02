#!/usr/bin/env python3
"""Production-copy controls for profile, routing and credential boundaries."""
import pathlib,shutil,subprocess,tempfile
root=pathlib.Path(__file__).resolve().parents[1]
cases=[
 ('credential persisted','internal/launch/profile.go','CredentialEnv: o.CredentialEnv','CredentialEnv: os.Getenv(o.CredentialEnv)'),
 ('duplicated v1','internal/launch/profile.go','strings.TrimRight(base, "/") + "/v1"','strings.TrimRight(base, "/") + "/v1/v1"'),
 ('adapter files omitted','internal/launch/profile.go','for _, f := range p.Spec.Files {','for _, f := range []install.LaunchFile{} {'),
 ('inherited secret','internal/launch/run.go','ok && keep[strings.ToUpper(k)]','ok && (keep[strings.ToUpper(k)] || strings.Contains(k,"SECRET"))'),
 ('response credential reaches child','internal/launch/bridge.go','filter := redactor(w, patterns)','filter := redactor(w, nil)'),
 ('redaction disabled','internal/launch/redact.go','patterns: patterns','patterns: nil'),
 ('foreign profile adopted','internal/launch/profile.go','e != nil || len(entries) != 0','e != nil || (false && len(entries) != 0)'),
 ('native npm through Node','internal/install/launch_exec.go','if strings.EqualFold(filepath.Ext(path), ".exe") {','if false && strings.EqualFold(filepath.Ext(path), ".exe") {'),
 ('wrong reply accepted','cmd/lminstall-mock-e2e/main.go','if observed != wanted {','if false {'),
 ('foreign gateway allowed','internal/launch/cleanup.go','got != expected','false && got != expected'),
]
with tempfile.TemporaryDirectory(prefix='lminstall-launch-mut-') as tmp:
 copy=pathlib.Path(tmp)/'source';shutil.copytree(root,copy,ignore=shutil.ignore_patterns('.git','build','__pycache__'))
 originals={file:(copy/file).read_text() for _,file,_,_ in cases}
 def run():return subprocess.run(['go','test','-count=1','./...'],cwd=copy,capture_output=True,text=True)
 result=run();assert result.returncode==0,result.stdout+result.stderr;print('BASELINE PASS',flush=True)
 for name,file,before,after in cases:
  for path,text in originals.items():(copy/path).write_text(text)
  assert originals[file].count(before)==1,('anchor',name,originals[file].count(before))
  (copy/file).write_text(originals[file].replace(before,after))
  result=run();assert result.returncode!=0,('SURVIVED',name)
  assert '--- FAIL: Test' in result.stdout and '[build failed]' not in result.stdout,result.stdout+result.stderr
  print('CAUGHT '+name,flush=True)
 print(f'PASS {len(cases)} launch controls',flush=True)
