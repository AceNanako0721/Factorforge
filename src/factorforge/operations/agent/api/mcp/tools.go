package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	app "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/ports"
	"sort"
	"time"
)

var both = []string{d.Manager, d.Developer}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func integer(desc string, min, max int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "minimum": min, "maximum": max}
}
func strs(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// strict decodes tool arguments, refusing unknown fields.
func strict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return d.Fail("ARGUMENTS_INVALID")
	}
	return nil
}

func when(s string, def time.Time) (time.Time, error) {
	if s == "" {
		return def, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, d.Fail("TIME_INVALID")
	}
	return t.UTC(), nil
}

func bound(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// lastItems keeps the newest n list items of a lower read. byKey names the
// time field used to order them.
func lastItems(raw json.RawMessage, n int, byKey string) (any, error) {
	var root any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&root) != nil {
		return nil, d.Fail("LOWER_RESPONSE_INVALID")
	}
	var list []any
	var env map[string]any
	switch x := root.(type) {
	case []any:
		list = x
	case map[string]any:
		env = x
		if l, ok := x["items"].([]any); ok {
			list = l
		} else {
			return x, nil
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, _ := list[i].(map[string]any)
		b, _ := list[j].(map[string]any)
		as, _ := a[byKey].(string)
		bs, _ := b[byKey].(string)
		return as > bs
	})
	truncated := len(list) > n
	if truncated {
		list = list[:n]
	}
	out := map[string]any{"items": list, "truncated": truncated}
	if env != nil {
		for _, k := range []string{"observed_at", "source_version", "cursor"} {
			if v, ok := env[k]; ok {
				out[k] = v
			}
		}
	}
	return out, nil
}

