// Development-only native payload probe. Never imported by a production entrypoint.
import "../../../../src/factorforge/applications/model_access/entrypoints/omp-environment.js";
import fs from "node:fs/promises";
import path from "node:path";
import {createHash} from "node:crypto";
import {spawn} from "node:child_process";
import {ConfigStore} from "../../../../src/factorforge/applications/model_access/config/store.js";
import {OMPAccess} from "../../../../src/factorforge/applications/model_access/adapters/omp-access.js";
import {OMPService} from "../../../../src/factorforge/applications/model_access/application/omp-service.js";
import {ModelError} from "../../../../src/factorforge/applications/model_access/api/protocol.js";
import {postmortem} from "../../../../src/factorforge/applications/model_access/node_modules/@oh-my-pi/pi-utils/src/index.ts";

export const financialJSONSchema = {
 type:"object",additionalProperties:false,required:["answer","scale"],properties:{
  answer:{anyOf:[{type:"null"},{type:"object",additionalProperties:false,required:["origin","row","column","paragraph","quote","occurrence"],properties:{origin:{type:"string",enum:["TABLE","TEXT"]},row:{type:"integer"},column:{type:"integer"},paragraph:{type:"integer"},quote:{type:"string"},occurrence:{type:"integer",minimum:0}}}]},
  scale:{anyOf:[{type:"null"},{type:"string",enum:["UNSCALED","thousand","million","billion","percent"]}]}
 }
};
export function structuredFinancialPayload(raw:string) {
 const body=JSON.parse(raw);
 if(!body||typeof body!=="object"||!body.request||typeof body.request!=="object"||Array.isArray(body.request))throw Error("STRUCTURED_NATIVE_SHAPE");
 const old=body.request.generationConfig??{};
 if(!old||typeof old!=="object"||Array.isArray(old)||["responseMimeType","responseJsonSchema","responseSchema"].some(k=>k in old))throw Error("STRUCTURED_NATIVE_CONFLICT");
 body.request.generationConfig={...old,responseMimeType:"application/json",responseJsonSchema:financialJSONSchema};
 return JSON.stringify(body);
}
// Match Go encoding/json escaping for hashes of these already-sealed records.
export const goJSON=(v:unknown)=>JSON.stringify(v).replace(/[<>&\u2028\u2029]/g,c=>"\\u"+c.charCodeAt(0).toString(16).padStart(4,"0"));
const hash=(v:string|Uint8Array)=>createHash("sha256").update(v).digest("hex");
export const goTimestamp=(date:Date)=>date.toISOString().replace(/(\.[0-9]*?[1-9])0+Z$/,"$1Z").replace(/\.0+Z$/,"Z");

