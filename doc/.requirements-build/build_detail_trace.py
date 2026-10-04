"""Refresh the source-ID trace matrix in 详细设计书_v1.1.md.

The table is a design mapping, not evidence that implementation or tests pass.
"""

from pathlib import Path
import re
import sys

root = Path(__file__).resolve().parents[1]
srs_path = root / "要求分析式样书_v1.1.md"
plan_path = root / "SOXLUSDT实盘系统完整开发规划书_v1.1.md"
detail_path = root / (sys.argv[1] if len(sys.argv) > 1 else "详细设计书_v1.1.md")
srs = srs_path.read_text(encoding="utf-8").splitlines()
plan = plan_path.read_text(encoding="utf-8").splitlines()

family = {
    "GOV": ("001,009", "application/governance", "phase-gates, config-proposals", "01,03,17", "01,02,32", "18"),
    "OBJ": ("003,011", "domain/decision; research", "decisions, reports", "04,08,13", "01,28,31", "17"),
    "DAT": ("003,008,018", "workers/ingestion; ports/EvidenceExtractor; application/analysis/routing", "market/*, evidence/*", "02,05", "02,29", "04,05"),
    "STA": ("003,015", "domain/state; workers/scheduler", "runs, decisions, start/stop", "07,09,12", "03,19,35", "14,19"),
    "EVT": ("004,014,017,018", "domain/event; application/analysis/routing; ports/EvidenceExtractor", "analysis/jobs, events/*", "05,06", "04,05,06,07", "17"),
    "SEN": ("003,008,014", "domain/sentiment; adapters/postgres", "sentiment, decisions", "07,08", "08,09,10", "05"),
    "REG": ("003,011", "domain/regime", "market/snapshots, decisions", "07,08", "11", "04,17"),
    "FAC": ("003,011", "domain/factors; research", "decisions, reports", "04,07,08", "11", "05,17"),
    "POS": ("003,014", "domain/position; application/decision", "decisions, trade-requests", "07,09", "12,13", "02,03"),
    "EXE": ("005,006,013", "application/intents; execution/*", "trade-requests, intents, orders", "09,10", "14", "08,14"),
    "STP": ("007,015", "domain/stop; execution/protection", "protections, reduce-requests", "09,10", "15,16", "09,10,11"),
    "RSK": ("002,005,008,015", "domain/risk; application/risk", "risk, risk-events, trade-requests", "09,10", "17,18,19", "08,12,15,21"),
    "CAS": ("008,011", "application/cases", "cases, positions", "11", "20", "15"),
    "OBS": ("011", "application/observation", "cases, labels", "11", "20", "17"),
    "ATR": ("004,011", "application/attribution", "attributions, labels", "11", "21", "17"),
    "CFT": ("011", "research/counterfactual", "replay-runs, reports", "08,11", "22", "17"),
    "LRN": ("010,011", "application/learning; domain/parameters", "learning-candidates, parameter-versions", "11,16", "23,24,25,26", "22"),
    "PAR": ("008,010", "domain/parameters; application/config", "parameters, config-proposals", "01,07,11", "27", "21,22"),
    "MET": ("011,012", "research/metrics; monitoring", "reports", "08,12", "28", "17"),
    "VAL": ("003,011", "research/replay; research/validation", "replay-runs, reports", "04,08,14", "29,30,31", "16,17"),
    "REL": ("012,015", "application/phase_gate; deployment", "phase-gates, start", "13,15,17", "32", "14,18"),
    "TST": ("011,012", "tests/acceptance", "jobs, reports", "13,17", "32", "01-24"),
    "TRD": ("006,007,008", "adapters/binance; domain/instrument", "market/instruments, orders, protections", "02,09,10", "33,34", "01-13"),
    "TIM": ("003,015", "workers/scheduler; domain/time_window", "time-windows, decisions", "07,09", "35,36", "04,19,20"),
    "CAP": ("002,015", "domain/risk; application/phase_gate", "risk, phase-gates", "09,14,15", "37,38,39", "21"),
    "AUT": ("010", "application/learning; workers/scheduler", "learning-candidates, reports", "11,16", "40", "22"),
    "OPS": ("012", "deployment/breakglass; application/recovery", "health/*, recover, reconciliations", "03,12,13", "41", "14,23"),
    "SEC": ("001,004,009,016,017", "api/auth; application/workload_identity; deployment/public-export", "api-keys, permissions, audit", "03,12,18", "42", "18,24"),
}

