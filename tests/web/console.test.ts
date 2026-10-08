import {
  describe,
  it,
  expect,
} from "../../src/factorforge/applications/console/web/node_modules/vitest/dist/index.js";
import {
  compare,
  scalar,
} from "../../src/factorforge/applications/console/web/src/schemas/display";
import {
  query,
  checkResponse,
  type Selection,
  type Response,
} from "../../src/factorforge/applications/console/web/src/api/client";
import { candleData } from "../../src/factorforge/applications/console/web/src/features/market-chart";
describe("read-only browser semantics", () => {
  it("retains exact monetary strings and sorts without rounding", () => {
    expect(
      compare(
        "1000000000000000.000000000000000002",
        "1000000000000000.000000000000000001",
      ),
    ).toBe(1);
    expect(compare("-0.000000000000000002", "-0.000000000000000001")).toBe(-1);
    expect(scalar("0.000000000000000001")).toBe("0.000000000000000001");
    expect(scalar(null)).not.toBe("0");
  });
  it("builds only a scoped query and rejects another environment", () => {
    const s = {
      selection_id: "a",
      binding_version: "v1",
      environment: "SIM",
      instance_id: "i",
      trading_run_key: { environment: "SIM", account_id: "a", run_id: "r" },
    } as Selection;
    expect(query("/events", s, { cursor: "x/y" })).toBe(
      "/events?selection_id=a&cursor=x%2Fy",
    );
    expect(() =>
      checkResponse(
        {
          schema_version: "console-2.0",
          selection: { ...s, environment: "LIVE" },
        } as Response,
        s,
      ),
    ).toThrow("BINDING_MISMATCH");
  });
  it("charts only finite, final, actual timestamps without manufactured gaps", () => {
    const base = {
      open_at: "2026-01-05T00:00:00Z",
      final: true,
      open: "100",
      high: "105",
      low: "99",
      close: "101",
    };
    const bars = candleData([
      base,
      { ...base, open_at: "2026-01-05T00:03:00Z" },
      { ...base, open_at: "2026-01-05T00:01:00Z", final: false },
      { ...base, close: "Infinity" },
      base,
    ]);
    expect(bars).toHaveLength(2);
    expect(Number(bars[1].time) - Number(bars[0].time)).toBe(180);
  });
});
