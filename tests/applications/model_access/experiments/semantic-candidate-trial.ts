// Explicit finite development runner. Never imported by a production module.
import "../../../../src/factorforge/applications/model_access/entrypoints/omp-environment.js";
import fs from "node:fs/promises";
import path from "node:path";
import { createHash } from "node:crypto";
import { spawn, type ChildProcess } from "node:child_process";
import { ConfigStore } from "../../../../src/factorforge/applications/model_access/config/store.js";
import { OMPAccess } from "../../../../src/factorforge/applications/model_access/adapters/omp-access.js";
import { OMPService } from "../../../../src/factorforge/applications/model_access/application/omp-service.js";
import { postmortem } from "../../../../src/factorforge/applications/model_access/node_modules/@oh-my-pi/pi-utils/src/index.ts";

const [rootArg, labName, binaryArg, promptArg] = process.argv.slice(2);
if (!rootArg || !/^[A-Za-z0-9_-]+$/.test(labName ?? "") || !binaryArg || !promptArg || process.argv.length !== 6) throw Error("TRIAL_USAGE_INVALID");
const root = path.resolve(rootArg), binary = path.resolve(binaryArg), prompt = path.resolve(promptArg);
if (await fs.realpath(root) !== root || !prompt.startsWith(path.join(root,"prompts")+path.sep)) throw Error("TRIAL_PATH_INVALID");
const config = path.join(root,"config","config.toml"), dir = path.join(root,"runtime",labName!);
if (await fs.lstat(dir).then(()=>true,()=>false)) throw Error("TRIAL_DIRECTORY_EXISTS");
const cwd = path.resolve(import.meta.dirname,"../../soxl_jev/experiments");
const hash = (s: string | Uint8Array) => createHash("sha256").update(s).digest("hex");
const trial = {enabled:true,prompt_file:path.relative(root,prompt),timeout_seconds:120,max_input_bytes:65536,max_output_bytes:32768,max_line_bytes:1048576,omp_max_requests:0,budget_window_seconds:0};
let child: ChildProcess | undefined, store: ConfigStore | undefined, access: OMPAccess | undefined;
let changed = false, stopped = false;
let saved: Record<string,unknown> = {};
let promptHash = "";
let done!:()=>void;
const finished = new Promise<void>(resolve=>done=resolve);
const stop = () => {stopped=true;child?.kill("SIGTERM")};
const unregister = postmortem.register("factorforge-semantic-candidate-trial",async()=>{stop();await finished});
process.on("SIGINT",stop);process.on("SIGTERM",stop);
async function run(mode: string) {
  if(stopped && mode!=="evaluate") throw Error("TRIAL_CANCELLED");
  return await new Promise<number>((resolve,reject)=>{
    child=spawn(binary,["-test.run=^TestSemanticCandidateTrial$","-test.v","-test.timeout=15m"],{cwd,stdio:"inherit",windowsHide:true,env:{...process.env,FACTORFORGE_SEMANTIC_TRIAL_MODE:mode,FACTORFORGE_SEMANTIC_TRIAL_ROOT:root,FACTORFORGE_SEMANTIC_TRIAL_DIR:dir,FACTORFORGE_SEMANTIC_TRIAL_PROMPT_HASH:promptHash,FACTORFORGE_SEMANTIC_TRIAL_BUN:process.execPath}});
    child.once("error",()=>{child=undefined;reject(Error("TRIAL_CHILD_FAILED"))});
    child.once("exit",code=>{child=undefined;resolve(code ?? 1)});
  });
}
try {
  store=await ConfigStore.open(config);
  for(const key of Object.keys(trial)) saved[key]=(store.settings as any)[key];
  const originalPrompt=store.settings.prompt_file;
  // prompt() only loads the private file; no OAuth/catalog may persist this edit.
  try {store.settings.prompt_file=trial.prompt_file;promptHash=hash(await store.prompt())}
  finally {store.settings.prompt_file=originalPrompt}
  if(await run("prepare")!==0) throw Error("TRIAL_PREPARE_FAILED");
  // Only non-secret settings are backed up; credentials/state are never copied.
  await fs.writeFile(path.join(dir,"restore-settings.json"),JSON.stringify(saved,null,2),{flag:"wx",mode:0o600});
  await fs.writeFile(path.join(dir,"runner-seal.json"),JSON.stringify({at:new Date().toISOString(),runner_hash:hash(await fs.readFile(import.meta.filename)),binary_hash:hash(await fs.readFile(binary)),local_budget:"unlimited",planned_calls:6,prompt_hash:promptHash},null,2),{flag:"wx",mode:0o600});
  if(stopped) throw Error("TRIAL_CANCELLED");
  changed=true;Object.assign(store.settings,trial);store.saveSync();
  access=new OMPAccess(store);await access.ready();
  const catalog=await new OMPService(store,access).handle({v:2,id:"semantic_trial_catalog",op:"models",provider:"google-antigravity",account_id:1});
  if(!catalog.ok || !(catalog as any).result.models.some((m:any)=>m.id==="gemini-3.8-flash")) throw Error("TRIAL_MODEL_UNAVAILABLE");
  access.close();access=undefined;await store.close();store=undefined;
  const status=await run("run");
  const evaluation=await run("evaluate");
  if(status!==0||evaluation!==0) throw Error("TRIAL_INCOMPLETE_SEE_PRIVATE_OUTCOMES");
} catch {
  // No raw provider errors, responses or private instructions in terminal logs.
  process.exitCode=1;
  console.error("TRIAL_FAILED_OR_INCOMPLETE");
} finally {
  try {
    access?.close();access=undefined;
    if(changed) {
      store ??= await ConfigStore.open(config);
      for(const [key,value] of Object.entries(trial)) if((store.settings as any)[key]!==value) throw Error("TRIAL_SETTINGS_CHANGED_REQUIRES_RECONCILIATION");
      Object.assign(store.settings,saved); // Retain current OAuth rotations and durable budget.
      store.saveSync();
      console.log(JSON.stringify({settings_restored:true,local_budget:"unlimited"}));
    }
  } finally {
    await store?.close();done();unregister();process.off("SIGINT",stop);process.off("SIGTERM",stop);
  }
}
