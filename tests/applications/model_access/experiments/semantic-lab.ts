// Opt-in, finite development trial. No production worker imports this file.
import "../../../../src/factorforge/applications/model_access/entrypoints/omp-environment.js";
import fs from "node:fs/promises";
import path from "node:path";
import { createHash } from "node:crypto";
import { ConfigStore } from "../../../../src/factorforge/applications/model_access/config/store.js";
import { OMPAccess } from "../../../../src/factorforge/applications/model_access/adapters/omp-access.js";
import { OMPService } from "../../../../src/factorforge/applications/model_access/application/omp-service.js";
import { postmortem } from "../../../../src/factorforge/applications/model_access/node_modules/@oh-my-pi/pi-utils/src/index.ts";
import { mock } from "bun:test";
import * as upstream from "../../../../src/factorforge/applications/model_access/node_modules/@oh-my-pi/pi-ai/src/index.ts";

const digest = (value: string) => createHash("sha256").update(value).digest("hex");
const [command, configFile, promptFile, output] = process.argv.slice(2).filter(x => x !== "--config");
if (!["run","resume"].includes(command!) || !configFile || !promptFile || !output) throw Error("LAB_USAGE_INVALID");
const fixtureBytes = await fs.readFile(new URL("./semantic-cases.json", import.meta.url), "utf8");
const fixture = JSON.parse(fixtureBytes);
const schema = {schema_version:1,events:[{paragraph_id:"string",quote:"exact original substring",occurrence:0,subject_id:"catalog id or UNKNOWN",item_id:"catalog id or UNKNOWN",assertion:"AFFIRMED|NEGATED|HYPOTHETICAL|UNCERTAIN",relation:"UNKNOWN|REVISION|RETRACTION"}]};
const requests = fixture.cases.map((c: any) => ({v:2,id:`semantic_lab_20261011_${c.id}`,op:"generate",provider:fixture.provider,account_id:fixture.account_id,model:fixture.model,input:JSON.stringify({schema_version:1,catalog:fixture.catalog,paragraphs:c.paragraphs,response_shape:schema})}));
await fs.mkdir(output, {recursive:true,mode:0o700});
// Refuse repetition: a lost response must not cause another delivery.
const seal = {fixture_hash:digest(fixtureBytes),requests:requests.map((r: any) => ({id:r.id,hash:digest(JSON.stringify(r))})),prompt_hash:digest(await fs.readFile(promptFile,"utf8")),max_requests:4,timeout_seconds:120,max_input_bytes:65536,max_output_bytes:32768,model:fixture.model,created_at:new Date().toISOString()};
if(command === "run") await fs.writeFile(path.join(output,"seal.json"),JSON.stringify(seal,null,2),{flag:"wx",mode:0o600});
else {
  const old=JSON.parse(await fs.readFile(path.join(output,"seal.json"),"utf8"));
  if(old.fixture_hash!==seal.fixture_hash || old.prompt_hash!==seal.prompt_hash || JSON.stringify(old.requests)!==JSON.stringify(seal.requests)) throw Error("SEAL_MISMATCH");
}
// Observe only a finite classification of upstream failures, never error text.
const originalStream=upstream.stream;
mock.module(new URL("../../../../src/factorforge/applications/model_access/node_modules/@oh-my-pi/pi-ai/src/index.ts",import.meta.url).pathname,()=>({...upstream,stream:(...args:any[])=>{
  const events=(originalStream as any)(...args);
  return {[Symbol.asyncIterator]:async function*(){for await(const e of events){if(e.type==="error")console.log(JSON.stringify({upstream_error_id:e.error.errorId,status:e.error.errorStatus,category:["credential","projectId","finish reason","empty","aborted","fetch","validation","token","compat","thinking","native"].filter(k=>String(e.error.errorMessage??"").toLowerCase().includes(k.toLowerCase()))}));yield e}}};
}}));
const store = await ConfigStore.open(configFile);
const saved = {...store.settings};
const stop = new AbortController();
let done!: () => void;
const finished = new Promise<void>(resolve => done=resolve);
const unregister = postmortem.register("factorforge-semantic-lab",async()=>{stop.abort();await finished});
const cancel = () => stop.abort();
process.on("SIGINT",cancel);process.on("SIGTERM",cancel);
let access: OMPAccess | undefined;
try {
  Object.assign(store.settings,{enabled:true,prompt_file:path.relative(path.dirname(path.dirname(configFile)),promptFile),timeout_seconds:120,max_input_bytes:65536,max_output_bytes:32768,max_line_bytes:1048576,budget_window_seconds:86400,omp_max_requests:4});
  store.saveSync();
  access=new OMPAccess(store,(async(url:any,init:any)=>{
    const phase=String(url).includes("streamGenerateContent")?"generation":String(url).includes("fetchAvailableModels")?"catalog":String(url).includes("manifest")?"version":"other";
    try{const response=await fetch(url,init);console.log(JSON.stringify({transport_phase:phase,status:response.status,media_type:response.headers.get("content-type")}));
      if(phase==="generation"){
        const reader=response.clone().body?.getReader();let raw="",bytes=0;
        if(reader)try{while(true){const c=await reader.read();if(c.done)break;bytes+=c.value.length;if(bytes>1048576)break;raw+=new TextDecoder().decode(c.value)}}finally{await reader.cancel()}
        const frames=raw.split(/\r?\n\r?\n/).filter(x=>x.includes("data:"));
        const shape=(text:string)=>{try{const e=JSON.parse(text);const r=e.response??e;return {keys:Object.keys(e),response_keys:Object.keys(r),candidate_keys:(r.candidates??[]).map((c:any)=>Object.keys(c)),has_finish:(r.candidates??[]).some((c:any)=>!!c.finishReason),error_code:typeof e.error?.code==="number"?e.error.code:undefined}}catch{return{json:false}}};
        console.log(JSON.stringify({stream_bytes:bytes,frames:frames.length,shapes:frames.slice(0,3).map(f=>shape(f.split(/\r?\n/).filter(l=>l.startsWith("data:")).map(l=>l.slice(5)).join("\n"))),non_sse:frames.length?undefined:shape(raw)}));
      }
      return response}
    catch{console.log(JSON.stringify({transport_phase:phase,fetch_failed:true}));throw Error("LAB_FETCH_FAILED")}
  }) as any);await access.ready();
  const service=new OMPService(store,access);
  for (const [i,r] of requests.entries()) {
    if(command==="resume" && await fs.stat(path.join(output,`${i+1}.json`)).then(()=>true,()=>false)) continue;
    if(i===3){
      const amendment={original_request_hash:digest(JSON.stringify(r)),reason:"Three gemini-3-flash responses lack finishReason; check independently discovered gemini-3.8-flash without retrying an earlier case",model:"gemini-3.8-flash",id:"semantic_lab_20261011_untrusted_38"};
      await fs.writeFile(path.join(output,"amendment-4.json"),JSON.stringify(amendment,null,2),{flag:"wx",mode:0o600});
      r.model=amendment.model;r.id=amendment.id;
    }
    if(stop.signal.aborted) break;
    const started=Date.now();
    const reply=await service.handle(r,stop.signal);
    await fs.writeFile(path.join(output,`${i+1}.json`),JSON.stringify({elapsed_ms:Date.now()-started,reply},null,2),{flag:"wx",mode:0o600});
    // Print only delivery metadata; never prompts, source material or responses.
    console.log(JSON.stringify({id:r.id,ok:reply.ok,elapsed_ms:Date.now()-started,...("error" in reply?{error:reply.error}:{})}));
    if(!reply.ok) break;
  }
} finally {
  try {
    access?.close();
    // Preserve OAuth rotations and durable request reservations.
    const state=store.settings.state_json;
    Object.assign(store.settings,saved,{state_json:state});
    store.saveSync();
  } finally {await store.close();done();unregister();process.off("SIGINT",cancel);process.off("SIGTERM",cancel)}
}