// Tools registers every tool; NewServer keeps the profile's share.
func Tools(o app.Observer, store ports.Store, profile string, now func() time.Time) []Tool {
	rec := app.Records{Store: store, Profile: profile, Now: now}
	instanceArg := str("实例标识，见 list_instances")
	raw := func(route, order string, def, max int, timeQuery bool) func(context.Context, json.RawMessage) (any, error) {
		return func(ctx context.Context, a json.RawMessage) (any, error) {
			var p struct {
				Instance string `json:"instance"`
				From     string `json:"from"`
				To       string `json:"to"`
				Limit    int    `json:"limit"`
			}
			if err := strict(a, &p); err != nil {
				return nil, err
			}
			q := map[string]string{}
			if timeQuery {
				for k, v := range map[string]string{"from": p.From, "to": p.To} {
					if v != "" {
						if _, err := when(v, time.Time{}); err != nil {
							return nil, err
						}
						q[k] = v
					}
				}
				if _, ok := q["limit"]; !ok && route != "strategy.history" {
					q["limit"] = "500"
				}
			}
			if route == "strategy.history" && (q["from"] == "" || q["to"] == "") {
				to := now().UTC()
				if q["to"] == "" {
					q["to"] = to.Format(time.RFC3339)
				}
				if q["from"] == "" {
					t, _ := when(q["to"], to)
					q["from"] = t.Add(-6 * time.Hour).Format(time.RFC3339)
				}
			}
			r, err := o.Raw(ctx, p.Instance, route, q)
			if err != nil {
				return nil, err
			}
			return lastItems(r, bound(p.Limit, def, max), order)
		}
	}
	rangeProps := map[string]any{"instance": instanceArg, "from": str("起点（RFC3339，可省）"), "to": str("终点（RFC3339，可省）"), "limit": integer("最多返回几条（最新的）", 1, 500)}
	return []Tool{
		{Name: "list_instances", Tier: Observe, Profiles: both, Schema: obj(map[string]any{}),
			Description: "列出全部实例：测试目的、登记设置，以及与多数实例不同的设置。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				if err := strict(a, &struct{}{}); err != nil {
					return nil, err
				}
				return map[string]any{"instances": o.List(ctx)}, nil
			}},
		{Name: "instance_status", Tier: Observe, Profiles: both, Schema: obj(map[string]any{"instance": instanceArg}, "instance"),
			Description: "一个实例此刻的状态：交易运行、账户、租约、标记价、持仓与保护单、最新决策与研判、服务与版本、应用层批次与研判链，以及按登记规则判出的命中（不写检查记录）。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					Instance string `json:"instance"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				return o.Status(ctx, p.Instance)
			}},
		{Name: "list_batches", Tier: Observe, Profiles: both, Schema: obj(map[string]any{"instance": instanceArg, "limit": integer("最多返回几笔（最新的）", 1, 500)}, "instance"),
			Description: "策略层记录的批次（开仓到平仓）：方向、预算、净盈亏、R、退出方式。",
			Call:        raw("strategy.batches", "opened_at", 50, 500, false)},
		{Name: "decision_history", Tier: Observe, Profiles: both, Schema: obj(rangeProps, "instance"),
			Description: "策略层周期决策历史（默认最近 6 小时）。",
			Call:        raw("strategy.history", "at", 100, 500, true)},
		{Name: "list_judgments", Tier: Observe, Profiles: both, Schema: obj(rangeProps, "instance"),
			Description: "应用层研判记录：方向、信心、时长、论点、反方、失效条件、关键条目。",
			Call:        raw("instance.judgments", "at", 20, 200, true)},
		{Name: "list_actions", Tier: Observe, Profiles: both, Schema: obj(rangeProps, "instance"),
			Description: "应用层动作记录：注入、止损、重入等。",
			Call:        raw("instance.actions", "at", 50, 500, true)},
		{Name: "list_fills", Tier: Observe, Profiles: both, Schema: obj(map[string]any{"instance": instanceArg, "limit": integer("最多返回几笔（最新的）", 1, 500)}, "instance"),
			Description: "交易层成交记录。",
			Call:        raw("trading.fills", "at", 50, 500, false)},
		{Name: "judgment_scorecard", Tier: Observe, Profiles: both,
			Schema: obj(map[string]any{"instance": instanceArg, "from": str("起点（RFC3339）"), "to": str("终点（RFC3339，默认现在）"),
				"horizons": map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1, "maximum": 336}, "description": "比较时长（小时），默认 4、24、48；另外总会给出按研判自带时长（OWN）的结果"},
				"exclude":  map[string]any{"type": "boolean", "description": "是否剔除登记的剔除段（默认是）"}}, "instance", "from"),
			Description: "研判准确率：每条有方向的研判与之后若干小时的价格变化比较，按时长、方向、信心档位统计命中率；观望单独计数。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					Instance string `json:"instance"`
					From     string `json:"from"`
					To       string `json:"to"`
					Horizons []int  `json:"horizons"`
					Exclude  *bool  `json:"exclude"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				from, err := when(p.From, time.Time{})
				if err != nil || from.IsZero() {
					return nil, d.Fail("TIME_INVALID")
				}
				to, err := when(p.To, now().UTC())
				if err != nil {
					return nil, err
				}
				for _, h := range p.Horizons {
					if h < 1 || h > 336 {
						return nil, d.Fail("HORIZON_INVALID")
					}
				}
				return o.Scorecard(ctx, p.Instance, from, to, p.Horizons, p.Exclude == nil || *p.Exclude)
			}},
		{Name: "list_rules", Tier: Observe, Profiles: both, Schema: obj(map[string]any{}),
			Description: "登记的判定规则：编号、条件、等级、良性例外、处置建议。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				if err := strict(a, &struct{}{}); err != nil {
					return nil, err
				}
				return map[string]any{"rules_version": d.RulesVersion, "rules": d.Rules()}, nil
			}},
		{Name: "list_checks", Tier: Observe, Profiles: both,
			Schema:      obj(map[string]any{"instance": str("实例标识或 host；省略为全部"), "since_hours": integer("最近几小时（默认 3）", 1, 720), "only_hits": map[string]any{"type": "boolean"}, "limit": integer("最多几条", 1, 500)}),
			Description: "监视器的检查记录（连续相同结果合并为区间）。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					Instance   string `json:"instance"`
					SinceHours int    `json:"since_hours"`
					OnlyHits   bool   `json:"only_hits"`
					Limit      int    `json:"limit"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				cs, err := store.Checks(ctx, p.Instance, now().UTC().Add(-time.Duration(bound(p.SinceHours, 3, 720))*time.Hour), p.OnlyHits, bound(p.Limit, 100, 500))
				return map[string]any{"checks": cs}, err
			}},
		{Name: "list_issues", Tier: Observe, Profiles: both,
			Schema:      obj(map[string]any{"states": strs("状态（OPEN/WATCH/FIXING/VERIFYING/CLOSED），默认未关闭的全部"), "instance": str("实例标识"), "limit": integer("最多几张", 1, 500)}),
			Description: "问题单列表。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					States   []string `json:"states"`
					Instance string   `json:"instance"`
					Limit    int      `json:"limit"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				is, err := store.Issues(ctx, ports.IssueFilter{States: p.States, Instance: p.Instance, Limit: p.Limit})
				return map[string]any{"issues": is}, err
			}},
		{Name: "get_issue", Tier: Observe, Profiles: both, Schema: obj(map[string]any{"id": str("问题单编号")}, "id"),
			Description: "一张问题单及其全部记录。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					ID string `json:"id"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				i, es, err := store.Issue(ctx, p.ID)
				return map[string]any{"issue": i, "entries": es}, err
			}},
		{Name: "list_reports", Tier: Observe, Profiles: both, Schema: obj(map[string]any{"limit": integer("最多几份", 1, 100)}),
			Description: "巡检报告（最新在前）。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					Limit int `json:"limit"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				rs, err := store.Reports(ctx, bound(p.Limit, 10, 100))
				return map[string]any{"reports": rs}, err
			}},
		{Name: "list_owner_decisions", Tier: Observe, Profiles: both, Schema: obj(map[string]any{"limit": integer("最多几条", 1, 200)}),
			Description: "所有者决定记录（最新在前）。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					Limit int `json:"limit"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				ds, err := store.Decisions(ctx, bound(p.Limit, 20, 200))
				return map[string]any{"decisions": ds}, err
			}},
		{Name: "list_exclusions", Tier: Observe, Profiles: both, Schema: obj(map[string]any{"instance": str("实例标识；省略为全部")}),
			Description: "登记的剔除段。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					Instance string `json:"instance"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				ss, err := store.Segments(ctx, p.Instance)
				return map[string]any{"exclusions": ss}, err
			}},

		{Name: "open_issue", Tier: Record, Profiles: []string{d.Manager},
			Schema:      obj(map[string]any{"title": str("标题"), "severity": map[string]any{"type": "string", "enum": d.Severities}, "instances": strs("涉及实例"), "note": str("现象与证据")}, "title", "severity", "note"),
			Description: "手动开一张问题单（规则命中的 ANOMALY 已由监视器自动开单）。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					Title     string   `json:"title"`
					Severity  string   `json:"severity"`
					Instances []string `json:"instances"`
					Note      string   `json:"note"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				return rec.OpenIssue(ctx, p.Title, p.Severity, p.Instances, p.Note)
			}},
		{Name: "add_issue_note", Tier: Record, Profiles: both,
			Schema:      obj(map[string]any{"id": str("问题单编号"), "note": str("记录"), "evidence": map[string]any{"type": "object", "description": "证据（状态、编号、时间、计数、数值；不含令牌与正文）"}}, "id", "note"),
			Description: "给问题单追加记录或证据。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					ID       string         `json:"id"`
					Note     string         `json:"note"`
					Evidence map[string]any `json:"evidence"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, rec.Note(ctx, p.ID, p.Note, p.Evidence)
			}},
		{Name: "move_issue", Tier: Record, Profiles: both,
			Schema: obj(map[string]any{"id": str("问题单编号"), "to": map[string]any{"type": "string", "enum": []string{d.Open, d.Watch, d.Fixing, d.Verifying, d.Closed}},
				"note": str("理由"), "owner_words": str("关闭时必填：所有者在对话中的原话")}, "id", "to", "note"),
			Description: "改变问题单状态。管理者：OPEN↔WATCH、VERIFYING→FIXING（验证失败）；开发者：认领（→FIXING）、带所有者原话关闭（→CLOSED）。写修复版本用 set_issue_fixed。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					ID    string `json:"id"`
					To    string `json:"to"`
					Note  string `json:"note"`
					Owner string `json:"owner_words"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, rec.Move(ctx, p.ID, p.To, p.Note, p.Owner)
			}},
		{Name: "set_issue_fixed", Tier: Record, Profiles: []string{d.Developer},
			Schema:      obj(map[string]any{"id": str("问题单编号"), "version": str("修复所在版本，例如 v4.0.1"), "note": str("修复内容")}, "id", "version", "note"),
			Description: "写入修复版本，问题单进入 VERIFYING 等待管理者验证。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					ID      string `json:"id"`
					Version string `json:"version"`
					Note    string `json:"note"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, rec.Fixed(ctx, p.ID, p.Version, p.Note)
			}},
		{Name: "verify_issue", Tier: Record, Profiles: []string{d.Manager},
			Schema:      obj(map[string]any{"id": str("问题单编号"), "note": str("验证依据")}, "id", "note"),
			Description: "记录修复后的验证结果（问题不再出现）。关闭仍需所有者承认后由开发者执行。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					ID   string `json:"id"`
					Note string `json:"note"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				return map[string]any{"ok": true}, rec.Verify(ctx, p.ID, p.Note)
			}},
		{Name: "write_report", Tier: Record, Profiles: both,
			Schema:      obj(map[string]any{"summary": str("结论"), "findings": strs("发现"), "handled": strs("已处理"), "issues": strs("涉及问题单")}, "summary"),
			Description: "写一份巡检报告。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					Summary  string   `json:"summary"`
					Findings []string `json:"findings"`
					Handled  []string `json:"handled"`
					Issues   []string `json:"issues"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				id, err := rec.Report(ctx, p.Summary, p.Findings, p.Handled, p.Issues)
				return map[string]any{"id": id}, err
			}},
		{Name: "record_owner_decision", Tier: Record, Profiles: both,
			Schema:      obj(map[string]any{"owner_words": str("所有者原话"), "summary": str("决定内容"), "instances": strs("涉及实例"), "issues": strs("涉及问题单")}, "owner_words", "summary"),
			Description: "按所有者原话记录一条决定。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					Words     string   `json:"owner_words"`
					Summary   string   `json:"summary"`
					Instances []string `json:"instances"`
					Issues    []string `json:"issues"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				id, err := rec.Decision(ctx, p.Words, p.Summary, p.Instances, p.Issues)
				return map[string]any{"id": id}, err
			}},
		{Name: "add_exclusion", Tier: Record, Profiles: both,
			Schema:      obj(map[string]any{"instance": str("实例标识，* 为全部实例"), "from": str("起点（RFC3339）"), "to": str("终点（RFC3339）"), "reason": str("原因"), "issue": str("问题单编号（可省）")}, "instance", "from", "to", "reason"),
			Description: "登记一个剔除段；比较实验与准确率时排除。",
			Call: func(ctx context.Context, a json.RawMessage) (any, error) {
				var p struct {
					Instance string `json:"instance"`
					From     string `json:"from"`
					To       string `json:"to"`
					Reason   string `json:"reason"`
					Issue    string `json:"issue"`
				}
				if err := strict(a, &p); err != nil {
					return nil, err
				}
				from, err := when(p.From, time.Time{})
				if err != nil {
					return nil, err
				}
				to, err := when(p.To, time.Time{})
				if err != nil {
					return nil, err
				}
				return rec.Segment(ctx, p.Instance, from, to, p.Reason, p.Issue)
			}},
	}
}
