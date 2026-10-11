// Paired whole-article event comparison; no production selection.
import {runFinancialPair} from "./structured-json-trial.ts";
await runFinancialPair({provider:"openai-codex",accountId:2,model:"gpt-6-luna",prefix:"luna_event_",protocol:"P3_LUNA_EVENT_PROTOCOL.md",task:"external-event"});