async function main() {
 const [rootArg,originalArg,dirArg,binaryArg,promptArg]=process.argv.slice(2);
 if(!rootArg||!originalArg||!dirArg||!binaryArg||!promptArg||process.argv.length!==7)throw Error("STRUCTURED_TRIAL_USAGE");
 const root=path.resolve(rootArg),original=path.resolve(originalArg),dir=path.resolve(dirArg),binary=path.resolve(binaryArg),prompt=path.resolve(promptArg);
 const checkout=path.resolve(import.meta.dirname,"../../../..");
 if(await fs.realpath(root)!==root||path.dirname(original)!==path.join(checkout,"runtime")||path.dirname(dir)!==path.join(checkout,"runtime")||!prompt.startsWith(path.join(root,"prompts")+path.sep))throw Error("STRUCTURED_TRIAL_PATH");
 await fs.mkdir(dir,{mode:0o700});
 const write=async(name:string,value:unknown)=>{const f=await fs.open(path.join(dir,name),"wx",0o600);try{await f.writeFile(JSON.stringify(value,null,2));await f.sync()}finally{await f.close()}};
 const sealRaw=await fs.readFile(path.join(original,"seal.json")),casesRaw=await fs.readFile(path.join(original,"cases.json"));
 const seal=JSON.parse(sealRaw.toString()),cases=JSON.parse(casesRaw.toString());
 if(seal.cases_hash!==hash(casesRaw)||cases.length!==12||seal.config_root!==root)throw Error("STRUCTURED_ORIGINAL_SEAL");
 await fs.writeFile(path.join(dir,"seal.json"),sealRaw,{flag:"wx",mode:0o600});await fs.writeFile(path.join(dir,"cases.json"),casesRaw,{flag:"wx",mode:0o600});
 const config=path.join(root,"config","config.toml"),trial={enabled:true,prompt_file:path.relative(root,prompt),timeout_seconds:120,max_input_bytes:65536,max_output_bytes:32768,max_line_bytes:1048576,omp_max_requests:0,budget_window_seconds:0};
 let store:ConfigStore|undefined,access:OMPAccess|undefined,changed=false,stage="config",completed=0;
 const saved:Record<string,unknown>={},cancelled=new AbortController();const stop=()=>cancelled.abort();process.on("SIGINT",stop);process.on("SIGTERM",stop);
 let done!:()=>void;const finished=new Promise<void>(resolve=>done=resolve);
 const unregister=postmortem.register("factorforge-structured-json-trial",async()=>{stop();await finished});
 try {
  store=await ConfigStore.open(config);for(const k of Object.keys(trial))saved[k]=(store.settings as any)[k];
  const priorPrompt=store.settings.prompt_file,priorInput=store.settings.max_input_bytes;
  let promptHash="";try{store.settings.prompt_file=trial.prompt_file;store.settings.max_input_bytes=trial.max_input_bytes;promptHash=hash(await store.prompt())}finally{store.settings.prompt_file=priorPrompt;store.settings.max_input_bytes=priorInput}
  if(promptHash!==seal.prompt_hash)throw Error("STRUCTURED_PROMPT_CHANGED");
  await write("probe-seal.json",{at:new Date().toISOString(),protocol_hash:hash(await fs.readFile(path.join(checkout,"doc","engineering","P3_STRUCTURED_JSON_PROTOCOL.md"))),runner_hash:hash(await fs.readFile(import.meta.filename)),original_seal_hash:hash(sealRaw),cases_hash:hash(casesRaw),schema_hash:hash(goJSON(financialJSONSchema)),paired_development:true,planned_calls:12});
  await write("restore-settings.json",saved);Object.assign(store.settings,trial);changed=true;store.saveSync();
  let injected=0;const nativeFetch=globalThis.fetch;
  const fetcher:typeof fetch=async(url,init)=>{
   const address=new URL(typeof url==="string"?url:url instanceof URL?url.href:url.url);
   if(address.pathname.endsWith(":streamGenerateContent")) {
    if(typeof init?.body!=="string"||++injected!==1)throw Error("STRUCTURED_NATIVE_SINGLE_SEND");
    return nativeFetch(url,{...init,body:structuredFinancialPayload(init.body)});
   }
   return nativeFetch(url,init);
  };
  access=new OMPAccess(store,fetcher);await access.ready();const service=new OMPService(store,access);
  stage="catalog";const cat=await service.handle({v:2,id:"structured_catalog",op:"models",provider:"google-antigravity",account_id:1},cancelled.signal);
  if(!cat.ok||!(cat as any).result.models.some((m:any)=>m.id==="gemini-3.8-flash"))throw Error("STRUCTURED_MODEL_UNAVAILABLE");
  await write("started.json",{at:new Date().toISOString(),automatic_retry:false,seal_hash:hash(goJSON(seal)),native_json_schema:true});
  stage="generate";
  for(const c of cases) {
   cancelled.signal.throwIfAborted();injected=0;
   await write(c.id+"-attempt.json",{at:new Date().toISOString(),input_hash:hash(c.input),seal_hash:hash(goJSON(seal))});
   const started=Date.now();const reply=await service.handle({v:2,id:"structured_"+c.id,op:"generate",provider:"google-antigravity",account_id:1,model:"gemini-3.8-flash",input:c.input},cancelled.signal);
   let generation:unknown=null;const code=reply.ok?"":reply.error?.code??"STRUCTURED_FAILED";
   if(reply.ok) {const r=reply.result as any;if(injected!==1||r.prompt_hash!==seal.prompt_hash)throw Error("STRUCTURED_PROVENANCE");generation={provider:r.provider,account_id:r.account_id,model:r.model,prompt_hash:r.prompt_hash,text:r.text,completed_at:goTimestamp(new Date())};await write(c.id+"-generation.json",generation);completed++}
   // The baseline comparator is closed, so native injection is recorded separately.
   await write(c.id+"-native-metadata.json",{native_injected:injected,returned_usage:reply.ok?(reply.result as any).usage:null});
   await write(c.id+"-outcome.json",{elapsed_ms:Date.now()-started,code,at:new Date().toISOString(),generation_hash:generation?hash(goJSON(generation)):""});
   console.log(JSON.stringify({id:c.id,ok:reply.ok,code,native_injected:injected}));if(!reply.ok)break;
  }
 }catch(e){process.exitCode=1;console.error(JSON.stringify({stage,code:e instanceof ModelError?e.code:"STRUCTURED_FAILED"}))}
 finally {
  try{access?.close();if(store&&changed){for(const[k,v]of Object.entries(trial))if((store.settings as any)[k]!==v)throw Error("STRUCTURED_SETTINGS_RECONCILE");Object.assign(store.settings,saved);store.saveSync();console.log(JSON.stringify({settings_restored:true,completed,local_budget:"unlimited"}))}}finally{await store?.close();done();unregister();process.off("SIGINT",stop);process.off("SIGTERM",stop)}
 }
 stage="evaluate";
 const result=await new Promise<number>((resolve,reject)=>{const child=spawn(binary,["-test.run=^TestFinancialExtractionTrial$","-test.v"],{cwd:path.join(checkout,"tests/applications/soxl_jev/experiments"),stdio:"inherit",windowsHide:true,env:{...process.env,FACTORFORGE_FINANCIAL_EXTRACTION_MODE:"evaluate",FACTORFORGE_FINANCIAL_EXTRACTION_ROOT:checkout,FACTORFORGE_FINANCIAL_EXTRACTION_CONFIG_ROOT:root,FACTORFORGE_FINANCIAL_EXTRACTION_DIR:dir}});child.once("error",()=>reject(Error("STRUCTURED_EVALUATE_FAILED")));child.once("exit",code=>resolve(code??1))});if(result||completed!==12)process.exitCode=1;
}
if(import.meta.main)await main();
