"""One-time v2.1.1 design authoring; never rewrites a registered/frozen baseline.

This migration helper is temporary. Published HTML is the sole document body.
"""
from html import escape
import json
from pathlib import Path
import re

from html_documents import document, diagram, table
from check_html_documents import spec_semantics

ROOT=Path(__file__).resolve().parents[1]
VERSION="2.1.1"
NOTE='<div class="note">本版调整目标实现为 Go 后端与浏览器 JavaScript（React/TypeScript 编译），三层职责和全部业务式样不变。旧 Python P1/P2 是迁移对照，目前尚未切换生产入口；Go 域计算已开始，HTTP/存储/执行迁移另行验收。以迁移进度为准，旧版验收不能移作 Go 运行证据。</div>'


def flow(identifier,title,labels):
    nodes=[(100,15+i*100,700,70,label) for i,label in enumerate(labels)]
    return diagram(identifier,title,nodes,[(i,i+1,'') for i in range(len(labels)-1)],height=len(labels)*100)


def section(title,text):return '<h2>'+escape(title)+'</h2>'+text


COMMON=section('Go 迁移共同约束与切换门槛', '''
<p>根 go.mod 是 Go 模块唯一真源，go.sum 固定依赖校验；本版工具链 Go 1.27.1。Go 生产代码仍只放 src/factorforge 对应层，domain、application、ports、adapters、api、workers 六类职责保留；独立 main 放本层 entrypoints/入口名。P1 不导入上层，P2 仅引用 P1 api/dto 的公开值类型和 HTTP 客户端；不创建承载跨层业务的 common。Go 测试放 tests 对应层，数据库迁移仍归属本层 PostgreSQL 适配器。</p>
<p>迁移以 v2.1.0 标签 e97c9e9a380d105a7e96933bdd325e1d9af06d93 的源码、冻结业务式样和 contracts/v2 契约为对照。HTTP 方法/路径、schema_version、十进制字符串、UTC/空值/未知语义、错误码、状态机和授权范围保持；OpenAPI 3.1.0 及历史 JSON Schema Draft 2020-12 不降级。接口不是因 Go 类型名变化自动升版。</p>
<p>新实现不调用 Python 子进程、不嵌 Python 解释器，也不以 Python HTTP 服务作为最终 Go 后端。迁移期间旧源码与入口保留，用合成夹具捕获对照；不得双启动同一账户的执行者、双写同一运行，或因 Go 模块编译通过就替换运行服务。切换前记录当前版本、运行状态和库快照，停止旧执行者并取得隔离证据，由新进程恢复对账；回退也须停止并隔离新执行者，不能同时使用同一签名身份。</p>
<p>替换全部活跃初始化、布局/版本/HTML/公开内容/历史契约检查、构建、hooks、CI 与运行工具；最终用没有 Python 的受控环境完成 Go 构建、单元/故障/真实 PostgreSQL/独立进程/SIM 验收。历史冻结文档中的 Python 文字或文件保留为历史，不要求运行其作者脚本；旧活跃作者工具在 Go 替代检查通过后退役。迁移夹具 JSON 自带源提交和 fixture_only，Go 测试读取固定结果，不依赖即时 Python oracle。真实配置及 Prompt 不迁移进夹具。</p>
<p>发布记录应逐模块区分已迁移、仅设计和已运行验收；P1/P2 原 Python 运行证据继续保留但不标成 Go 已通过，P3 与管理台仍为未实现。实盘准入、业务校准及 DEC-06 等原未决项保持。本次不授权实际交易。</p>''')

