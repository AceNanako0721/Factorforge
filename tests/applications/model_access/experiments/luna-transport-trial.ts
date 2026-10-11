// Single synthetic diagnostic via the existing public service, with metadata only.
import "../../../../src/factorforge/applications/model_access/entrypoints/omp-environment.js";
import fs from "node:fs/promises";
import path from "node:path";
import {createHash} from "node:crypto";
import {ConfigStore} from "../../../../src/factorforge/applications/model_access/config/store.js";
import {OMPAccess} from "../../../../src/factorforge/applications/model_access/adapters/omp-access.js";
import {OMPService} from "../../../../src/factorforge/applications/model_access/application/omp-service.js";
import {SSEGuard} from "../../../../src/factorforge/applications/model_access/adapters/omp-stream-guard.js";
import {postmortem} from "../../../../src/factorforge/applications/model_access/node_modules/@oh-my-pi/pi-utils/src/index.ts";

const hash=(s:string|Uint8Array)=>createHash("sha256").update(s).digest("hex");
export function safeTransportError(value:unknown):string {
 if(!value||typeof value!=="object")return "UNKNOWN";
 const v=value as any,e=v.response?.error??v.error??v;
 const code=typeof e?.code==="string"?e.code:"";
 const allow=new Set(["usage_limit_reached","rate_limit_exceeded","invalid_request_error","model_not_found","server_error","insufficient_quota","unsupported_parameter","context_length_exceeded"]);
 if(allow.has(code))return code;
 const message=typeof e?.message==="string"?e.message:typeof e==="string"?e:typeof v.detail==="string"?v.detail:"";
 if(/unsupported parameter/i.test(message))return "UNSUPPORTED_PARAMETER";
 if(/usage limit|quota.*exceed/i.test(message))return "PROVIDER_QUOTA";
 if(/model.*not supported|not.*supported.*model/i.test(message))return "MODEL_UNSUPPORTED";
 if(/account.*deactiv|account.*suspend/i.test(message))return "ACCOUNT_UNAVAILABLE";
 return "UNKNOWN";
}

