// User-selected development comparison; never a production default.
import {runFinancialPair} from "./structured-json-trial.ts";
await runFinancialPair({provider:"openai-codex",accountId:2,model:"gpt-6-luna",prefix:"luna_",protocol:"P3_LUNA_FINANCIAL_PROTOCOL.md"});
