package domain

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// RulesVersion changes whenever a rule's condition, level or exception
// changes; every check record carries it (S6-003).
const RulesVersion = "1"

type Level string

const (
	Anomaly Level = "ANOMALY"
	Warn    Level = "WARN"
	Info    Level = "INFO"
)

// Hit is one rule match. Subject separates matches of one rule on one
// instance (a unit, a protection role); Rule+Instance+Subject is the issue
// source key.
type Hit struct {
	Rule     string         `json:"rule"`
	Level    Level          `json:"level"`
	Instance string         `json:"instance"`
	Subject  string         `json:"subject,omitempty"`
	Summary  string         `json:"summary"`
	Evidence map[string]any `json:"evidence,omitempty"`
}

// Source is the issue source key of a hit.
func (h Hit) Source() string { return h.Rule + "/" + h.Instance + "/" + h.Subject }

// Rule is one registered judgment. Exceptions are part of the condition and
// are written out so a reader needs no memory of past incidents.
type Rule struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Scope      string `json:"scope"` // INSTANCE or HOST
	Level      Level  `json:"level"`
	Severity   string `json:"severity"` // issue severity when it opens one: HIGH, MEDIUM, LOW
	Condition  string `json:"condition"`
	Exceptions string `json:"exceptions"`
	Action     string `json:"action"`

	instance func(*Snapshot) []Hit
	host     func(*HostSnapshot) []Hit
}

// Thresholds of the registered rules.
const (
	MarkStaleAfter     = 5 * time.Minute
	DecisionStaleAfter = 15 * time.Minute
	JudgmentStaleAfter = 70 * time.Minute
	RestartGrace       = 120 * time.Second
	HotTemperatureC    = 90.0
)

