import test from "node:test";
import assert from "node:assert/strict";
import { reserveBudget, recentRequest, UNLIMITED_RECEIPT_LIMIT } from "../../../runtime/model-access-build/application/request-budget.js";
import { parseInferenceValue, validateInferenceLimits } from "../../../runtime/model-access-build/application/inference-limits.js";

test("unlimited accounting retains bounded receipts, counts attempts and saturates safely",()=>{
  let b;
  for(let i=0;i<1000;i++)b=reserveBudget(b,`request-${i}`,0,0,1000+i);
  assert.equal(b.count,1000);assert.equal(b.ids.length,UNLIMITED_RECEIPT_LIMIT);
  assert.equal(recentRequest(b,"request-999",0,3000),true);
  assert.equal(recentRequest(b,"request-0",0,3000),false);
  assert.throws(()=>reserveBudget(b,"request-999",0,0,3000),/DUPLICATE_REQUEST/);
  assert.throws(()=>reserveBudget(b,"new",0,0,100),/CLOCK_ROLLBACK/);
  assert.equal(reserveBudget({...b,count:Number.MAX_SAFE_INTEGER},"new",0,0,3000).count,Number.MAX_SAFE_INTEGER);
  // Changing to an explicit cap cannot replenish existing attempts.
  assert.throws(()=>reserveBudget(b,"new",10,60,3000),/LOCAL_BUDGET_EXHAUSTED/);
  assert.equal(reserveBudget(b,"new",10,1,4000).count,1);
});
test("menu allows unlimited or keeping values, while bounded generation inputs stay positive",()=>{
  const values={timeout_seconds:120,max_input_bytes:65536,max_output_bytes:32768,max_line_bytes:1048576,budget_window_seconds:0,omp_max_requests:0};
  assert.equal(parseInferenceValue("omp_max_requests","0",4),0);
  assert.equal(parseInferenceValue("omp_max_requests"," ",0),0);
  assert.equal(parseInferenceValue("timeout_seconds","",120),120);
  assert.doesNotThrow(()=>validateInferenceLimits(values));
  assert.throws(()=>validateInferenceLimits({...values,omp_max_requests:4}),/BUDGET_WINDOW_REQUIRED/);
  assert.doesNotThrow(()=>validateInferenceLimits({...values,omp_max_requests:4,budget_window_seconds:60}));
  for(const input of ["-1","1.5","Infinity","9007199254740992"])assert.throws(()=>parseInferenceValue("omp_max_requests",input,0),/LIMITS_INVALID/);
  assert.throws(()=>parseInferenceValue("max_input_bytes","0",1),/LIMITS_INVALID/);
});
