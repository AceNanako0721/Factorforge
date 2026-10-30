// Disposable production-image checks. No model authentication or external network.
import fs from 'node:fs';
import path from 'node:path';
import {gunzipSync} from 'node:zlib';
import {spawnSync,execFileSync} from 'node:child_process';
if(process.argv[2]!=='--run')throw Error('Explicit --run required');
const root=execFileSync('git',['rev-parse','--show-toplevel'],{encoding:'utf8'}).trim();
const id=Date.now()+'-'+process.pid,lab=path.join(root,'runtime/runtime-acceptance-'+id),network='ff-runtime-lab-'+id;
fs.mkdirSync(lab,{recursive:true,mode:0o700});
const checks=[],containers=[];
const run=(args)=>{const r=spawnSync('docker',args,{encoding:'utf8',timeout:120000});if(r.status!==0)throw Error('docker failed '+args.slice(0,3).join(' ')+': '+r.stderr);return r.stdout.trim();};
const wait=ms=>new Promise(r=>setTimeout(r,ms));
const base=['--user',process.getuid()+':'+process.getgid(),'--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--memory','256m','--pids-limit','128','--tmpfs','/tmp:rw,size=64m','--tmpfs','/var/lib/postgresql/data:rw,size=1m'];
const mount=(from,to,ro=false)=>['--mount',`type=bind,src=${from},dst=${to}${ro?',readonly':''}`];
const newLogs=name=>{const p=path.join(lab,name);fs.mkdirSync(p,{mode:0o700});return p;};
const readLogs=p=>fs.readdirSync(p,{recursive:true}).filter(f=>f.endsWith('.gz')||f.endsWith('.open')).map(f=>{const b=fs.readFileSync(path.join(p,f));return f.endsWith('.gz')?gunzipSync(b).toString():b.toString();}).join('');
const launch=(suffix,args)=>{const name='ff-runtime-'+id+'-'+suffix;containers.push(name);run(['run','-d','--name',name,'--init',...base,...args]);return name;};
try{
 const logs=newLogs('signal');const n=launch('signal',['--network','none',...mount(logs,'/logs'),'factorforge-core:2.20.0','sh','-c',"trap 'echo term-tail; exit 0' TERM; echo ready; while :; do sleep 1; done"]);
 for(let i=0;i<40&&!run(['logs',n]).includes('ready');i++)await wait(100);
 run(['stop','-t','10',n]);if(!readLogs(logs).includes('term-tail'))throw Error('SIGTERM tail missing');checks.push('PASS production supervisor forwards SIGTERM and seals tail');
 const second=launch('recreate',['--network','none',...mount(logs,'/logs'),'factorforge-core:2.20.0','sh','-c','echo rebuilt; exit 37']);if(run(['wait',second])!=='37')throw Error('exit status lost');const text=readLogs(logs);if(!text.includes('term-tail')||!text.includes('rebuilt'))throw Error('archive lost on recreation');checks.push('PASS exit 37 and archived logs survive replacement');
 const readonly=newLogs('readonly');const denied=launch('readonly',['--network','none',...mount(readonly,'/logs',true),'factorforge-core:2.20.0','sh','-c','echo must-not-run']);if(run(['wait',denied])!=='73'||run(['logs',denied]).includes('must-not-run'))throw Error('unwritable logs admitted child');checks.push('PASS unwritable logs reject startup before child');
 run(['network','create','--internal',network]);
 const backend=launch('backend',['--network',network,'--network-alias','trading-api',...mount(newLogs('backend'),'/logs'),'factorforge-model:2.20.0','bun','-e','Bun.serve({hostname:"0.0.0.0",port:8000,fetch(){return new Response("synthetic-tls-ok")}})']);
 const acceptance=fs.readFileSync(path.join(root,'runtime/deploy-acceptance-last.txt'),'utf8').trim();const tls=path.join(acceptance,'data/tls');
 const gateway=launch('gateway',['--network',network,'--network-alias','gateway',...mount(newLogs('gateway'),'/logs'),...mount(path.join(tls,'gateway'),'/tls',true),'factorforge-core:2.20.0','ff-runtime','gateway']);
 const tlsClient=launch('tls-client',['--network',network,...mount(newLogs('tls-client'),'/logs'),...mount(path.join(tls,'trust'),'/trust',true),'factorforge-model:2.20.0','bun','-e','for(let i=0;i<30;i++){try{const r=await fetch("https://gateway:8443",{tls:{ca:await Bun.file("/trust/ca.pem").text(),rejectUnauthorized:true}});const b=await r.text();if(b==="synthetic-tls-ok"){console.log(b);process.exit(0)}}catch{}await Bun.sleep(200)}process.exit(1)']);
 if(run(['wait',tlsClient])!=='0'||!run(['logs',tlsClient]).includes('synthetic-tls-ok'))throw Error('verified TLS routing failed');checks.push('PASS production TLS gateway verifies private CA and proxies request');
 const privateConfig=newLogs('config');fs.copyFileSync(path.join(root,'config/config.example.toml'),path.join(privateConfig,'config.toml'));fs.chmodSync(path.join(privateConfig,'config.toml'),0o600);
 const model=launch('model',['--network','none',...mount(newLogs('model'),'/logs'),...mount(privateConfig,'/private/config'),...mount(newLogs('model-state'),'/private/runtime'),'factorforge-model:2.20.0','bun','/app/runtime/model-access-build/entrypoints/omp-cli.js','status','--config','/private/config/config.toml']);
 if(run(['wait',model])!=='0'||!run(['logs',model]).includes('"ok":true'))throw Error('OMP offline status failed');checks.push('PASS model image loads native OMP runtime and returns offline status');
}finally{
 for(const c of containers){spawnSync('docker',['stop','-t','10',c]);spawnSync('docker',['rm',c]);}spawnSync('docker',['network','rm',network]);
 fs.writeFileSync(path.join(lab,'result.json'),JSON.stringify({checks,syntheticOnly:true},null,2));fs.writeFileSync(path.join(root,'runtime/runtime-acceptance-last.txt'),lab+'\n');
}
console.log(checks.join('\n'));console.log('Evidence: '+lab);