P1=section('9 Go 包、数值与交易运行实现', '''
<p>api/handlers.go 使用标准库 net/http.ServeMux 注册现有 /api/v2/trading 路由，Handler 只调用 application 服务。写正文先限制长度、校验 Content-Type，json.Decoder.DisallowUnknownFields、单文档 EOF 和显式字段/枚举/跨字段校验；不接受浮点金额。需要区分缺失与 null 的字段使用存在标记结构；必需布尔/整数不能仅由零值判断。保持 Pydantic 原 ID、时间因果、OHLC、规则、保护和账户政策约束，按现有 OpenAPI 比对错误状态与业务 Problem。</p>
<p>domain/decimal/value.go 封装 cockroachdb/apd/v3 v3.2.3，不向调用者暴露可变系数或全局 Context。普通运算沿用原 Decimal 28 位、ROUND_HALF_EVEN 和逐操作顺序；舍入到数量步长仍为 ROUND_DOWN（趋零）。解析只接受有限十进制字符串，保留负零/指数/尾数语义；错误不打印输入。Math 属于一次计算，错误传到用例，在提交/出站前检查，不能把失败生成的零值写入状态。api/dto/decimal.go 只公开稳定值类型与算术接口，供 P2 依赖；这是 P1 通用数据能力，不是跨层业务 common。</p>
<p>application/{commands,targets,reconcile,recovery,execution_facts,emergency}.go 与旧用例逐项对应。写事务取得运行/账户串行锁，再核验 aggregate_version、幂等请求指纹、所有权/epoch、硬风险和最不利未成交量；持久化命令及 outbox 成功后才返回受理。workers/{executor,protection,feedback,market}.go 独立运行，出站操作前/后记录状态。超时写结果使用 UNKNOWN 和原 client_order_id 查询；不能依赖通用 HTTP 重试重发写单。普通单和条件保护使用不同适配端口及查询事实。</p>
<p>ports/store.go 定义 context.Context 贯穿的事务/查询端口，不暴露 pgx 类型。adapters/postgres/store.go 采用 pgx/v5 与 pgxpool，首次引入时锁定验证版本；加载原 migrations/001_trading.sql，保留 trading_sim/trading_live 模式、数据库角色、外键和追加账务。JSONB 解码不经 float64，数量/时间由显式 DTO 读取；原状态没有适配证据时不得自动升级数据库或重新赋予执行权。序列化后的金额仍是字符串。事务取消/提交结果未知时先读幂等结果。</p>
<p>adapters/sim/broker.go 迁移可用时点、延迟、参与率、部分成交、手续费、资金、OHLC 保守路径和影子不撮合规则。adapters/binance/{protocol,broker,market,testnet}.go 用 net/http 和明确 Transport 超时实现限流、签名、时间同步、原 ID 查询及状态映射；签名密钥只在执行入口读取 config/config.toml。API/行情进程不能装配签名执行凭据。</p>
<p>domain/lease.go 保持租约不等于隔离：到期后替换仍需旧 epoch 隔离证据；续租、出站 epoch、停机恢复锁和 DONE outbox 不被新执行者改写。entrypoints/{trading-api,execution-sim,execution-live,market-collector,factorforge-trading}/main.go 各自装配本层，P1 二进制不包含 strategy/applications。LIVE 默认未就绪，测试网能力/故障闭环须在另行授权的迁移验收中复测。</p>''')+flow('go-p1','图 D1-Go：事务、执行权与结果未知',[
'HTTP / CLI → Auth + typed DTO|固定 environment / account / run，拒绝未知字段',
'application → Store.Transaction|版本 / 幂等 / 所有权 / 风险 → 命令 + outbox',
'executor / protection → 有效 lease + 已核验隔离|按端口出站；签名秘密只在执行进程',
'回报 / 原 ID 查询 → execution_facts / accounting|去重记账 + 保护事实；UNKNOWN 禁止盲目重发',
'恢复 / SIM 故障验收通过后切换|上层未部署仍独立运行；LIVE 另行准入'])

