import { describe, it, expect } from "../../src/factorforge/operations/console/web/node_modules/vitest/dist/index.js";
import { settingsRows, type SettingsColumn } from "../../src/factorforge/operations/console/web/src/features/settings-model";

// S4-025: the settings page reads only recorded values and marks the cells
// that differ from what most instances share.
const status = (over: Record<string, unknown>) => ({
  settings: {
    purpose: "baseline",
    judgment: { mode: "FOLLOW", models: [], judge_minutes: [], source_instance_id: "soxl-source", since: "2026-01-02T03:04:05Z" },
    close_minutes: 5,
    close_quantile: "0.995",
    wick_quantile: "0.999",
    wick_multiplier: "1.2",
    trail_quantile: "0.99",
    insurance_fraction: "0.25",
    ladder_step: null,
    round_trip_cost: "0.0018",
    max_risk_fraction: "0.02",
    reentry_wait_minutes: 30,
    directive_ttl_minutes: 20,
    baseline_max_share: "0.5",
    baseline_renew_minutes: 30,
    event_types: [],
    price_state_gate: null,
    ...over,
  },
});
const policy = { policy_version: "p1", policy: { fee_rate: "0.0007", slippage_fraction: "0.0009", levels: [{ enter: "0.3", exit: "0.15", exposure: "0.4" }], composition: { sizing: { max_risk_fraction: "0.03" }, reentry: { cooldown_seconds: 2700, qualifying_inputs: ["NEW_CONTRIBUTION", "BASELINE_RECONFIRM"] }, rebalance: { band_floor: "0.015", band_ratio_of_unit: "0.3" } } } };
const col = (name: string, over: Record<string, unknown> = {}, extra: Partial<SettingsColumn> = {}): SettingsColumn => ({
  name,
  status: status(over) as never,
  run: { execution_mode: "SIM" },
  account: { equity: "5100", pnl: { total: { trading: "100" } } },
  policy: policy as never,
  failed: [],
  ...extra,
});
const row = (rows: ReturnType<typeof settingsRows>, label: string) => rows.find((r) => r.label === label)!;

describe("instance settings", () => {
  it("writes each setting in words and marks the minority", () => {
    const rows = settingsRows([
      col("ALPHA", { judgment: { mode: "MODELS", models: ["model-one", "model-two"], judge_minutes: [1, 31] } }),
      col("BRAVO", { ladder_step: "0.03", purpose: "ladder" }),
      col("CHARLIE", { price_state_gate: { model: "jev-latest", scope: "ALL", on_unavailable: "BLOCK", max_age_minutes: 120 } }),
      col("DELTA", { price_state_gate: { model: "jev-latest", scope: "REVERSAL", on_unavailable: "BLOCK", max_age_minutes: 120 } }),
      col("ECHO", { max_risk_fraction: "1" }, { run: { execution_mode: "LIVE" } }),
    ]);
    expect(row(rows, "研判来源").values).toEqual(["自己研判（model-one、model-two）", "跟随 SOURCE", "跟随 SOURCE", "跟随 SOURCE", "跟随 SOURCE"]);
    expect(row(rows, "研判来源").differs).toEqual([true, false, false, false, false]);
    expect(row(rows, "止损移动方式").values).toEqual(["连续移动", "阶梯 3%", "连续移动", "连续移动", "连续移动"]);
    expect(row(rows, "止损移动方式").differs).toEqual([false, true, false, false, false]);
    expect(row(rows, "价格状态闸门").values).toEqual(["不启用", "不启用", "所有开仓；不可用时不开仓", "仅反向开仓；不可用时不开仓", "不启用"]);
    expect(row(rows, "单笔风险上限（应用）").values[4]).toBe("100%");
    expect(row(rows, "下单方式").values[4]).toBe("交易所下单（模拟盘）");
    expect(row(rows, "初始资金").values[0]).toBe("5000");
    expect(row(rows, "止损后重入条件").values[0]).toBe("新研判计入 或 基线重新确认");
    expect(row(rows, "止损后重入冷却").values[0]).toBe("45 分钟");
    expect(row(rows, "跟随起点").values.slice(0, 2)).toEqual(["不跟随", "2026-01-02"]);
    // The purpose is a description, not a compared value.
    expect(row(rows, "测试目的").differs.every((x) => !x)).toBe(true);
  });

  it("shows a dash for what was not read or not yet recorded", () => {
    const rows = settingsRows([col("ALPHA"), col("FOXTROT", {}, { status: {}, policy: null, failed: ["policy"] })]);
    expect(row(rows, "止损移动方式").values[1]).toBe("—");
    expect(row(rows, "止损后重入冷却").values[1]).toBe("—");
    expect(row(rows, "止损移动方式").differs).toEqual([false, false]);
  });
});
