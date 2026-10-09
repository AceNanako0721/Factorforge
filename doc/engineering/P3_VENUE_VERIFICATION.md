# P3 目标产品及 TradFi 日历验证

日期：2026-10-09。先完成本实验，再修改对应 P1/P3 配对设计，最后写生产代码。产品接口可读不等于账户可交易；本文不授予 LIVE，也未自动修改真实配置或交易层乘数。

## 只读实际请求

源码 tests/applications/soxl_jev/experiments/product_lab_test.go，默认跳过。显式私有实验计划限制8次GET、单次15秒和4000000字节；这是实验预算，不是生产默认。固定两个官方域名，无配置/Key读取、签名、账户操作、协议接受POST或订单接口，不自动重试。2026-10-09T06:00:01～06:00:04Z取得8个HTTP 200；原始结果及完整哈希只存runtime/product-lab-20261009，目录0700、文件0600。

| 环境 | exchangeInfo | SOXLUSDT mark/index | SOXLUSDT bookTicker | tradingSchedule |
| --- | --- | --- | --- | --- |
| demo-fapi.binance.com | 901331字节；741标的 | 226字节 | 148字节 | 10470字节 |
| fapi.binance.com | 1147713字节；924标的 | 226字节 | 152字节 | 10470字节 |

模拟盘 exchangeInfo SHA-256：d101447840bb9a807d4942e6cd69746dc8604daeb72001aca8d6a365f32ddca3；日历 SHA-256：23733f349eb627ec3d1e4951d74e412b0202d1fede0b3d5ef942fa8dd3a61489。正式公开环境分别为 bde0726c83119718da7e69df034c575e1902a26c9e52bd160a471d97b90985b6、dfc52769d0abe8d57faf21d28cb4315676426edb7421fe6b157cf0a2d76160bc。环境的上市日期及最大数量不同，不能混用规则。