P1+=section('10 P1 开源参考与本版落地状态',table(['部件','固定来源 / 部分','复用方式与验收'],[
['十进制','<a href="https://github.com/cockroachdb/apd/tree/6d9c587326e78bcfbea630bf52893ff45f6f9ed5">apd v3.2.3</a>；context.go、decimal.go、round.go','依赖锁定，不复制其源码；Apache-2.0，保留依赖许可证；固定 JSON 与算术对照'],
['HTTP 服务','<a href="https://go.dev/doc/go1.22">Go ServeMux 方法/路径路由</a>、net/http','标准库调用；认证/错误/重试/超时按 Factorforge 契约设计'],
['PostgreSQL','<a href="https://github.com/jackc/pgx">pgx/v5</a>；pgxpool、事务及类型映射','仅基础设施依赖；数据库角色、账本与运行事务仍按本项目；引入时固定版本'],
['Binance 网络参考','局域网 TradingAgent internal/exchange/{binance_client,binance_futures}.go；正式引用前核验其许可和固定提交','参考签名/时钟/原 ID 查询及错误分类；本版未复制其源码，不继承 float64 账务'],
['本版已开始的源码','domain/decimal/value.go、domain/quantity.go、domain/lease.go、api/dto/decimal.go','已迁移纯计算及执行租约，测试在 tests/trading；HTTP、存储、账务/撮合/执行仍待迁移和验收']]))+COMMON

P2=section('12 Go 框架用例、数值及持久化实现', '''
<p>domain/{sentiment,position,stop,events,case,attribution,learning,validation,regime,time_policy,performance}.go 与原域函数对应，不导入交易层执行/账务/存储或绑定标的/模型。本版首先落地 numerical_types.go 定义纯算法输入，不宣称已经替换完整 StrategyState 或 HTTP 请求 DTO。身份使用 identity.go 中独立 ApiScope 与 WorkloadCapability 命名类型，各自 JSON 枚举校验；PublicPrincipal 不能解码 signal:sim/live，WorkloadIdentity 不使用公共 scope 代替内部能力。</p>
<p>sentiment.Decay 沿用局部 40 位上下文；seconds/half_life → 负号 → ln(2) → 乘法 → exp → amount 的逐步舍入保持。apd 默认同精度 Exp 在前期样例有末位差异，Math.Exp 使用 2×/3×输出精度的工作计算，再分别舍入到原精度；两者不一致则返回数值错误，不注入情绪。额外位数是实现工作精度，不是业务容差/标定参数，原 numerical_tolerance 不改。较大指数、下溢/异常和长期停机仍需对照，不能用少量样例覆盖整个输入域。</p>
<p>sentiment.{AppendEntry,VerifyLedger,Waterfill,AdvanceContributions,Pool} 保留逐贡献账本、真实时间消耗、共同价格预算、high_water 与过期原因。Advance 在局部副本计算，通过数值错误及账本守恒检查才提交。原 Python 集合迭代顺序不稳定；Go 分配使用稳定键顺序，完整多贡献回放须在登记容差内核验且逐条保存差异；不能因此扩大业务容差。停机时间使用原 elapsed-time 转换语义，时间戳因果与极长时段由专门边界测试验证。</p>
<p>position 保留 Financier 来源的滞回等号规则、理论暴露、数量趋零舍入、先降险后增险的共同 alpha 投影。ExistingRiskScale 保留净额对冲可行区间下界和上界，未管理风险已经超限或区间为空仍要求核验，不能宣称归零已解决。二分使用原 numerical_tolerance；无法继续表示中点时报数值故障，不无限循环。stop 保留已完成且当时可用 K 线、鲁棒噪声、异常缺口不放宽止损、价格向入场方向取 tick、费用/滑点/缺口的单笔风险以及实际已核验保护减险。</p>
<p>application/{object_service,event_service,score_admission,decision_cycle,case_service,learning_service,validation_runner,risk_correction}.go 通过 ports.Store 的事务计算周期并写 Decision/TargetOutbox/账本/审计，原 clock cutoff、TTL、owner_epoch 与目标反馈保持。adapters/trading_v2.go 仅用 P1 /api/v2/trading：市场/账户/仓位/所有权/成交/保护/外部事实 GET 与 owner 目标/保护写契约，不直接复用 P1 执行或数据库。写目标保留请求版本及期限；DELIVERY_UNKNOWN 查询回执后才能决定续接。</p>
<p>api/{public,workload,handlers,dto}.go 用 net/http 注册原两个 listener，公共写研究/对象权限与内部 signal 资格分别校验；SIM/LIVE 身份固定到部署、实例和对象。adapters/postgres/store.go 用 pgx/v5、独立角色与原 migrations/001_strategy.sql；保留公开/工作负载隔离、交易账本与研究回执分表及追加审计。case/learning/validation 的去重、污染隔离、封存数据、固定步长、候选/验证/激活/回滚状态不因 Go 迁移改变。完整持久化模型和数据库恢复是后续切换门槛。</p>
<p>entrypoints/{strategy-api,strategy-scheduler,strategy-feedback,factorforge-strategy}/main.go 独立装配；执行保护仍由 P1 负责。S2-024 待实现查询继续由本层只读用例提供，不能为管理台用 Go 数据库连接替代未实现接口。workers 明确取消/关闭生命周期，不能以 goroutine 数量冒充研究/交易及 SIM/LIVE 的资源隔离。</p>''')+flow('go-p2','图 D2-Go：资格、账本、投影与执行事实',[
'public listener / workload listener|独立身份类型 → score_admission；研究不授予信号',
'decision_cycle → 前周期成交 / 保护反馈|sentiment 衰减 / 兑现 → VerifyLedger',
'position + stop → 先降险 / 目标投影|登记容差 / hard risk / owner_epoch',
'Store.Transaction → 决策 + TargetOutbox|trading_v2 HTTP → ACK / UNKNOWN / REJECTED',
'案例观察 → 归因 / 验证 → 固定步长学习|激活 / 监测 / 回滚保持原控制流'])

