import {test,expect} from "bun:test";
import {structuredFinancialPayload,financialJSONSchema,goJSON,goTimestamp,prepareFinancialPair} from "./structured-json-trial.ts";
test("paired provider/account binding changes no evidence or local labels and preserves parent",()=>{
 const seal={settings:{Provider:"google-antigravity",AccountID:1,Model:"gemini-3.8-flash"},code_hash:"old",cases_hash:"old",prompt_hash:"sealed"};
 const cases=[{id:"financial_1",input:"SYNTHETIC",gold:"23",scale:"UNSCALED",source:"TEXT",control:false}];
 const before=JSON.stringify({seal,cases});
 const p=prepareFinancialPair(seal,cases,{provider:"openai-codex",accountId:2,model:"gpt-6-luna",prefix:"luna_",protocol:"fixture"},"current",new Date("2026-10-11T00:00:00Z"));
 expect(JSON.stringify({seal,cases})).toBe(before);expect(p.cases).toEqual([{...cases[0],id:"luna_financial_1"}]);
 expect(p.seal.settings).toEqual({Provider:"openai-codex",AccountID:2,Model:"gpt-6-luna"});expect(p.seal.prompt_hash).toBe("sealed");expect(p.seal.code_hash).toBe("current");
 for(const accountId of [0,-1,1.5])expect(()=>prepareFinancialPair(seal,cases,{accountId,model:"m",prefix:"luna_",protocol:"p"},"c",new Date())).toThrow();
});
test("native structured probe preserves original fields and injects only closed output metadata",()=>{
 const body={model:"fixture-model",request:{contents:[{parts:[{text:"SYNTHETIC"}]}],generationConfig:{maxOutputTokens:17,thinkingConfig:{includeThoughts:false}}}};
 const result=JSON.parse(structuredFinancialPayload(JSON.stringify(body)));
 expect(result.model).toBe(body.model);expect(result.request.contents).toEqual(body.request.contents);
 expect(result.request.generationConfig.maxOutputTokens).toBe(17);expect(result.request.generationConfig.thinkingConfig).toEqual(body.request.generationConfig.thinkingConfig);
 expect(result.request.generationConfig.responseMimeType).toBe("application/json");expect(result.request.generationConfig.responseJsonSchema).toEqual(financialJSONSchema);
 expect(financialJSONSchema.additionalProperties).toBe(false);expect(financialJSONSchema.properties.answer.anyOf[1].additionalProperties).toBe(false);expect(financialJSONSchema.properties.answer.anyOf[1].properties.row.type).toBe("integer");
 for(const raw of ['{}','{"request":[]}','{"request":{"generationConfig":[]}}',JSON.stringify(result)])expect(()=>structuredFinancialPayload(raw)).toThrow();
 expect(goJSON({x:"<&>\u2028"})).toBe('{"x":"\\u003c\\u0026\\u003e\\u2028"}');
 expect(goTimestamp(new Date("2026-10-11T00:00:00.240Z"))).toBe("2026-10-11T00:00:00.24Z");
 expect(goTimestamp(new Date("2026-10-11T00:00:00.000Z"))).toBe("2026-10-11T00:00:00Z");
 expect(goTimestamp(new Date("2026-10-11T00:00:00.123Z"))).toBe("2026-10-11T00:00:00.123Z");
});
