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

# Hermetic host: lminstall is a standalone Go program and must not need npm, git,
# uv, curl, tar or any installer on PATH. Native providers see an empty PATH;
# JavaScript providers (run by the installed package, not installed by it) see a
# directory holding only `node`. Hermes' own vendor PM runs from its pinned tools.
hermetic=root/'hermetic'
empty=hermetic/'empty'; nodeonly=hermetic/'node-only'
empty.mkdir(parents=True,exist_ok=True); nodeonly.mkdir(parents=True,exist_ok=True)
node=shutil.which('node'); assert node,'node is required to run JavaScript providers'
target=nodeonly/pathlib.Path(node).name
if not target.exists():
 try: target.symlink_to(node)
 except OSError: shutil.copy2(node,target)
# Vendor wrapper scripts need the OS's own shell/sysctl, never an installer.
if sys.platform=='win32':
 sysroot=os.environ.get('SystemRoot','C:\\Windows')
 os_dirs=[os.path.join(sysroot,'System32'),os.path.join(sysroot,'System32','WindowsPowerShell','v1.0')]  # OS components: .cmd shims run through PowerShell
else:
 os_dirs=[]
 if sys.platform=='darwin': os_dirs=['/usr/sbin','/sbin']
 for name in ('sh','dirname'):  # runtime needs of vendor wrapper scripts, not installers
  found=shutil.which(name)
  for d in (empty,nodeonly):
   link=d/name
   if found and not link.exists(): link.symlink_to(found)
check=('npm','git','uv') if sys.platform=='win32' else ('npm','git','uv','curl','tar','unzip')
for tool in check:
 for d in (empty,nodeonly): assert shutil.which(tool,path=os.pathsep.join([str(d)]+os_dirs)) is None,(tool,d)
JS={'gemini','pi','dsh','miniplayer'}
HERMES={'hermes'}
def host_path(name):
 if name in HERMES: return None
 return os.pathsep.join([str(nodeonly if name in JS else empty)]+os_dirs)

def call(op,name):
 print(json.dumps({'provider':name,'operation':op,'event':'start'}),flush=True)
 env=os.environ.copy()
 hp=host_path(name)
 if hp:
  env['PATH']=hp
  print(json.dumps({'provider':name,'hermetic_path':hp}),flush=True)
 elif sys.platform=='linux' and name in HERMES:
  env['PATH']=str(minimal)
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