两者 SOXLUSDT 均为 TRADIFI_PERPETUAL、underlyingType=EQUITY、baseAsset=SOXL、quoteAsset=marginAsset=USDT、status=TRADING；价格步长0.01、数量步长0.01、最小名义5 USDT。这些是本次返回事实，不写成源码固定规则或生产标定值。官方[上市说明](https://www.binance.com/es/support/announcement/detail/a2bce2686fc64a61b44c4335facafe7f)说明跟踪SOXL ETF价格；合约损益不能再次乘ETF的三倍目标。本次未验证账户产品协议/权限、合约单位换算、资金费账单或价格代理适用性。

## 确切实现缺口

已归档真实模拟盘 exchangeInfo 通过临时HTTP服务重放给现有 binance.Market.InstrumentSpecs。显式提供仅为暴露过滤门的开发乘数1，得到0个SOXLUSDT规则。原因是 src/factorforge/trading/adapters/binance/market.go 只接受 contractType=PERPETUAL，跳过真实返回 TRADIFI_PERPETUAL；不是交易所没有产品、API Key无效或下层HTTP丢记录。

修复选择沿用现有通用LINEAR_PERPETUAL值接口，明确适配该交易所枚举；P1不得硬编码SOXL或应用策略。仍要求操作者登记正数乘数，不能因类型可识别便默认1、开启真实执行或声称条件保护已验证。新类型核对基础/报价/结算身份，纳入新类型规则摘要；原普通PERPETUAL规则摘要保持，避免无变化规则抖动。

## 日历方法试验

官方[市场数据API](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/market-data)的 GET /fapi/v1/tradingSchedule 给出底层市场时段，不是合约停止交易时间；[通用说明](https://developers.binance.com/en/docs/products/derivatives-trading-usds-futures/general-info)确认模拟端点。EQUITY与HK_EQUITY/KR_EQUITY/COMMODITY/FX等分开，不能凭同名REGULAR跨市场混用。

实际EQUITY序列42项，时间连续、无重叠/缺口；10项REGULAR，10项各OVERNIGHT/PRE_MARKET/AFTER_MARKET，2项NO_TRADING。提取REGULAR并按America/New_York保留其原始UTC开闭时刻，再调用现有 operations.Calendar.Windows/Plan，得到10个常规及9个非传统窗口。覆盖2026-10-01T13:30Z～2026-10-14T20:00Z；周末窗口65.5小时，不按午夜、日历刷新或上游时段细分重新计数。所有阈值仍由已有TimePolicy提供。

此试验没有接入生产链路、调用P2写入或安装日历资产。下一设计采用可复算私有日历产物：显式环境/绑定、完整产品和时段响应/哈希/接收时间、版本及预算；闭合操作请求，外部JSON允许未知字段但拒绝重复键，未知时段/缺口/重叠/精度/过期整体拒绝。源读取只做快照，运行仍沿已有日历安装和持久计数接口；不把NO_TRADING解释成强制平仓。

生产上游刷新不自动推断节假日；已发布窗口不能由新日历静默改写，新数据只从持久旧末端追加。正式独立日历复核、目标账户能力、乘数/费用/代理、来源许可/语义标定与OD-02仍需各自证据。

## 设计后生产验证

v2.1.8设计先行88c54e9/c1c23d0，之后实现P1通用枚举与P3离线日历编译。相同已归档模拟返回重放现在识别1个SOXLUSDT规则（修复前0）；没有新增外部请求、账户操作或订单。显式乘数仍为开发假设，不能视作生产验证。

生产编译器对DEMO/PUBLIC_MAIN两份完整产品及日历快照分别生成可复算私有产物：各10个常规/19个总窗口，UTC有效范围2026-10-01T13:30Z～2026-10-14T20Z，周末65.5小时。实验时效86400秒/2MB原文/1000时段和4MB文件预算仅供本次重放，没有装入真实配置。实际原生CLI额外成功离线编译同一DEMO请求；真实业务未安装。

Go P3→实际Go P2 HTTP覆盖编译产物和旧inline，验证同版本重启、换版本同覆盖不写入、重叠窗口冲突与末端回退拒绝、准确末端追加后原窗口耗尽次数仍拒绝增险。与完整测试中的PostgreSQL持久重开证据分开记录，不把临时MemoryStore称为数据库持久化。

## v2.1.10 后续：目标测试网账户只读验证

2026-10-09T07:36:59.855833203Z～07:37:01Z，复用 config/config.toml 中已有模拟盘 Key，固定官方 demo-fapi.binance.com，只执行八次签名 GET：/fapi/v3/account、/fapi/v1/accountConfig、/fapi/v1/positionSide/dual、/fapi/v1/multiAssetsMargin、/fapi/v1/openOrders、/fapi/v1/openAlgoOrders，以及 symbol=SOXLUSDT 的 /fapi/v1/symbolConfig、/fapi/v1/commissionRate。后两项按官方 [账户 API](https://developers.binance.com/en/docs/catalog/core-trading-derivatives-trading-usd-s-m-futures/api/rest-api/account) 选择；前六项复用既有 P1 AccountProbe / ReadOnlyAccountTransport。

八项 HTTP 200，单次130～733ms。既有探针确认账户可读、账户交易开关开启、单向/单资产模式；普通及条件挂单数均为零。目标配置返回一条 SOXLUSDT；费率响应包含 symbol、makerCommissionRate、takerCommissionRate、rpiCommissionRate。只证明这些账户/标的读取接口成功；没有以 GET 响应替代写权限、产品协议、实际费用结算或 SOXLUSDT 下单/保护能力验证，也没有据此声明当前所有仓位为零。具体杠杆、名义额度、费率及余额不复制到公开记录。

实验源码 account_lab_test.go 默认跳过，只在显式私有目录下运行。计划固定最多8请求、每次10秒、recvWindow=5000ms、本地request_budget=1000/60秒；这些仅是本次诊断边界，不是供应商限额或生产默认。发送前不可覆盖started标记；HTTP边界再次限定官方测试网、GET、八项路径及目标symbol，预算耗尽或任一错误停止，不重试。不安装执行fence/数据库/Readiness，不接受协议、不修改保证金/杠杆/模式、不发挂撤单。

报告仅存 runtime/p3-account-lab-20261009/report.json，目录0700/文件0600，SHA256 a3b7eb0a7d1c8379bf755c4d02b08061d812dc7ec3987f4148098f141d8ca564；公开日志仅路径、状态、耗时和布尔结论，不记录请求查询串/签名/Key。普通离线测试验证 POST/DELETE、订单路径、正式域名、其他symbol及超预算在底层传输前拒绝。真实配置/Prompt/生产资产不变。

T3-01/T3-07 的账户只读能力已取得这份有限证据。目标产品协议/经济单位、费率政策有效期与实际结算、参考价适用、保护/执行联合验收仍未关闭；正式账户或生产业务实例不从测试网证据继承准入。
