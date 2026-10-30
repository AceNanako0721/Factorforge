// Explicit, disposable acceptance of the production deployment tools.
import fs from 'node:fs';
import path from 'node:path';
import {execFileSync,spawnSync} from 'node:child_process';
import {createRequire} from 'node:module';
if(process.argv[2]!=='--run')throw Error('Explicit --run required');
const root=execFileSync('git',['rev-parse','--show-toplevel'],{encoding:'utf8'}).trim();
const require=createRequire(path.join(root,'src/factorforge/applications/model_access/package.json'));
const {parse,stringify}=require('smol-toml');
const id=`${Date.now()}-${process.pid}`,project=`factorforge-lab-${id}`;
const lab=path.join(root,'runtime',`deploy-acceptance-${id}`),repo=path.join(lab,'repo'),data=path.join(lab,'data');
fs.mkdirSync(path.join(repo,'config'),{recursive:true,mode:0o700});fs.mkdirSync(path.join(repo,'prompts'),{recursive:true});fs.mkdirSync(path.join(repo,'tools/deploy'),{recursive:true});
for(const f of ['go.mod','VERSION','config/config.example.toml','tools/deploy/compose.yaml'])fs.copyFileSync(path.join(root,f),path.join(repo,f));
const tool=path.join(root,'runtime/deploy'),log=[];
const run=(name,args,options={})=>{const r=spawnSync(name,args,{cwd:repo,encoding:'utf8',timeout:180000,...options});if(r.status!==0)throw Error(`${name} failed: ${r.status}\n${r.stdout||''}\n${r.stderr||''}`);return r.stdout||'';};
const compose=(...args)=>run('docker',['compose','--env-file',path.join(repo,'runtime/deployment/compose.env'),'-p',project,'-f',path.join(repo,'tools/deploy/compose.yaml'),...args]);
const snapshot=()=>{const ids=run('docker',['ps','-q','--no-trunc']).trim().split(/\s+/).filter(Boolean);return Object.fromEntries(ids.map(id=>[id,run('docker',['inspect','--format','{{.State.StartedAt}}',id]).trim()]));};
const before=snapshot();let started=false;
try{
 run(tool,['init','--data-root',data]);const cfg=path.join(repo,'config/config.toml');const c=parse(fs.readFileSync(cfg,'utf8'));c.deployment.project=project;c.strategy.instance_id='deployment-fixture';fs.writeFileSync(cfg,stringify(c),{mode:0o600});
 run(tool,['prepare']);
 const doctor=spawnSync(tool,['doctor'],{cwd:repo,encoding:'utf8'});if(doctor.status===0||!doctor.stdout.includes('MISSING profile.'))throw Error('Missing configuration was admitted');log.push('PASS missing application config refuses startup');
 started=true;run(tool,['infra-up']);run(tool,['schema']);
 compose('exec','-T','postgres','psql','-U','ff_admin','-d','factorforge','-v','ON_ERROR_STOP=1','-c',"CREATE TABLE deployment_probe(id integer PRIMARY KEY, note text); INSERT INTO deployment_probe VALUES(1,'synthetic-durable'); GRANT SELECT ON deployment_probe TO ff_p1;");
 run(tool,['schema']);if(!compose('exec','-T','postgres','psql','-U','ff_admin','-d','factorforge','-Atc','SELECT note FROM deployment_probe').includes('synthetic-durable'))throw Error('Schema rerun lost data');log.push('PASS schema rerun keeps data and role grants');
 const old=compose('ps','-q','postgres').trim();compose('up','-d','--force-recreate','--wait','postgres');const fresh=compose('ps','-q','postgres').trim();if(old===fresh)throw Error('container was not replaced');if(!compose('exec','-T','postgres','psql','-U','ff_admin','-d','factorforge','-Atc','SELECT note FROM deployment_probe').includes('synthetic-durable'))throw Error('rebuild lost data');log.push('PASS production postgres wrapper and data mount survive recreation');
 const backupOutput=run(tool,['backup']);const backup=backupOutput.split('\n').find(s=>s.startsWith('Backup complete: '))?.slice('Backup complete: '.length);if(!backup)throw Error('No completed backup');
 const target=path.join(lab,'restored');run(tool,['restore','--source',backup,'--destination',target]);if(!fs.existsSync(path.join(target,'RESTORED.json')))throw Error('restore incomplete');const verifier='ff-restore-check-'+id;
 try {
 run('docker',['run','-d','--name',verifier,'--network','none','--user',process.getuid()+':'+process.getgid(),'--memory','512m','--read-only','--cap-drop','ALL','--tmpfs','/tmp:rw,size=64m','--tmpfs','/var/run/postgresql:rw,size=16m','-e','PGDATA=/var/lib/postgresql/data/pgdata','--mount','type=bind,src='+path.join(target,'postgres')+',dst=/var/lib/postgresql/data','--mount','type=bind,src='+path.join(target,'logs')+',dst=/logs','factorforge-postgres:2.20.0']);
 let ready=false;for(let n=0;n<60;n++){if(spawnSync('docker',['exec',verifier,'pg_isready','-h','127.0.0.1','-U','ff_admin','-d','postgres']).status===0){ready=true;break;}Atomics.wait(new Int32Array(new SharedArrayBuffer(4)),0,0,500);}if(!ready)throw Error('restored database not ready');
 const sql="SET ROLE ff_p1; SELECT note FROM deployment_probe; RESET ROLE; SELECT rolsuper FROM pg_roles WHERE rolname='ff_p1'; SELECT pg_has_role('ff_p3_read','factorforge_instance_sim_read','MEMBER');";
 const restored=run('docker',['exec',verifier,'psql','-X','-U','ff_admin','-d','factorforge','-At','-v','ON_ERROR_STOP=1','-c',sql]);
 if(!restored.includes('synthetic-durable')||!restored.includes('\nf\nt\n'))throw Error('restored data/role grants mismatch');
 }finally{spawnSync('docker',['stop','-t','30',verifier]);spawnSync('docker',['rm',verifier]);}
 log.push('PASS checked backup restored actual sentinel and non-superuser role permissions');
 const again=spawnSync(tool,['restore','--source',backup,'--destination',target],{cwd:repo,encoding:'utf8'});if(again.status===0)throw Error('restore overwrote target');log.push('PASS restore refuses existing destination');
 const dump=path.join(backup,'database.dump');fs.appendFileSync(dump,'corrupt-fixture');const bad=spawnSync(tool,['restore','--source',backup,'--destination',path.join(lab,'corrupt-target')],{cwd:repo,encoding:'utf8'});if(bad.status===0||fs.existsSync(path.join(lab,'corrupt-target')))throw Error('corrupt backup accepted');log.push('PASS corrupted backup refused before target creation');
 const logDir=path.join(data,'logs/postgres');if(!fs.readdirSync(logDir).some(n=>/^\d{4}-\d{2}-\d{2}$/.test(n)))throw Error('dated logs absent');log.push('PASS persistent PostgreSQL logs use date directories');
}finally{
 if(started)compose('--profile','app','down','--timeout','30');
 const after=snapshot();for(const [id,time]of Object.entries(before)){if(after[id]!==time)throw Error('pre-existing container changed')}
 fs.writeFileSync(path.join(lab,'result.json'),JSON.stringify({checks:log,preExistingUnchanged:true,syntheticOnly:true},null,2));
 fs.writeFileSync(path.join(root,'runtime/deploy-acceptance-last.txt'),lab+'\n');
}
console.log(log.join('\n'));console.log(`Evidence: ${lab}`);
