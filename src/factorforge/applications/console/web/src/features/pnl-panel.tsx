import { record, type Json } from "../api/client";

// S4-020 / P4 design 19. Every figure comes from P1's account view (design
// chapter 24); the browser only subtracts to show the funding difference.
export type PnlLayout = "SYSTEM" | "DEMO" | "LIVE" | "MISSING";
export type PnlRow = {
  label: string;
  since: string | null;
  trading: number | null;
  bookedFunding: number | null;
  referenceFunding: number | null;
  booked: number | null;
  reference: number | null;
};
export type PnlSummary = {
  layout: PnlLayout;
  rows: PnlRow[];
  venueObservedAt: string | null;
  throughAt: string | null;
  settlements: number;
  stale: boolean;
  check: string;
  missing: string[];
};

function num(v: Json | undefined): number | null {
  const n = typeof v === "string" ? Number(v) : typeof v === "number" ? v : NaN;
  return typeof v === "string" || typeof v === "number" ? (Number.isFinite(n) ? n : null) : null;
}
function str(v: Json | undefined): string | null {
  return typeof v === "string" && v !== "" ? v : null;
}
function row(label: string, block: Record<string, Json>): PnlRow {
  return {
    label,
    since: str(block.since),
    trading: num(block.trading),
    bookedFunding: num(block.booked_funding),
    referenceFunding: num(block.reference_funding),
    booked: num(block.booked),
    reference: num(block.reference),
  };
}

export function pnlRows(pnl: Json | undefined, instrumentCode: string): PnlSummary {
  const empty: PnlSummary = {
    layout: "MISSING",
    rows: [],
    venueObservedAt: null,
    throughAt: null,
    settlements: 0,
    stale: false,
    check: "NOT_APPLICABLE",
    missing: [],
  };
  if (pnl === undefined || pnl === null || typeof pnl !== "object" || Array.isArray(pnl)) return empty;
  const view = record(pnl);
  const reference = record(view.reference ?? null);
  const layout: PnlLayout =
    view.basis === "SYSTEM" ? "SYSTEM" : view.environment === "LIVE" ? "LIVE" : "DEMO";
  const rows: PnlRow[] = [];
  const position = (Array.isArray(view.positions) ? view.positions : [])
    .map(record)
    .find((p) => {
      const k = record(p.instrument_key ?? null);
      return `${k.venue}:${k.product}:${k.instrument_id}` === instrumentCode;
    });
  if (position) rows.push(row("当前持仓", position));
  rows.push(row("累计", record(view.total ?? null)));
  return {
    layout,
    rows,
    venueObservedAt: str(view.venue_observed_at),
    throughAt: str(reference.through_at),
    settlements: typeof reference.settlements === "number" ? reference.settlements : 0,
    stale: reference.stale === true,
    check: str(reference.check) ?? "NOT_APPLICABLE",
    missing: Array.isArray(view.missing) ? view.missing.filter((m): m is string => typeof m === "string") : [],
  };
}

export function fundingGap(r: PnlRow): number | null {
  return r.referenceFunding !== null && r.bookedFunding !== null ? r.referenceFunding - r.bookedFunding : null;
}

const missingText: Record<string, string> = {
  VENUE_ACCOUNT_NOT_OBSERVED: "交易所账户数字尚未取得",
  POSITION_HISTORY_INCOMPLETE: "持仓历史不完整，真实资金费未计算",
  REFERENCE_FUNDING_NOT_OBSERVED: "真实资金费尚未取得",
};

function Amount({ value }: { value: number | null }) {
  if (value === null) return <span className="muted">未取得</span>;
  return <span className={value < 0 ? "neg" : value > 0 ? "pos" : undefined}>{value.toFixed(2)}</span>;
}
function Source({ venue }: { venue: boolean }) {
  return <span className={`source-tag ${venue ? "source-venue" : "source-system"}`}>{venue ? "交易所" : "系统计算"}</span>;
}

