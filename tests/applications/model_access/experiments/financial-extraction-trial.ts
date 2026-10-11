// Explicit opt-in laboratory runner; never imported by production entrypoints.
import "../../../../src/factorforge/applications/model_access/entrypoints/omp-environment.js";
import fs from "node:fs/promises";
import path from "node:path";
import { createHash } from "node:crypto";
import { spawn, type ChildProcess } from "node:child_process";
import { ConfigStore } from "../../../../src/factorforge/applications/model_access/config/store.js";
import { OMPAccess } from "../../../../src/factorforge/applications/model_access/adapters/omp-access.js";
import { OMPService } from "../../../../src/factorforge/applications/model_access/application/omp-service.js";
import { ModelError } from "../../../../src/factorforge/applications/model_access/api/protocol.js";
import { postmortem } from "../../../../src/factorforge/applications/model_access/node_modules/@oh-my-pi/pi-utils/src/index.ts";

const [rootArg, dirArg, binaryArg, promptArg] = process.argv.slice(2);
if (!rootArg || !dirArg || !binaryArg || !promptArg || process.argv.length !== 6) throw Error("FINANCIAL_TRIAL_USAGE");
const root = path.resolve(rootArg), dir = path.resolve(dirArg), binary = path.resolve(binaryArg), prompt = path.resolve(promptArg);
// The corpus lives in the isolated checkout; only canonical model config/prompt
// use the deployment root. Never create a copied credential/config snapshot.
const checkout = path.resolve(import.meta.dirname,"../../../..");
if (await fs.realpath(root) !== root || await fs.realpath(checkout) !== checkout || path.dirname(dir)!==path.join(checkout,"runtime") || !prompt.startsWith(path.join(root,"prompts")+path.sep)) throw Error("FINANCIAL_TRIAL_PATH");
if (await fs.lstat(path.join(dir,"seal.json")).then(()=>true,()=>false)) throw Error("FINANCIAL_TRIAL_ALREADY_SEALED");
const config=path.join(root,"config","config.toml");
const hash=(s:string|Uint8Array)=>createHash("sha256").update(s).digest("hex");
const trial={enabled:true,prompt_file:path.relative(root,prompt),timeout_seconds:120,max_input_bytes:65536,max_output_bytes:32768,max_line_bytes:1048576,omp_max_requests:0,budget_window_seconds:0};
let child:ChildProcess|undefined,store:ConfigStore|undefined,access:OMPAccess|undefined;
let changed=false,stopped=false,stage="config",promptHash="";
const saved:Record<string,unknown>={};
const cancelled=new AbortController();
let done!:()=>void;const finished=new Promise<void>(resolve=>done=resolve);
const stop=()=>{stopped=true;cancelled.abort();child?.kill("SIGTERM")};
const unregister=postmortem.register("factorforge-financial-extraction-trial",async()=>{stop();await finished});
process.on("SIGINT",stop);process.on("SIGTERM",stop);
async function run(mode:string) {
  if(stopped&&mode!=="evaluate")throw Error("FINANCIAL_TRIAL_CANCELLED");
  return await new Promise<number>((resolve,reject)=>{
    child=spawn(binary,["-test.run=^TestFinancialExtractionTrial$","-test.v","-test.timeout=35m"],{cwd:path.join(checkout,"tests","applications","soxl_jev","experiments"),stdio:"inherit",windowsHide:true,env:{...process.env,FACTORFORGE_FINANCIAL_EXTRACTION_MODE:mode,FACTORFORGE_FINANCIAL_EXTRACTION_ROOT:checkout,FACTORFORGE_FINANCIAL_EXTRACTION_CONFIG_ROOT:root,FACTORFORGE_FINANCIAL_EXTRACTION_DIR:dir,FACTORFORGE_FINANCIAL_EXTRACTION_PROMPT_HASH:promptHash,FACTORFORGE_FINANCIAL_EXTRACTION_BUN:process.execPath}});
    child.once("error",()=>{child=undefined;reject(Error("FINANCIAL_TRIAL_CHILD"))});
    child.once("exit",code=>{child=undefined;resolve(code??1)});
  });
}
try {
  store=await ConfigStore.open(config);
  for(const k of Object.keys(trial))saved[k]=(store.settings as any)[k];
  const originalPrompt=store.settings.prompt_file,originalInput=store.settings.max_input_bytes;
  stage="prompt";
  try{store.settings.prompt_file=trial.prompt_file;store.settings.max_input_bytes=trial.max_input_bytes;promptHash=hash(await store.prompt())}
  finally{store.settings.prompt_file=originalPrompt;store.settings.max_input_bytes=originalInput}
  // Prompt/protocol were sealed before remaining-case selection; historical corpus use is disclosed.
  const pre=JSON.parse(await fs.readFile(path.join(checkout,"runtime","financial-extraction-metadata","pre-access.json"),"utf8"));
  if(pre.remaining_cases_selected!==false||pre.prompt_hash!==promptHash||pre.protocol_hash!==hash(await fs.readFile(path.join(checkout,"doc","engineering","P3_FINANCIAL_EXTRACTION_PROTOCOL.md"))))throw Error("FINANCIAL_PREACCESS_CHANGED");
  stage="prepare";if(await run("prepare")!==0)throw Error("FINANCIAL_PREPARE_FAILED");
  await fs.writeFile(path.join(dir,"restore-settings.json"),JSON.stringify(saved,null,2),{flag:"wx",mode:0o600});
  await fs.writeFile(path.join(dir,"runner-seal.json"),JSON.stringify({at:new Date().toISOString(),runner_hash:hash(await fs.readFile(import.meta.filename)),binary_hash:hash(await fs.readFile(binary)),local_budget:"unlimited",planned_calls:12,prompt_hash:promptHash},null,2),{flag:"wx",mode:0o600});
  if(stopped)throw Error("FINANCIAL_CANCELLED");
  changed=true;Object.assign(store.settings,trial);store.saveSync();
  stage="catalog";access=new OMPAccess(store);await access.ready();
  const catalog=await new OMPService(store,access).handle({v:2,id:"financial_extraction_catalog",op:"models",provider:"google-antigravity",account_id:1},cancelled.signal);
  if(!catalog.ok||!(catalog as any).result.models.some((m:any)=>m.id==="gemini-3.8-flash"))throw Error("FINANCIAL_MODEL_UNAVAILABLE");
  access.close();access=undefined;await store.close();store=undefined;
  stage="generate";const result=await run("run");
  stage="evaluate";const evaluated=await run("evaluate");
  if(result!==0||evaluated!==0)throw Error("FINANCIAL_INCOMPLETE");
} catch(error) {
  process.exitCode=1;
  console.error(JSON.stringify({code:error instanceof ModelError?error.code:"FINANCIAL_FAILED_OR_INCOMPLETE",stage}));
} finally {
  try{
    access?.close();access=undefined;
    if(changed){
      store??=await ConfigStore.open(config);
      for(const[k,v]of Object.entries(trial))if((store.settings as any)[k]!==v)throw Error("FINANCIAL_SETTINGS_CHANGED_RECONCILE");
      Object.assign(store.settings,saved);store.saveSync();
      console.log(JSON.stringify({settings_restored:true,local_budget:"unlimited"}));
    }
  }finally{await store?.close();done();unregister();process.off("SIGINT",stop);process.off("SIGTERM",stop)}
}