nfr = {
    "001": ("003,007,012", "monitoring; workers/scheduler; workers/trading_analysis", "health/*, jobs", "03,12,13", "27", "10,17,23"),
    "002": ("003,008,013", "domain; adapters/postgres", "decisions, replay-runs", "07,08,13", "27", "08,14"),
    "003": ("008,009,016,019", "application/audit; api/auth", "audit, evidence", "03,12,18", "27,42", "18,24"),
    "004": ("004,009,017,018", "application/analysis/routing; ports/EvidenceExtractor; api/auth", "analysis/jobs, analysis-candidates", "06,13", "27", "17,24"),
    "005": ("008,012", "application/recovery; deployment/breakglass", "recover, reconciliations", "03,10,13", "27,41", "14,23"),
}

cr = {
    "01": ("006", "domain/instrument; adapters/binance", "market/instruments", "02,09", "33", "01,02,03"),
    "02": ("003,011,014", "domain/sentiment; research", "sentiment, reports", "05,07,08", "09,29", "04,05,17"),
    "03": ("002,008,015", "domain/risk; application/account", "risk, portfolio", "09", "17,18,33", "05,06,12"),
    "04": ("005,006,007,013", "application/intents; execution", "intents, orders, protections", "10", "14,34", "08,09,10,13,14"),
    "05": ("006,008", "adapters/binance; application/account", "reconciliations", "09,10", "33,34", "06,07,15"),
    "06": ("003,011,017,018", "research/replay; application/learning; application/analysis", "replay-runs, reports", "04,08,11,14", "20,29,30,31", "16,17"),
    "07": ("010,012,015", "application/phase_gate; deployment", "phase-gates, start", "01,13,15,17", "32,35-42", "18-24"),
}

dec = {
    "01": ("002,006", "adapters/binance; phase_gate", "start", "02", "32,33", "01,16"),
    "02": ("003", "workers/scheduler", "time-windows", "07", "35", "19"),
    "03": ("002,015", "domain/risk; application/account", "risk, start", "09,15", "18,37", "15,21"),
    "04": ("003,006,011", "workers/ingestion; adapters/binance", "market/*, evidence/*", "02,05,10", "29,33,34", "04-07"),
    "05": ("004,010", "domain/event; application/learning", "events, learning-candidates", "04,06,11", "04,23", "17,22"),
    "06": ("015", "domain/risk; application/phase_gate", "risk, start, recover", "09,15", "18,32", "21"),
    "07": ("015", "domain/risk; domain/time_window", "risk, time-windows", "09,14", "36,39", "20,21"),
    "08": ("011", "research/validation", "reports", "04,08", "30,31", "17"),
    "09": ("010", "application/learning", "learning-candidates", "11,16", "26,40", "22"),
    "10": ("012,016", "deployment; application/recovery", "health/*, recover", "03,13,18", "41,42", "23,24"),
}