export async function runLunaTransport() {
 const [rootArg,originalArg,dirArg,promptArg]=process.argv.slice(2);
 if(!rootArg||!originalArg||!dirArg||!promptArg||process.argv.length!==6)throw Error("TRANSPORT_USAGE");
 const checkout=path.resolve(import.meta.dirname,"../../../.."),root=path.resolve(rootArg),original=path.resolve(originalArg),dir=path.resolve(dirArg),prompt=path.resolve(promptArg);
 if(await fs.realpath(root)!==root||path.dirname(original)!==path.join(checkout,"runtime")||path.dirname(dir)!==path.join(checkout,"runtime")||!prompt.startsWith(path.join(root,"prompts")+path.sep))throw Error("TRANSPORT_PATH");
 await fs.mkdir(dir,{mode:0o700});
 const write=async(n:string,v:unknown)=>{const f=await fs.open(path.join(dir,n),"wx",0o600);try{await f.writeFile(JSON.stringify(v,null,2));await f.sync()}finally{await f.close()}};
 const parent=await fs.readFile(path.join(original,"seal.json")),cases=JSON.parse(await fs.readFile(path.join(original,"cases.json"),"utf8"));
 const input=JSON.stringify({question:"What is the synthetic fixture value?",table:[],paragraphs:["The synthetic fixture value is 23."],response_shape:JSON.parse(cases[0].input).response_shape});
 const trial={enabled:true,prompt_file:path.relative(root,prompt),timeout_seconds:120,max_input_bytes:65536,max_output_bytes:32768,max_line_bytes:1048576,omp_max_requests:0,budget_window_seconds:0};
 const saved:Record<string,unknown>={},cancelled=new AbortController();let store:ConfigStore|undefined,access:OMPAccess|undefined,changed=false;
 const stop=()=>cancelled.abort();process.on("SIGINT",stop);process.on("SIGTERM",stop);
 let done!:()=>void;const finished=new Promise<void>(r=>done=r),unregister=postmortem.register("factorforge-luna-transport",async()=>{stop();await finished});
 const metadata:any[]=[],nativeErrors:any[]=[],guardErrors:any[]=[];const readers:Promise<void>[]=[];let generationSends=0;
 const originalFrame=(SSEGuard.prototype as any).frame;
 const originalFeed=SSEGuard.prototype.feed;
 SSEGuard.prototype.feed=function(value){try{return originalFeed.call(this,value)}catch(e:any){guardErrors.push({stage:"feed",code:typeof e?.code==="string"&&/^[A-Z_]+$/.test(e.code)?e.code:"UNKNOWN",chunk_bytes:value?.byteLength??0,frames:[...(typeof e?.stack==="string"?e.stack:"").matchAll(/applications\/model_access\/([a-zA-Z0-9_./-]+:\d+:\d+)/g)].map(m=>m[1]).slice(0,5)});throw e}};
 (SSEGuard.prototype as any).frame=function(){
  const raw=(this.data as string[]).join("\n");
  try{return originalFrame.call(this)}catch(e:any){let parsed:any;try{parsed=JSON.parse(raw)}catch{}
   guardErrors.push({kind:e?.name==="TypeError"?"TypeError":"OTHER",code:typeof e?.code==="string"&&/^[A-Z_]+$/.test(e.code)?e.code:"UNKNOWN",frame_bytes:Buffer.byteLength(raw),json_valid:parsed!==undefined,frame_type:typeof parsed?.type==="string"&&/^[a-z_.]+$/.test(parsed.type)?parsed.type:null,top_keys:parsed&&typeof parsed==="object"?Object.keys(parsed).filter(k=>["type","response","error","delta","choices","candidates","usage","status"].includes(k)):[],output_is_array:Array.isArray(parsed?.response?.output),choices_type:typeof parsed?.choices,google_candidates_type:typeof(parsed?.response??parsed)?.candidates,terminal:this.terminal});throw e;
  }
 };
 // Experiment-only observation. The original formatter still owns the outcome.
 const {mock}=await import("bun:test");
 const modulePath=path.join(checkout,"src/factorforge/applications/model_access/node_modules/@oh-my-pi/pi-ai/src/error/index.ts");
 const originalError=await import(modulePath),finalize=originalError.finalize;
 mock.module(modulePath,()=>({...originalError,finalize:async(e:any,...args:any[])=>{
  const stack=typeof e?.stack==="string"?e.stack:"";
  const frames=[...stack.matchAll(/\/node_modules\/@oh-my-pi\/pi-ai\/src\/([a-zA-Z0-9_./-]+:\d+:\d+)/g)].map(m=>m[1]).slice(0,5);
  nativeErrors.push({kind:["Error","TypeError","AbortError","TimeoutError","SyntaxError"].includes(e?.name)?e.name:"OTHER",category:safeTransportError(e),frames});
  return finalize(e,...args);
 }}));
 const outputErrors:any[]=[],streamEvents:any[]=[];
 const checkOutput=(OMPAccess.prototype as any).checkOutput;
 (OMPAccess.prototype as any).checkOutput=function(message:any){try{return checkOutput.call(this,message)}catch(e:any){outputErrors.push({kind:e?.name,content:message?.content?.map((c:any)=>({type:c.type,text_type:typeof c.text,thinking_type:typeof c.thinking}))});throw e}};
 const aiPath=path.join(checkout,"src/factorforge/applications/model_access/node_modules/@oh-my-pi/pi-ai/src/index.ts"),ai=await import(aiPath),nativeStream=ai.stream;
 mock.module(aiPath,()=>({...ai,stream:(...args:any[])=>{
  const stream=nativeStream(...args);return {async *[Symbol.asyncIterator](){for await(const event of stream){if(["error","done"].includes(event.type))streamEvents.push({type:event.type,reason:event.reason,status:event.error?.errorStatus,category:safeTransportError({message:event.error?.errorMessage}),error_id:event.error?.errorId});yield event}}};
 }}));
 const nativeFetch=globalThis.fetch;
 try {
  store=await ConfigStore.open(path.join(root,"config/config.toml"));for(const k of Object.keys(trial))saved[k]=(store.settings as any)[k];
  const old=store.settings.prompt_file,limit=store.settings.max_input_bytes;let promptHash="";
  try{store.settings.prompt_file=trial.prompt_file;store.settings.max_input_bytes=trial.max_input_bytes;promptHash=hash(await store.prompt())}finally{store.settings.prompt_file=old;store.settings.max_input_bytes=limit}
  if(promptHash!==JSON.parse(parent.toString()).prompt_hash)throw Error("TRANSPORT_PROMPT_CHANGED");
  await write("seal.json",{at:new Date().toISOString(),provider:"openai-codex",account_id:2,model:"gpt-6-luna",parent_hash:hash(parent),input_hash:hash(input),prompt_hash:promptHash,protocol_hash:hash(await fs.readFile(path.join(checkout,"doc/engineering/P3_LUNA_TRANSPORT_PROTOCOL.md"))),code_hash:hash(await fs.readFile(import.meta.filename)),automatic_retry:false,planned_calls:1});
  await write("restore-settings.json",saved);Object.assign(store.settings,trial);changed=true;store.saveSync();
  const fetcher:typeof fetch=async(url,init)=>{
   const generation=init?.method?.toUpperCase()==="POST";
   if(generation&&++generationSends!==1)throw Error("TRANSPORT_SECOND_SEND_DENIED");
   const r=await nativeFetch(url,init);if(!generation)return r;
   const entry:any={status:r.status,content_type:r.headers.get("content-type")?.split(";")[0]??null,bytes:0,events:{},errors:[],finished:false};metadata.push(entry);
   const clone=r.clone();readers.push((async()=>{const reader=clone.body?.getReader();if(!reader)return;const decoder=new TextDecoder();let carry="";try{while(true){const next=await reader.read();if(next.done)break;entry.bytes+=next.value.byteLength;if(entry.bytes>1048576){await reader.cancel();throw Error("TRANSPORT_METADATA_LIMIT")}carry+=decoder.decode(next.value,{stream:true});let index:number;while((index=carry.indexOf("\n"))>=0){const line=carry.slice(0,index).trim();carry=carry.slice(index+1);if(line.startsWith("data:")){try{const e=JSON.parse(line.slice(5));const t=typeof e.type==="string"&&/^[a-z_.]{1,80}$/.test(e.type)?e.type:"UNKNOWN";entry.events[t]=(entry.events[t]??0)+1;if(e.error||e.response?.error||t==="error")entry.errors.push(safeTransportError(e));}catch{}}}}if(carry.trim()){try{entry.errors.push(safeTransportError(JSON.parse(carry)))}catch{}}entry.finished=true;}catch{entry.read_failed=true}finally{reader.releaseLock()}})());
   return r;
  };
  access=new OMPAccess(store,fetcher);await access.ready();const service=new OMPService(store,access);
  await write("attempt.json",{at:new Date().toISOString(),input_hash:hash(input)});
  const start=Date.now(),reply=await service.handle({v:2,id:"luna_transport_synthetic_20261011_v4",op:"generate",provider:"openai-codex",account_id:2,model:"gpt-6-luna",input},cancelled.signal);
  await Promise.allSettled(readers);
  await write("outcome.json",{at:new Date().toISOString(),ok:reply.ok,code:reply.ok?"":reply.error?.code,elapsed_ms:Date.now()-start,generation_sends:generationSends,metadata,native_errors:nativeErrors,guard_errors:guardErrors,output_errors:outputErrors,stream_events:streamEvents});
  if(reply.ok)await write("generation.json",reply.result);
  console.log(JSON.stringify({ok:reply.ok,code:reply.ok?"":reply.error?.code,generation_sends:generationSends,metadata,native_errors:nativeErrors,guard_errors:guardErrors,output_errors:outputErrors,stream_events:streamEvents}));
 }finally{try{access?.close();if(store&&changed){for(const[k,v]of Object.entries(trial))if((store.settings as any)[k]!==v)throw Error("TRANSPORT_SETTINGS_RECONCILE");Object.assign(store.settings,saved);store.saveSync()}}finally{await store?.close();done();unregister();process.off("SIGINT",stop);process.off("SIGTERM",stop)}}
}
if(import.meta.main)await runLunaTransport();
