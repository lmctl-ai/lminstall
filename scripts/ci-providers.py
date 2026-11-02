#!/usr/bin/env python3
"""Live installs, never model prompts. Attempt the whole catalog and retain failures."""
import json,os,pathlib,subprocess,sys,shutil
sys.stdout.reconfigure(encoding='utf-8',errors='backslashreplace')
sys.stderr.reconfigure(encoding='utf-8',errors='backslashreplace')
binary=str(pathlib.Path(sys.argv[1]).resolve())
root=pathlib.Path(os.environ['RUNNER_TEMP'])/'lminstall live root 雪'
summary=[]
# Linux native-provider rows deliberately cannot find host Node/npm/uv.
# Keep other ordinary OS build tools available to the upstream Hermes PM.
minimal=root/'no-node-tools'
if sys.platform=='linux':
 minimal.mkdir(parents=True,exist_ok=True)
 excluded={'node','nodejs','npm','npx','uv','uvx','sudo','doas','su'}
 for directory in ['/usr/bin','/bin']:
  for entry in pathlib.Path(directory).iterdir():
   dest=minimal/entry.name
   if entry.name not in excluded and entry.is_file() and not dest.exists():
    dest.symlink_to(entry)
 for name in excluded:
  assert shutil.which(name,path=str(minimal)) is None,name

def call(op,name):
 print(json.dumps({'provider':name,'operation':op,'event':'start'}),flush=True)
 env=os.environ.copy()
 if sys.platform=='linux' and name in {'kimi','agy','muse','hermes'}:
  env['PATH']=str(minimal)
  print(json.dumps({'provider':name,'no_host_node_npm_uv':True}),flush=True)
 p=subprocess.run([binary,op,name,'--root',str(root),'--json'],stdout=subprocess.PIPE,stderr=None,env=env)
 try:rows=json.loads(p.stdout);row=rows[0]
 except (ValueError,IndexError):row={'reason':p.stdout.decode(errors='replace')}
 return p.returncode,row
catalog=json.loads(subprocess.check_output([binary,'list','--json']))
failed=False
for entry in catalog:
 name=entry['provider'];record={'provider':name,'platform':sys.platform,'state':'failed'}
 try:
  code,installed=call('install',name)
  if entry['state']=='unavailable':
   assert code==3 and installed['state']=='unavailable' and installed['reason'],installed
   record.update(state='unavailable',reason=installed['reason']);continue
  assert code==0 and installed['state']=='installed' and installed['version'],installed
  receipt=root/'providers'/name/'receipt.json';stamp=receipt.stat().st_mtime_ns
  code,repeated=call('install',name)
  assert code==0 and repeated['state']=='installed' and receipt.stat().st_mtime_ns==stamp,repeated
  code,status=call('status',name);assert code==0 and status['state']=='installed',status
  code,upgraded=call('upgrade',name)
  assert code==0 and upgraded['state']=='installed' and upgraded['bin_dir']==installed['bin_dir'],upgraded
  record.update(state='verified',version=upgraded['version'],channel=upgraded['channel'])
 except Exception as error:
  failed=True;record['reason']=str(error);print('FAILED '+name+': '+str(error),flush=True)
 finally:
  if entry['state']!='unavailable':
   code,removed=call('uninstall',name)
   if code!=0:failed=True;record['state']='failed';record['cleanup_error']=str(removed)
   code,status=call('status',name)
   if code!=0 or status['state']!='absent':failed=True;record['state']='failed';record['cleanup_status']=str(status)
  summary.append(record)
  pathlib.Path('provider-results.json').write_text(json.dumps(summary,indent=2)+'\n',encoding='utf-8')
  print(json.dumps(record),flush=True)
pathlib.Path('provider-results.json').write_text(json.dumps(summary,indent=2)+'\n',encoding='utf-8')
with open(os.environ['GITHUB_STEP_SUMMARY'],'a',encoding='utf-8') as out:
 out.write('| Provider | Outcome | Version / precise reason |\n|---|---|---|\n')
 for r in summary:out.write('| '+r['provider']+' | '+r['state']+' | '+r.get('version',r.get('reason','')).replace('\n',' ').replace('|','\\|')+' |\n')
sys.exit(1 if failed else 0)
