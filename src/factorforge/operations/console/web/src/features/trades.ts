import type { Json } from "../api/client";
import { record } from "../api/client";

// S4-022: round trips rebuilt from the trading layer's fills. A trade starts
// when the position leaves zero and ends when it returns to zero; a fill that
// crosses zero closes one trade and opens the next with the rest. Realized
// P&L is price P&L on the closed quantity; fees are listed separately, as in
// the account. Unrealized P&L uses the price the page passes in.
export type Trade = {
  direction: "LONG" | "SHORT";
  openedAt: string;
  closedAt: string | null;
  fills: number;
  openQuantity: number;
  openNotional: number;
  averageEntry: number;
  closeQuantity: number;
  closeNotional: number;
  averageExit: number | null;
  remaining: number;
  fees: number;
  realized: number;
  realizedRatio: number | null;
  unrealized: number | null;
  unrealizedRatio: number | null;
};

type Fill = { at: string; signed: number; price: number; fee: number };
const EPS = 1e-9;

function fills(records: Json[]): Fill[] {
  const out: Fill[] = [];
  for (const raw of records) {
    const f = record(raw);
    const q = Number(f.quantity);
    const price = Number(f.price);
    const fee = f.fee === null || f.fee === undefined ? 0 : Number(f.fee);
    if (typeof f.happened_at !== "string" || !(q > 0) || !(price > 0) || !Number.isFinite(fee)) continue;
    if (f.side !== "BUY" && f.side !== "SELL") continue;
    out.push({ at: f.happened_at, signed: f.side === "BUY" ? q : -q, price, fee });
  }
  return out.sort((a, b) => a.at.localeCompare(b.at));
}

export function buildTrades(records: Json[], mark: number | null): Trade[] {
  const trades: Trade[] = [];
  let open: Trade | null = null;
  let position = 0;
  const start = (f: Fill, quantity: number, fee: number): Trade => ({
    direction: quantity > 0 ? "LONG" : "SHORT",
    openedAt: f.at,
    closedAt: null,
    fills: 1,
    openQuantity: Math.abs(quantity),
    openNotional: Math.abs(quantity) * f.price,
    averageEntry: f.price,
    closeQuantity: 0,
    closeNotional: 0,
    averageExit: null,
    remaining: Math.abs(quantity),
    fees: fee,
    realized: 0,
    realizedRatio: null,
    unrealized: null,
    unrealizedRatio: null,
  });
  for (const f of fills(records)) {
    if (!open) {
      open = start(f, f.signed, f.fee);
      position = f.signed;
      continue;
    }
    const sign = position > 0 ? 1 : -1;
    if (Math.sign(f.signed) === sign) {
      open.fills++;
      open.openQuantity += Math.abs(f.signed);
      open.openNotional += Math.abs(f.signed) * f.price;
      open.averageEntry = open.openNotional / open.openQuantity;
      open.remaining += Math.abs(f.signed);
      open.fees += f.fee;
      position += f.signed;
      continue;
    }
    const closing = Math.min(Math.abs(f.signed), Math.abs(position));
    const share = closing / Math.abs(f.signed);
    open.fills++;
    open.closeQuantity += closing;
    open.closeNotional += closing * f.price;
    open.averageExit = open.closeNotional / open.closeQuantity;
    open.realized += closing * (f.price - open.averageEntry) * sign;
    open.remaining -= closing;
    open.fees += f.fee * share;
    position += f.signed;
    if (Math.abs(position) < EPS) position = 0;
    if (position === 0 || Math.sign(position) !== sign) {
      open.remaining = 0;
      open.closedAt = f.at;
      trades.push(open);
      open = null;
      const rest = f.signed + closing * sign;
      if (Math.abs(rest) > EPS) {
        open = start(f, rest, f.fee * (1 - share));
        position = rest;
      }
    }
  }
  if (open) trades.push(open);
  for (const t of trades) {
    const closedEntry = t.closeQuantity * t.averageEntry;
    t.realizedRatio = closedEntry > 0 ? t.realized / closedEntry : null;
    if (t.closedAt === null && mark !== null && t.remaining > EPS) {
      const sign = t.direction === "LONG" ? 1 : -1;
      t.unrealized = t.remaining * (mark - t.averageEntry) * sign;
      t.unrealizedRatio = t.unrealized / (t.remaining * t.averageEntry);
    }
  }
  return trades.reverse();
}
