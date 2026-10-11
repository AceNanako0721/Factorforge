// Explicit paired experiment; no production default or provider fallback.
import {runFinancialPair} from "./structured-json-trial.ts";
await runFinancialPair({model:"claude-sonnet-4-6",prefix:"sonnet_",protocol:"P3_MODEL_QUALITY_PROTOCOL.md"});