P2+=section('13 P2 参考、固定对照与本版落地状态', '''
<p>本版复用当前已审查的 Financier 固定提交 3708a31d77cb57c0f972828488449efc341dfbf1 / engine/app/core/fusion_hub.py 的 _gate_direction 等号边界；Go position.LevelFor 延续现有改编，保留 MIT 与版权声明。timeseriescv 固定提交 cb04fb6ea7a0b2c15920ca253f882336fe336ba8 / timeseriescv/cross_validation.py 的 purge 思路将在 validation.go 迁移，不引入 pandas/NumPy，不增加未来训练分支。</p>
<p>本版已开始迁移 sentiment.go、position.go、stop.go、identity.go；固定合成结果在 tests/strategy/fixtures/go_migration.json，源提交与 fixture_only 可查，测试逐项比较数值/时点/状态/账本。tools/capture_migration_fixtures.py 是暂时的旧实现捕获工具；Go 测试不运行它、不读取配置、不调供应商。API、周期事务、完整案例/学习与 PostgreSQL/进程闭环尚未迁移，不能把域对照标为 T2 整体验收完成。</p>''')+COMMON

P3=section('10 Go 实例模块、下层接口与进程边界', '''
<p>本应用目标实现全部 Go；entrypoints/{soxl-jev-api,ingest-worker,trading-analysis-worker,research-analysis-worker}/main.go 独立启动。config、monitoring、evidence、routing、analysis、submission、reports、operations 的职责和全部 S3/T3 保持；Provider/Source/Extractor 是带 context.Context 的 Go interface，Mock 也用 Go 实现，不启动 Python sidecar。标准库 HTTP 客户端只接供应商正式登记协议，不猜 SDK；JEV 供应商版本/来源许可/抽取器等原未决项继续保留。</p>
<p>submission/framework_client.go 用框架公共创建对象/事件接口和独立内部评分 listener，按既有 schema_version 提交版本化评分、修订与证据；保存 AdmissionReceipt，不把回执升级成成交。operations/trading_read_client.go 只调用 P1 产品/行情/运行/健康查询；本应用不导入 P1 执行组件，不读取签名密钥，不直接写 P1/P2 数据库。实例 spec/score/request 时间仍为真实可用时点。</p>
<p>analysis/provider.go 把状态和有限问题映射到 JEV 请求，用 encoding/json 明确 DTO 校验格式/方向/质量/弃权；提示词由 analysis/prompt_provider.go 从 config/config.toml 指定的私有文件载入。只允许模板进公开仓库，HTTP 错误、追踪、报告和模型记录均不给出真实 Prompt 或供应商密钥。数据库队列表/角色/SIM-LIVE保留预算、抓取/抽取的保留容量及上游共享限流验证按原第3章实施，不能把多个 goroutine 当成权限边界。</p>
<p>reports/read_service.go、operations/query_service.go、api/read_dto.go 实现 S3-018 的 /api/v2/instances/{instance_id} GET 投影，全部具体下层调用和受限原文规则按原第9章及管理台设计清单。HTTP/存储/供应商适配完成后用 Go Mock 验证创建→评分→修订→撤销与许可/身份/资源故障。P3 当前尚无正式实现，本版不为了展示 Go 迁移制造假生产 Provider 或实例。</p>''')+flow('go-p3','图 D3-Go：来源与模型输出经过确定性边界',[
'Go ingest → Source / Search / EvidenceExtractor|许可 / 来源 / 时间 / facts + spans',
'routing policy → 独立研究或交易持久任务|身份 / 对象 / 环境固定；未知隔离',
'Go AnalysisProvider + 私有 PromptProvider|JEV 有限 DTO 校验 / 弃权 / 版本',
'framework_client → 创建事件 / 首次评分 / 修订|公共研究和内部信号分别调用；读取准入回执',
'实例 read API → 受限报告 / 证据 / 运维|console 只读；不越过下层发单'])+COMMON