export function PnlPanel({
  pnl,
  instrumentCode,
  time,
}: {
  pnl: Json | undefined;
  instrumentCode: string;
  time: (v: Json) => string;
}) {
  const s = pnlRows(pnl, instrumentCode);
  const head = (
    <div className="panel-head">
      <div>
        <p className="eyebrow">PNL</p>
        <h2>持仓盈亏</h2>
      </div>
      {s.layout !== "MISSING" && s.layout !== "LIVE" && s.stale && <span className="badge state-STALE">资金费待更新</span>}
    </div>
  );
  if (s.layout === "MISSING")
    return (
      <section className="panel" aria-label="持仓盈亏">
        {head}
        <p className="muted">交易层未提供（版本早于 v2.15.0）</p>
      </section>
    );
  const venue = s.layout !== "SYSTEM";
  return (
    <section className="panel" aria-label="持仓盈亏">
      {head}
      <div className="table-scroll">
        <table className="ladder pnl">
          <thead>
            <tr>
              <th>范围</th>
              <th>
                交易盈亏 <Source venue={venue} />
              </th>
              {s.layout === "SYSTEM" && (
                <>
                  <th>
                    真实资金费 <Source venue={false} />
                  </th>
                  <th>按真实资金费合计</th>
                </>
              )}
              {s.layout === "DEMO" && (
                <>
                  <th>
                    模拟盘资金费 <Source venue />
                  </th>
                  <th>模拟盘实际</th>
                  <th>
                    真实资金费 <Source venue={false} />
                  </th>
                  <th>按真实资金费</th>
                  <th>资金费差额</th>
                </>
              )}
              {s.layout === "LIVE" && (
                <>
                  <th>
                    资金费 <Source venue />
                  </th>
                  <th>实际合计</th>
                </>
              )}
            </tr>
          </thead>
          <tbody>
            {s.rows.map((r) => (
              <tr key={r.label}>
                <td>
                  {r.label}
                  <small>{r.since ? ` 自 ${time(r.since)}` : ""}</small>
                </td>
                <td>
                  <Amount value={r.trading} />
                </td>
                {s.layout === "SYSTEM" && (
                  <>
                    <td>
                      <Amount value={r.referenceFunding} />
                    </td>
                    <td>
                      <strong>
                        <Amount value={r.reference} />
                      </strong>
                    </td>
                  </>
                )}
                {s.layout === "DEMO" && (
                  <>
                    <td>
                      <Amount value={r.bookedFunding} />
                    </td>
                    <td>
                      <strong>
                        <Amount value={r.booked} />
                      </strong>
                    </td>
                    <td>
                      <Amount value={r.referenceFunding} />
                    </td>
                    <td>
                      <strong>
                        <Amount value={r.reference} />
                      </strong>
                    </td>
                    <td>
                      <Amount value={fundingGap(r)} />
                    </td>
                  </>
                )}
                {s.layout === "LIVE" && (
                  <>
                    <td>
                      <Amount value={r.bookedFunding} />
                    </td>
                    <td>
                      <strong>
                        <Amount value={r.booked} />
                      </strong>
                    </td>
                  </>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {s.layout === "LIVE" && s.check === "DIFFERENT" && (
        <p className="badge state-FORBIDDEN">
          资金费与真实盘结算记录不一致：差额 {(() => {
            const total = s.rows.find((r) => r.label === "累计");
            const gap = total ? fundingGap(total) : null;
            return gap === null ? "未取得" : gap.toFixed(8);
          })()}
        </p>
      )}
      <p className="metadata">
        {venue && <span>交易所数字观测于 {s.venueObservedAt ? time(s.venueObservedAt) : "未取得"}</span>}
        <span>
          真实盘资金费结算记录{" "}
          {s.throughAt ? `截至 ${time(s.throughAt)}（${s.settlements} 次）` : "尚未取得"}
        </span>
        <span>交易盈亏含手续费，不含资金费</span>
        {s.missing.map((m) => (
          <span key={m}>{missingText[m] ?? m}</span>
        ))}
      </p>
    </section>
  );
}