wp = {
    "01": "001,015", "02": "004,006", "03": "001,002,009,012,013,019,020", "04": "011",
    "05": "003,008,018,021", "06": "004,017,018,021", "07": "003,008,014", "08": "011",
    "09": "002,008,015", "10": "005,006,007", "11": "010,011", "12": "001,009,012,022",
    "13": "011,012,017,018,019,020,021,022", "14": "002,011,015", "15": "006,012,015,022", "16": "010,011",
    "17": "011,012", "18": "009,016,020",
}
wp_targets = {
    "01": ("application/governance; domain/parameters", "phase-gates, config-proposals", "AC-01, AC-27, TC-18"),
    "02": ("adapters/binance; workers/ingestion", "market/instruments, market/snapshots", "AC-02, AC-33, TC-01"),
    "03": ("api; adapters/postgres; application/workload_identity; deployment", "health/*, api-keys, jobs", "AC-41, TC-23, DD-CT-09/10"),
    "04": ("research/validation; application/labels", "reports, labels", "AC-29, AC-30, TC-17"),
    "05": ("workers/ingestion; ports/EvidenceExtractor; application/analysis/routing", "market/*, evidence/*", "AC-29, TC-04, DD-CT-06/11"),
    "06": ("domain/event; application/analysis/routing; ports/EvidenceExtractor", "analysis/jobs, analysis-candidates, events/*", "AC-04, TC-17, DD-CT-01/05/11/12"),
    "07": ("domain/sentiment; domain/position", "sentiment, decisions", "AC-08, AC-12, TC-05"),
    "08": ("research/replay; research/validation", "replay-runs, reports", "AC-30, AC-31, TC-16"),
    "09": ("domain/risk; application/account", "risk, portfolio, risk-events", "AC-17, AC-18, TC-21"),
    "10": ("application/intents; execution/protection; adapters/binance", "intents, orders, protections, reconciliations", "AC-14, AC-15, TC-14"),
    "11": ("application/cases; application/attribution; application/learning", "cases, labels, attributions, learning-candidates", "AC-20, AC-26, TC-22"),
    "12": ("api/auth; api/ops; deployment/breakglass; monitoring", "start, stop, emergency-stop, audit", "AC-41, AC-42, TC-24, DD-CT-13"),
    "13": ("tests/acceptance; tests/contract; contracts", "全部受测 API", "AC-01–AC-42, TC-01–TC-24, DD-CT-01–13"),
    "14": ("execution/sim; research/forward", "simulations, reports", "AC-30, AC-31, TC-16"),
    "15": ("execution/live; application/phase_gate; application/recovery; deployment/breakglass", "start, recover, reconciliations", "AC-32, AC-34, TC-21, DD-CT-13"),
    "16": ("application/learning; application/governance", "learning-candidates, config-proposals", "AC-26, AC-40, TC-22"),
    "17": ("application/phase_gate; tests/acceptance; deployment", "phase-gates, reports, audit", "AC-32, AC-41, TC-23"),
    "18": ("api/auth; application/workload_identity; application/audit; deployment/public-export", "api-keys, permissions, audit", "AC-42, TC-24, DD-CT-10"),
}

# Exact AC links from SRS 18.1/21.6. Family defaults cover additional tests,
# but these overrides prevent an individual requirement from inheriting a
# sibling's acceptance case (for example CAP-001 versus CAP-003).
ac_override = {
    "GOV": {"001": "01", "002": "02,32"},
    "OBJ": {"001": "01,28,31", "002": "03,20"},
    "DAT": {"001": "29", "002": "02,29"},
    "STA": {"001": "03", "002": "03,14", "003": "03,35", "004": "03,19"},
    "EVT": {"001": "04", "002": "04", "003": "05", "004": "05", "005": "06", "006": "06", "007": "07"},
    "SEN": {"001": "08", "002": "08", "003": "09", "004": "09", "005": "10"},
    "POS": {"001": "12", "002": "13", "003": "13", "004": "13", "005": "13"},
    "STP": {"001": "15", "002": "15", "003": "16", "004": "16"},
    "RSK": {"001": "17", "002": "17", "003": "18", "004": "18", "005": "19", "006": "19"},
    "LRN": {"001": "23", "002": "23", "003": "23", "004": "24", "005": "24", "006": "25", "007": "25", "008": "26", "009": "26", "010": "26"},
    "TRD": {"001": "33", "002": "33", "003": "33", "004": "34"},
    "TIM": {"001": "35", "002": "36", "003": "36"},
    "CAP": {"001": "37", "002": "38", "003": "39"},
    "VAL": {"001": "29", "002": "29", "003": "29", "004": "30", "005": "30", "006": "30", "007": "31", "008": "31"},
}
tc_override = {
    "TRD": {"001": "01,02,03", "002": "04,05", "003": "06,07,12", "004": "08,09,10,11,13,14,15,16"},
    "TIM": {"001": "04,19", "002": "20", "003": "20"},
    "CAP": {"001": "21", "002": "21", "003": "21"},
    "AUT": {"001": "22"},
    "OPS": {"001": "14,23"},
    "SEC": {"001": "24", "002": "24", "003": "24"},
    "LRN": {"008": "22", "009": "22", "010": "22"},
}