WEB=section('12 Go BFF 与浏览器构建实现', '''
<p>console 后端全部 Go：api/handlers.go 用 net/http 注册第3章 /api/v2/console 路由；application/{session_service,selection_registry,overview_query,market_query,event_query,learning_query,safe_export}.go 完成授权、绑定、读查询组合和字段白名单。clients/{trading_read,strategy_read,instance_read}.go 是仅允许清单 GET 的 net/http 客户端，保留第4～6章逐端点的参数、DTO与已有/未实现状态；禁止直读下层数据库或 workload listener。旧源码位置仅作契约对照，新实现不会依赖 FastAPI 服务。</p>
<p>entrypoints/console-api/main.go 装配同源 API 与静态资源，浏览器构建包由受控目录或 go:embed 嵌入；构建工具将资产交给本入口，不在服务器启动 Vite/Node。API fallback 只处理前端白名单路径，/api 错误不可返回 HTML index；私有 config/prompts 不在静态目录。build 工具必须检查资产不存在源地图敏感片段、真实 Provider 地址或 Prompt。</p>
<p>前端仍按原第1、8、10章采用 React+TypeScript+Vite，浏览器产物是 JavaScript；这不引入 Python。package.json/pnpm-lock.yaml 仅在 console/web，Node/pnpm 用于前端构建和浏览器测试。固定的 shadcn Admin / Lightweight Charts 来源与 FreqUI 只参考交互的规则保持，尚未引入真实组件或 npm 依赖。切换后运行只需要 Go 二进制、PostgreSQL、静态资产和私有配置，不需要 Python 或 Node 服务。</p>
<p>SessionService 用 Go 密码哈希验证、随机会话 ID、服务端会话表、HttpOnly/Secure/SameSite Cookie；challenge/预会话/登录轮换/CSRF/Origin/退出/失效语义按第2章保持。缺认证配置时不可对外匿名启动。JSON SafeDTO 显式白名单，不把 map[string]any 或未知 audit.detail 直发浏览器；异步取数用 context 超时与独立预算，各区时间/快照/环境分别保留。Decimal 字符串在 Go 与浏览器都不转换为账务 float64；图表坐标转换只在范围检查后进行。</p>
<p>Go 会话/越权/跨环境/撤权/未知字段/查询失败测试放 tests/applications/console，React/浏览器测试放 tests/web；正式验收必须接 Go P1/P2 本地 SIM，区分目标受理与成交，并在缺 P3 时显示未实现。console 关闭不影响下层运行和 P3 工作池。管理台源码仍未实现，本版调整目标后端，不把设计图标成现有页面。</p>''')+flow('go-console','图 D4-Go：同源静态页与严格只读 BFF',[
'React / TypeScript → 构建为静态 JavaScript|Go console-api 同源提供页面与 /api/v2/console',
'SessionService + SelectionRegistry|challenge / CSRF / 权限 / 绑定 / 环境',
'ReadQuery → 三类 GET-only client|各区超时 / 能力状态 / 源时间；不读下层 DB',
'SafeDTO 白名单 → 浏览器 QueryCache|绑定版本 / generation 校验；退出 / 撤权清除',
'桌面 / 窄屏 / 故障与本地 SIM 联调|页面关闭后 P1 / P2 / P3 仍独立运行'])+COMMON