// Rules returns the registered rules in ID order.
func Rules() []Rule {
	out := append([]Rule(nil), registry...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RuleByID finds one rule.
func RuleByID(id string) (Rule, bool) {
	for _, r := range registry {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// Evaluate applies every instance rule to s.
func Evaluate(s *Snapshot) []Hit {
	var out []Hit
	for _, r := range Rules() {
		if r.instance == nil {
			continue
		}
		for _, h := range r.instance(s) {
			h.Rule, h.Instance = r.ID, s.Instance
			if h.Level == "" {
				h.Level = r.Level
			}
			out = append(out, h)
		}
	}
	return out
}

// EvaluateHost applies every host rule to s.
func EvaluateHost(s *HostSnapshot) []Hit {
	var out []Hit
	for _, r := range Rules() {
		if r.host == nil {
			continue
		}
		for _, h := range r.host(s) {
			h.Rule, h.Instance = r.ID, "host"
			if h.Level == "" {
				h.Level = r.Level
			}
			out = append(out, h)
		}
	}
	return out
}

func hit(subject, summary string, evidence map[string]any) Hit {
	return Hit{Subject: subject, Summary: summary, Evidence: evidence}
}

func ageMinutes(at, now time.Time) float64 {
	return math.Round(now.Sub(at).Minutes()*10) / 10
}

var registry = []Rule{
	{ID: "R-001", Name: "下层读取失败", Scope: "INSTANCE", Level: Warn, Severity: "MEDIUM",
		Condition:  "本次检查读不到交易层、策略层或应用层的某个公开接口",
		Exceptions: "部署重启后 120 秒内，对应服务刚启动时的读取失败不报",
		Action:     "下一次检查仍失败时看该层服务状态（R-002）与日志",
		instance: func(s *Snapshot) []Hit {
			var out []Hit
			for _, src := range sortedKeys(s.Unavailable) {
				if recentlyStarted(s, layerOf(src)) {
					continue
				}
				out = append(out, hit(src, fmt.Sprintf("读不到 %s：%s", src, s.Unavailable[src]), map[string]any{"source": src, "code": s.Unavailable[src]}))
			}
			return out
		}},
	{ID: "R-002", Name: "服务未运行", Scope: "INSTANCE", Level: Anomaly, Severity: "HIGH",
		Condition:  "登记的常驻服务不是 active/running",
		Exceptions: "由定时器触发的一次性服务（Type=oneshot）不看运行状态；已禁用且未运行的服务不报；刚进入 activating/reloading 不满 120 秒的不报",
		Action:     "看该服务日志；执行器停止时先报告所有者",
		instance: func(s *Snapshot) []Hit {
			var out []Hit
			for _, u := range s.Units {
				if u.ReadFailure != "" {
					out = append(out, hit(u.Name, "读不到服务 "+u.Name+" 的状态", map[string]any{"unit": u.Name, "code": u.ReadFailure}))
					continue
				}
				if u.Type == "oneshot" || u.FileState == "disabled" && u.Active == "inactive" {
					continue
				}
				if u.Active == "active" && (u.Sub == "running" || u.Sub == "") {
					continue
				}
				if (u.Active == "activating" || u.Active == "reloading") && !u.EnteredAt.IsZero() && s.At.Sub(u.EnteredAt) < RestartGrace {
					continue
				}
				out = append(out, hit(u.Name, fmt.Sprintf("服务 %s 状态 %s/%s", u.Name, u.Active, u.Sub), map[string]any{"unit": u.Name, "active": u.Active, "sub": u.Sub}))
			}
			return out
		}},
	{ID: "R-003", Name: "交易运行不是 NORMAL", Scope: "INSTANCE", Level: Anomaly, Severity: "HIGH",
		Condition:  "交易层运行状态不是 NORMAL，或带有原因",
		Exceptions: "无",
		Action:     "先报告所有者，再按批准处理人工隔离",
		instance: func(s *Snapshot) []Hit {
			if s.Run == nil || s.Run.State == "NORMAL" && len(s.Run.Reasons) == 0 {
				return nil
			}
			return []Hit{hit("", fmt.Sprintf("交易运行 %s %s", s.Run.State, strings.Join(s.Run.Reasons, ",")), map[string]any{"state": s.Run.State, "reasons": s.Run.Reasons})}
		}},
	{ID: "R-004", Name: "账户风险锁", Scope: "INSTANCE", Level: Anomaly, Severity: "HIGH",
		Condition:  "交易层账户风险状态不是 NORMAL，或有风险锁",
		Exceptions: "无",
		Action:     "看交易层告警与审计，确认锁的来源",
		instance: func(s *Snapshot) []Hit {
			if s.Account == nil || s.Account.RiskState == "NORMAL" && len(s.Account.Locks) == 0 {
				return nil
			}
			return []Hit{hit("", "账户风险状态 "+s.Account.RiskState, map[string]any{"risk_state": s.Account.RiskState, "locks": s.Account.Locks})}
		}},
	{ID: "R-005", Name: "交易层新告警", Scope: "INSTANCE", Level: Anomaly, Severity: "MEDIUM",
		Condition:  "交易层告警列表里出现上一次检查之后的新告警",
		Exceptions: "上一次检查之前的告警（交易层保留已结束的告警）不报；首次检查只看最近 15 分钟",
		Action:     "按告警代码查交易层审计",
		instance: func(s *Snapshot) []Hit {
			since := s.Since
			if since.IsZero() {
				since = s.At.Add(-15 * time.Minute)
			}
			counts := map[string]int{}
			for _, a := range s.Alerts {
				if a.At.After(since) {
					counts[a.Code]++
				}
			}
			var out []Hit
			for _, code := range sortedKeys(counts) {
				out = append(out, hit(code, fmt.Sprintf("交易层告警 %s ×%d", code, counts[code]), map[string]any{"code": code, "count": counts[code]}))
			}
			return out
		}},
	{ID: "R-006", Name: "交易层运行健康问题", Scope: "INSTANCE", Level: Anomaly, Severity: "MEDIUM",
		Condition:  "交易层运行健康视图列出问题",
		Exceptions: "无",
		Action:     "按问题项查探针与执行器",
		instance: func(s *Snapshot) []Hit {
			if len(s.Health) == 0 {
				return nil
			}
			return []Hit{hit("", "运行健康问题："+strings.Join(s.Health, ","), map[string]any{"issues": s.Health})}
		}},
	{ID: "R-007", Name: "标记价过期", Scope: "INSTANCE", Level: Anomaly, Severity: "MEDIUM",
		Condition:  "交易层标记价缺失，或超过 5 分钟没有更新",
		Exceptions: "交易层读取失败时由 R-001 报，不重复报",
		Action:     "看行情采集服务与交易所连通",
		instance: func(s *Snapshot) []Hit {
			if _, down := s.Unavailable["trading.points"]; down {
				return nil
			}
			if s.Mark == nil || s.At.Sub(s.Mark.AvailableAt) > MarkStaleAfter {
				ev := map[string]any{}
				if s.Mark != nil {
					ev["age_minutes"] = ageMinutes(s.Mark.AvailableAt, s.At)
				}
				return []Hit{hit("", "标记价过期或缺失", ev)}
			}
			return nil
		}},
	{ID: "R-008", Name: "持仓对账或过渡异常", Scope: "INSTANCE", Level: Anomaly, Severity: "HIGH",
		Condition:  "交易层持仓的对账状态不是 NORMAL，或过渡状态不是 STABLE/CLOSED",
		Exceptions: "无",
		Action:     "看交易层对账与外部事实",
		instance: func(s *Snapshot) []Hit {
			for _, p := range s.Positions {
				if p.Reconciliation != "NORMAL" || p.Transition != "" && p.Transition != "STABLE" && p.Transition != "CLOSED" {
					return []Hit{hit("", "持仓状态 "+p.Reconciliation+"/"+p.Transition, map[string]any{"reconciliation": p.Reconciliation, "transition": p.Transition})}
				}
			}
			return nil
		}},
	{ID: "R-009", Name: "保护单与持仓不符", Scope: "INSTANCE", Level: Anomaly, Severity: "HIGH",
		Condition:  "有持仓时策略止损与保险止损各应恰好一张、已核实、覆盖全部数量、方向一致、触发价未越过标记价；空仓时不应有未关闭的保护单",
		Exceptions: "交易层持仓或保护单读取失败时不判",
		Action:     "看交易层保护单与执行器",
		instance: func(s *Snapshot) []Hit {
			if s.Unavailable["trading.positions"] != "" || s.Unavailable["trading.protections"] != "" {
				return nil
			}
			qty := 0.0
			for _, p := range s.Positions {
				qty += p.Quantity
			}
			var open []Protection
			for _, p := range s.Protection {
				if p.State != "CLOSED" {
					open = append(open, p)
				}
			}
			var out []Hit
			if qty == 0 {
				if len(open) > 0 {
					out = append(out, hit("FLAT", fmt.Sprintf("空仓但有 %d 张未关闭保护单", len(open)), map[string]any{"open": len(open)}))
				}
				return out
			}
			side := "LONG"
			if qty < 0 {
				side = "SHORT"
			}
			for _, role := range []string{"STRATEGY", "INSURANCE"} {
				var mine []Protection
				for _, p := range open {
					if p.Role == role {
						mine = append(mine, p)
					}
				}
				if len(mine) != 1 {
					out = append(out, hit(role, fmt.Sprintf("持仓 %g 但 %s 保护单 %d 张", qty, role, len(mine)), map[string]any{"quantity": qty, "count": len(mine)}))
					continue
				}
				p := mine[0]
				var bad []string
				if p.State != "ACTIVE_VERIFIED" {
					bad = append(bad, "状态 "+p.State)
				}
				if math.Abs(p.Covered-math.Abs(qty)) > 1e-9 {
					bad = append(bad, fmt.Sprintf("覆盖 %g≠%g", p.Covered, math.Abs(qty)))
				}
				if p.Direction != side {
					bad = append(bad, "方向 "+p.Direction)
				}
				if s.Mark != nil && p.Trigger > 0 && (qty > 0 && p.Trigger >= s.Mark.Value || qty < 0 && p.Trigger <= s.Mark.Value) {
					bad = append(bad, fmt.Sprintf("触发价 %g 已越过标记价 %g", p.Trigger, s.Mark.Value))
				}
				if len(bad) > 0 {
					out = append(out, hit(role, role+" 保护单："+strings.Join(bad, "；"), map[string]any{"state": p.State, "covered": p.Covered, "direction": p.Direction, "trigger": p.Trigger}))
				}
			}
			return out
		}},
	{ID: "R-010", Name: "策略决策停滞", Scope: "INSTANCE", Level: Anomaly, Severity: "HIGH",
		Condition:  "策略层最新周期决策超过 15 分钟（正常每 5 分钟一轮）",
		Exceptions: "策略层读取失败时由 R-001 报",
		Action:     "看策略层调度与反馈服务、快照冲突日志",
		instance: func(s *Snapshot) []Hit {
			if s.Unavailable["strategy.decisions"] != "" {
				return nil
			}
			if s.Decision == nil || s.At.Sub(s.Decision.At) > DecisionStaleAfter {
				ev := map[string]any{}
				if s.Decision != nil {
					ev["age_minutes"] = ageMinutes(s.Decision.At, s.At)
				}
				return []Hit{hit("", "策略决策停滞", ev)}
			}
			return nil
		}},
	{ID: "R-011", Name: "研判停滞", Scope: "INSTANCE", Level: Warn, Severity: "MEDIUM",
		Condition:  "最新研判超过 70 分钟（正常每 30 分钟一次）",
		Exceptions: "应用层读取失败时由 R-001 报",
		Action:     "看研判服务与模型接入",
		instance: func(s *Snapshot) []Hit {
			if s.Unavailable["instance.judgments"] != "" {
				return nil
			}
			if s.Judgment == nil || s.At.Sub(s.Judgment.At) > JudgmentStaleAfter {
				ev := map[string]any{}
				if s.Judgment != nil {
					ev["age_minutes"] = ageMinutes(s.Judgment.At, s.At)
				}
				return []Hit{hit("", "研判停滞", ev)}
			}
			return nil
		}},
	{ID: "R-012", Name: "最新研判失败", Scope: "INSTANCE", Level: Warn, Severity: "MEDIUM",
		Condition:  "最新一次研判没有得到有效结果",
		Exceptions: "无",
		Action:     "看模型接入的失败代码；连续失败时开单",
		instance: func(s *Snapshot) []Hit {
			if s.Judgment == nil || s.Judgment.OK {
				return nil
			}
			return []Hit{hit(s.Judgment.ID, "最新研判失败 "+s.Judgment.ID, map[string]any{"judgment_id": s.Judgment.ID})}
		}},
	{ID: "R-013", Name: "程序不是最新 main", Scope: "INSTANCE", Level: Warn, Severity: "MEDIUM",
		Condition:  "登记服务运行的程序版本与 main 不一致（所有实例一律用最新 main 构建的程序）",
		Exceptions: "不是 Go 构建的程序、读不到版本的程序不判；读不到 main 时不判",
		Action:     "部署最新 main",
		instance: func(s *Snapshot) []Hit {
			if s.MainRevision == "" {
				return nil
			}
			var stale []string
			for _, u := range s.Units {
				if u.Revision != "" && !strings.HasPrefix(u.Revision, s.MainRevision) && !strings.HasPrefix(s.MainRevision, u.Revision) {
					stale = append(stale, u.Name+"="+short(u.Revision))
				}
			}
			if len(stale) == 0 {
				return nil
			}
			return []Hit{hit("", fmt.Sprintf("%d 个服务不是最新 main（%s）", len(stale), short(s.MainRevision)), map[string]any{"main": short(s.MainRevision), "stale": stale})}
		}},
	{ID: "R-014", Name: "服务日志告警", Scope: "INSTANCE", Level: Warn, Severity: "LOW",
		Condition:  "上一次检查之后，服务日志里出现结构化告警（\"alert\" 字段）",
		Exceptions: "服务在本窗口内重启过（部署重启时停止中打印的内容）不计；只计数，不读正文",
		Action:     "同一告警持续出现时开单",
		instance: func(s *Snapshot) []Hit {
			var out []Hit
			for _, u := range s.Units {
				if len(u.Alerts) == 0 || !s.Since.IsZero() && u.EnteredAt.After(s.Since) {
					continue
				}
				var parts []string
				for _, k := range sortedKeys(u.Alerts) {
					parts = append(parts, fmt.Sprintf("%s×%d", k, u.Alerts[k]))
				}
				out = append(out, hit(u.Name, u.Name+" 日志告警 "+strings.Join(parts, " "), map[string]any{"unit": u.Name, "alerts": u.Alerts}))
			}
			return out
		}},
	{ID: "R-015", Name: "执行器租约过期", Scope: "INSTANCE", Level: Anomaly, Severity: "HIGH",
		Condition:  "交易运行有租约持有者（世代大于 0），租约到期时间已过",
		Exceptions: "没有租约的实例（世代为 0）不判",
		Action:     "看执行器服务；不能自动接管时先报告所有者，再按同意处理人工隔离",
		instance: func(s *Snapshot) []Hit {
			if s.Lease == nil || s.Lease.Epoch == 0 || s.Lease.Until == nil || s.Lease.Until.After(s.At) {
				return nil
			}
			return []Hit{hit("", "执行器租约已过期", map[string]any{"lease_epoch": s.Lease.Epoch, "expired_minutes": ageMinutes(*s.Lease.Until, s.At)})}
		}},
	{ID: "R-101", Name: "主机过热", Scope: "HOST", Level: Warn, Severity: "LOW",
		Condition:  "主机最高温度超过 90°C",
		Exceptions: "读不到温度时不判",
		Action:     "看占用最高的进程",
		host: func(s *HostSnapshot) []Hit {
			if s.TemperatureC <= HotTemperatureC {
				return nil
			}
			return []Hit{hit("", fmt.Sprintf("主机温度 %.0f°C", s.TemperatureC), map[string]any{"temperature_c": s.TemperatureC, "load1": s.Load1})}
		}},
}

func layerOf(source string) string {
	if i := strings.IndexByte(source, '.'); i > 0 {
		return source[:i]
	}
	return source
}

// recentlyStarted reports whether a unit of the given layer entered its
// current state within RestartGrace; unit names carry the layer's service
// role (trading-api, strategy-public, p3-api).
func recentlyStarted(s *Snapshot, layer string) bool {
	marker := map[string]string{"trading": "trading", "strategy": "strategy", "instance": "p3"}[layer]
	for _, u := range s.Units {
		if marker != "" && strings.Contains(u.Name, marker) && !u.EnteredAt.IsZero() && s.At.Sub(u.EnteredAt) < RestartGrace {
			return true
		}
	}
	return false
}

func short(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