adr_override = {
    "REQ-GOV-001": "001,019",
    "REQ-DAT-001": "003,008,018,021",
    "REQ-DAT-002": "003,008",
    "REQ-EVT-001": "004,019",
    "REQ-EVT-002": "004,017,018,021",
    "REQ-EVT-003": "004,018,021",
    "REQ-EVT-004": "004,014,018,021",
    "REQ-EVT-005": "004,014",
    "REQ-EVT-006": "004,014",
    "REQ-EVT-007": "004,014,019",
    "REQ-OPS-001": "012,022",
    "REQ-RSK-001": "002,005,008,015,022",
    "REQ-SEC-001": "001,004,009,016,017,020",
    "REQ-SEC-002": "001,004,009,016,017,020",
    "REQ-SEC-003": "001,004,009,016,017,020",
    "REQ-TST-001": "011,012,019,020,021,022",
    "NFR-001": "003,007,012,021",
    "NFR-002": "003,008,013,019",
    "NFR-004": "004,009,017,018,020,021",
    "NFR-005": "008,012,022",
}
design_tests = {
    "REQ-GOV-001": "DD-CT-09",
    "REQ-DAT-001": "DD-CT-06, DD-CT-11",
    "REQ-EVT-002": "DD-CT-01, DD-CT-05, DD-CT-06, DD-CT-08, DD-CT-11",
    "REQ-EVT-003": "DD-CT-07, DD-CT-11",
    "REQ-EVT-004": "DD-CT-07, DD-CT-11",
    "REQ-OPS-001": "DD-CT-13",
    "REQ-SEC-001": "DD-CT-10",
    "REQ-SEC-002": "DD-CT-10",
    "REQ-SEC-003": "DD-CT-10",
    "REQ-TST-001": "DD-CT-09, DD-CT-10, DD-CT-11, DD-CT-12, DD-CT-13",
    "NFR-001": "DD-CT-12",
    "NFR-004": "DD-CT-01, DD-CT-02, DD-CT-03, DD-CT-04, DD-CT-10, DD-CT-11, DD-CT-12",
    "NFR-005": "DD-CT-13",
}

def ids(lines, pattern):
    return sorted(set(re.findall(pattern, "\n".join(lines))), key=lambda x: (x.split("-")[0], x))

req_ids = ids(srs, r"\bREQ-[A-Z]+-\d{3}\b")
nfr_ids = ids(srs, r"\bNFR-\d{3}\b")
cr_ids = ids(plan, r"\bCR-\d{2}\b")
dec_ids = ids(srs, r"\bDEC-\d{2}\b")
wp_ids = ids(plan, r"\bWP-\d{2}\b")
ac_ids = ids(srs, r"\bAC-\d{2}\b")
tc_ids = ids(plan, r"(?<![A-Za-z0-9])TC-\d{2}(?![0-9])")
expected = (94, 5, 7, 10, 18, 42, 24)
actual = tuple(map(len, (req_ids, nfr_ids, cr_ids, dec_ids, wp_ids, ac_ids, tc_ids)))
if actual != expected:
    raise SystemExit(f"Source ID counts changed; review mappings: {actual} != {expected}")

def source_ref(lines, id_, source):
    matchers = {
        "REQ": lambda x: x.startswith(id_ + "〔"),
        "NFR": lambda x: x.startswith(id_ + "〔"),
        "CR": lambda x: x.lstrip().startswith("| " + id_ + " |"),
        "DEC": lambda x: x.lstrip().startswith("| " + id_ + " |"),
        "WP": lambda x: x.lstrip().startswith("| " + id_ + " ") or x.startswith(id_ + " "),
        "AC": lambda x: x.lstrip().startswith("| " + id_ + " "),
        "TC": lambda x: x.lstrip().startswith("| " + id_ + " |"),
    }
    prefix = id_.split("-")[0]
    pred = matchers[prefix]
    for no, line in enumerate(lines, 1):
        if pred(line):
            return f"{source}:{no}"
    for no, line in enumerate(lines, 1):
        if id_ in line:
            return f"{source}:{no}"
    raise ValueError(id_)