def body(text):return re.search(r'<article id="document-body">(.*?)</article>',text,re.S).group(1)


def main():
    target=ROOT/'doc'/('v'+VERSION)
    registry=json.loads((ROOT/'doc/releases.json').read_text(encoding='utf-8'))
    if target.exists() or any(r['version']==VERSION for r in registry['releases']):raise SystemExit('Refuse to overwrite an existing/registered baseline')
    target.mkdir()
    appendices={'01_交易系统层设计书.html':P1,'02_策略化框架层设计书.html':P2,'03_SOXLUSDT_JEV应用实例设计书.html':P3,'04_Web管理台设计书.html':WEB}
    for source in sorted((ROOT/'doc/v2.1.0').glob('*.html')):
        text=source.read_text(encoding='utf-8')
        kind=re.search(r'name="ff:kind" content="([^"]+)"',text).group(1)
        if kind=='specification':
            updated=text.replace('name="ff:version" content="2.1.0"','name="ff:version" content="'+VERSION+'"')
            updated=updated.replace(' · Factorforge v2.1.0</title>',' · Factorforge v'+VERSION+'</title>').replace('v2.1.0 · 2026-10-05 ·','v'+VERSION+' · 2026-10-05 ·')
            updated=updated.replace('版本：2.1.0；日期：2026年10月5日。','版本：'+VERSION+'；日期：2026年10月5日。')
            if spec_semantics(text,'.html')!=spec_semantics(updated,'.html'):raise ValueError('Specification semantics changed')
        else:
            content=body(text).replace('版本：2.1.0；日期：','版本：'+VERSION+'；日期：')
            content=re.sub(r'<h([23]) id="[^"]+">',r'<h\1>',content)
            pair=re.search(r'name="ff:pair" content="([^"]+)"',text)
            if kind=='design':
                content=content.replace('Python 3.11、FastAPI、PostgreSQL 沿用现有设计方向，确切依赖版本和锁文件在 P1 验证后冻结，不从旧设计猜供应商版本。','Go、net/http 与 PostgreSQL 为本版目标技术栈，Go 依赖以 go.mod/go.sum 固定；迁移约束和落地状态见新增 Go 实现章节。')
                content=content.replace('内部 Decimal','内部有限十进制 Value')
                content=content.replace('仅前端构建；不替代根 Python 包定义','仅前端构建；Go 后端由根 go.mod 定义')
                content=content.replace('Python API 以同源方式','Go console API 以同源方式').replace('P1/P2 wheel 不包含 console','P1/P2 独立 Go 二进制不包含 console')
                content+=appendices[source.name]
            elif kind=='plan':
                content=content.replace('Factorforge v2.1.0','Factorforge v'+VERSION)
                content=content.replace('当前已完成P1/P2既有实现与v2.1.0文档基线；新增只读查询、P3和Web管理台尚未实现，下一阶段按P3推进。','当前 Python P1/P2 及 v2.1.0 基线作为对照；先按 G0～G4 完成 Go 迁移，再推进新增只读查询、P3 与管理台。')
                content+=section('Go 迁移阶段（先于新应用启用）',table(['阶段','应完成事情','完成条件'],[
                ['G0 设计与固定对照','发布 Go 设计，固定契约、源提交与合成行为结果','四组式样语义不变，设计配对完整，对照来源可查'],
                ['G1 P1','迁移交易域、账务、仿真、API、存储、执行及运行工具','独立 Go P1 的适用 T1、SIM/故障/恢复验收通过'],
                ['G2 P2','迁移情绪、周期、执行反馈、案例、学习、研究与权限/存储','逐周期对照及独立 Go T2、数据库/进程/SIM 验收通过'],
                ['G3 工具与切换','替换活跃 Python 检查/构建/hooks/CI，隔离旧执行后恢复切换','无 Python 环境可构建测试运行，回退与隔离验证齐全'],
                ['G4 上层','用 Go 实现 P3/console 后端和 React 浏览器页','原 T3/T4 分阶段通过，供应商/LIVE 原未决项不自动解除']]))
            elif kind=='index':
                content=content.replace('Change-Type: specification。新增平级Web管理台及下层只读追溯要求，三层业务职责与依赖方向保持。','Change-Type: design。Go 后端迁移与四本设计调整，四本式样及三层职责/依赖方向保持。')
                content=content.replace('Factorforge v2.1.0 文档基线','Factorforge v'+VERSION+' 文档基线')
                content=content.replace('本版是文档发布。P1/P2已有实现及SIM机制验收；新增只读接口、P3、console均未实现。','本版是 Go 设计及重构启动基线。Python P1/P2 的既有验收保留；Go 域计算已开始迁移，Go HTTP/存储/执行/完整框架尚未验收；新增只读接口、P3、console均未实现。')
                content+=section('6 本版 Go 迁移与证据', '<p>目标后端全部 Go，前端 React/TypeScript 编译为 JavaScript；式样业务正文不变。四本设计追加具体 Go 模块、端口/实际 HTTP 调用、参考、异常与切换门槛；新设计不会让旧 Python 验收自动覆盖 Go。<a href="../progress/GO_MIGRATION.md">当前迁移进度</a>、<a href="../engineering/REPOSITORY_LAYOUT.md">文件树规范</a>，历史基线 <a href="../v2.1.0/index.html">v2.1.0</a> 冻结。当前 Go 已落地十进制、租约、情绪/账本、仓位/止损和身份类型；服务与工具迁移继续进行。</p>')
            content=NOTE+content
            updated=document(source.stem,content,kind,pair.group(1) if pair else None,version=VERSION)
        (target/source.name).write_text(updated,encoding='utf-8')
    registry['releases'].append(dict(version=VERSION,directory='doc/v'+VERSION,change_kind='design',date='2026-10-05',reason='业务式样不变；四本设计改为 Go 后端、浏览器 JavaScript，开始 P1/P2 数值域重构并登记迁移切换门槛。',format='html',document_pairs=registry['releases'][-1]['document_pairs']))
    (ROOT/'doc/releases.json').write_text(json.dumps(registry,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    (ROOT/'VERSION').write_text(VERSION+'\n',encoding='utf-8')
    print('Prepared v'+VERSION+'; frozen originals unchanged, specification semantics identical')


if __name__=='__main__':main()
