import {test,expect} from "bun:test";
import {safeTransportError} from "./luna-transport-trial.ts";
test("transport summaries never echo arbitrary server messages, codes or identities",()=>{
 expect(safeTransportError({error:{code:"usage_limit_reached",message:"fixture-private"}})).toBe("usage_limit_reached");
 expect(safeTransportError({detail:"Unsupported parameter: fixture-private"})).toBe("UNSUPPORTED_PARAMETER");
 for(const value of [null,"fixture-private",{error:{code:"fixture-private",message:"fixture-private"}},{response:{error:{message:"fixture-private"}}}])expect(safeTransportError(value)).toBe("UNKNOWN");
});