def expand(prefix, expression):
    out = []
    for part in expression.split(","):
        if "-" in part:
            lo, hi = part.split("-")
            out += [f"{prefix}-{n:02d}" for n in range(int(lo), int(hi) + 1)]
        else:
            out.append(f"{prefix}-{int(part):02d}")
    return out

reverse_ac = {id_: [] for id_ in ac_ids}
reverse_tc = {id_: [] for id_ in tc_ids}

def fmt(id_, source, spec, back, ac_expr=None, tc_expr=None):
    adrs, module, api, packages, ac, tc = spec
    adrs = adr_override.get(id_, adrs)
    ac_expr = ac_expr or ac
    tc_expr = tc_expr or tc
    adr_text = ", ".join("ADR-" + n for n in adrs.split(","))
    wp_text = ", ".join("WP-" + n for n in packages.split(","))
    ac_links = expand("AC", ac_expr)
    tc_links = expand("TC", tc_expr)
    for link in ac_links:
        reverse_ac[link].append(id_)
    for link in tc_links:
        reverse_tc[link].append(id_)
    tests = ", ".join(ac_links + tc_links)
    if id_ in design_tests:
        tests += ", " + design_tests[id_]
    return f"| {id_} | {source} | {adr_text} | `{module}` / `{api}` | {back}；{wp_text} | {tests} |"

rows = ["| 上游 ID | 原文位置 | ADR | 模块 / API 或端口 | 拟实现及 WP | 验收 |",
        "| --- | --- | --- | --- | --- | --- |"]
for id_ in req_ids:
    fam = id_.split("-")[1]
    serial = id_.split("-")[2]
    rows.append(fmt(id_, source_ref(srs, id_, "SRS"), family[fam], "待编码符号/提交",
                    ac_override.get(fam, {}).get(serial), tc_override.get(fam, {}).get(serial)))
for id_ in nfr_ids:
    rows.append(fmt(id_, source_ref(srs, id_, "SRS"), nfr[id_.split("-")[1]], "待编码符号/提交"))
for id_ in cr_ids:
    rows.append(fmt(id_, source_ref(plan, id_, "PLAN"), cr[id_.split("-")[1]], "待编码符号/提交"))
for id_ in dec_ids:
    rows.append(fmt(id_, source_ref(srs, id_, "SRS"), dec[id_.split("-")[1]], "配置门保持未决；关闭后待编码"))
for id_ in wp_ids:
    n = id_.split("-")[1]
    adrs = ", ".join("ADR-" + x for x in wp[n].split(","))
    module, api, checks = wp_targets[n]
    rows.append(f"| {id_} | {source_ref(plan, id_, 'PLAN')} | {adrs} | `{module}` / `{api}` | 工作包交付/提交待实施 | {checks}；完整 AC/TC 依上述需求行 |")
for id_ in ac_ids:
    refs = ", ".join(reverse_ac[id_]) or "按 SRS 18.1 的关联要求"
    rows.append(f"| {id_} | {source_ref(srs, id_, 'SRS')} | 由所测 REQ/NFR/CR 的 ADR 继承 | `tests/acceptance/{id_.lower()}` | 覆盖 {refs}；输入/预期/实际/恢复/证据待实施；WP-13/17 | {id_} |")
for id_ in tc_ids:
    refs = ", ".join(reverse_tc[id_]) or "按 PLAN 13.2/19.2 的关联要求"
    rows.append(f"| {id_} | {source_ref(plan, id_, 'PLAN')} | 由所测 CR/REQ 的 ADR 继承 | `tests/contract/{id_.lower()}` | 覆盖 {refs}；合约/故障/恢复证据待实施；WP-13/17 | {id_} |")

original = detail_path.read_text(encoding="utf-8")
start = "<!-- TRACE_MATRIX_START -->"
end = "<!-- TRACE_MATRIX_END -->"
if original.count(start) != 1 or original.count(end) != 1:
    raise SystemExit("Trace markers missing or duplicated")
head, tail = original.split(start)
_, foot = tail.split(end)
detail_path.write_text(head + start + "\n" + "\n".join(rows) + "\n" + end + foot, encoding="utf-8")
print(f"Wrote {len(rows)-2} trace rows: REQ/NFR/CR/DEC/WP/AC/TC={actual}")
