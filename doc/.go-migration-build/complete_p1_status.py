"""Update the draft v2.1.1 implementation status; never edit a specification.

The HTML remains the only version-baseline publication body. This authoring
script edits the Go implementation additions and links, preserving inherited
business paragraphs, diagrams and their frozen-source metadata.
"""
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[2]
BASE = ROOT / "doc/v2.1.1"
OLD_NOTE = "本版调整目标实现为 Go 后端与浏览器 JavaScript（React/TypeScript 编译），三层职责和全部业务式样不变。旧 Python P1/P2 是迁移对照，目前尚未切换生产入口；Go 域计算已开始，HTTP/存储/执行迁移另行验收。以迁移进度为准，旧版验收不能移作 Go 运行证据。"
NEW_NOTE = "本版采用 Go 后端与浏览器 JavaScript（React/TypeScript 编译），三层职责和业务式样不变。P1 已完成完整 Go 实现、本地运行验收及测试网只读/隔离/存储子集；完整 Go 现场交易复验单独登记。P2 数值域已迁移，完整框架及全局工具迁移继续进行；旧 Python 验收不能移作 Go 证据，生产实盘未准入。"


def main():
    if (ROOT / "VERSION").read_text().strip() != "2.1.1":
        raise SystemExit("This draft authoring tool applies to v2.1.1 only")
    for path in [*BASE.glob("*设计书.html"), BASE / "index.html"]:
        text = path.read_text(encoding="utf-8").replace(OLD_NOTE, NEW_NOTE)
        path.write_text(text, encoding="utf-8")
    path = BASE / "01_交易系统层设计书.html"
    text = path.read_text(encoding="utf-8")
    replacements = {
        "api/handlers.go 使用标准库": "api/http.go 使用标准库",
        "采用 pgx/v5 与 pgxpool，首次引入时锁定验证版本": "采用固定 pgx/v5 v5.11.0 与 pgxpool",
        "加载原 migrations/001_trading.sql，保留": "加载原 migrations/001_trading.sql，并应用可选 002_snapshot_order.sql 保留接收顺序；保留",
        "adapters/binance/{protocol,broker,market,testnet}.go": "adapters/binance/{transport,broker,market,probe,remote}.go",
        "entrypoints/{trading-api,execution-sim,execution-live,market-collector,factorforge-trading}/main.go": "entrypoints/{trading-api,execution-sim,execution-live,market-collector,trading-cli,testnet-acceptance}/main.go",
        "仅基础设施依赖；数据库角色、账本与运行事务仍按本项目；引入时固定版本": "基础设施依赖 v5.11.0，MIT；pgxpool/pool.go、tx.go、pgconn；角色、账本及事务仍按本项目设计",
        "domain/decimal/value.go、domain/quantity.go、domain/lease.go、api/dto/decimal.go": "domain、application、ports、adapters、api、workers 与六个 entrypoints；tools/build-trading、export-trading-contract、init-private-config、prepare-trading-profiles",
        "已迁移纯计算及执行租约，测试在 tests/trading；HTTP、存储、账务/撮合/执行仍待迁移和验收": "P1 完整 Go 实现和本地验收完成，真实 PostgreSQL/HTTP/故障/隔离/无 Python 路径进程验证通过；Go 测试网只读子集 12 项通过，完整现场交易复验不由旧报告代替",
    }
    for old, new in replacements.items():
        text = text.replace(old, new)
    additions = '<p data-go-p1-status="complete">运行实现：application/commands.go、operations.go、targets.go、market.go、execution_facts.go、venue.go 调用 ports.Store 的 Create/Read/BoundRun/Transaction；api/http.go 先经 api/dto/decode.go 的冻结形状/默认值/显式空值检查再调用应用服务。workers/executor.go、protection.go、feedback.go 调用 ports.Broker 的提交/查询/撤单及成交/收入/账户方法；collector.go 经 ports.TradingAPI.Call 使用下层本层 HTTP，不拥有交易密钥。adapters/postgres/store.go 在账户行锁事务中写入完整状态、outbox 和追加审计；实际 session_user 身份/跨环境权限及 NOLOGIN 均为执行边界。金额与原 JSONB 业务字段保持，顺序快照与旧写入兼容。</p><p>私有文件由 adapters/configuration/config.go 使用 go-toml/v2 v2.4.3 的 decode.go、marshaler.go 加载；公开格式仅有空内容模板。adapters/isolation/{gateway,filesystem_linux}.go 使用固定 go-landlock v0.10.1 的严格路径限制，空网络 namespace 由原生 unshare 装配；Unix CONNECT 只允许登记目的地，撤权关闭已建立 TLS 并禁止重连。测试网验收入口运行独立 API/签名子进程和本轮 PostgreSQL，显式授权写操作，失败只输出稳定错误码；独立清理仅取消本轮已知保护且每次确认账户已平仓。实验政策不能复制进生产 Readiness。许可证与固定版本见 <a href="../../THIRD_PARTY_NOTICES.md">第三方声明</a>，证据及当前 Go 启动见 <a href="../progress/GO_MIGRATION.md">迁移进度</a>、<a href="../engineering/TRADING_GO.md">运行说明</a>。</p>'
    if 'data-go-p1-status="complete"' not in text:
        text = text.replace('<h2 id="section-11">', additions + '<h2 id="section-11">')
    path.write_text(text, encoding="utf-8")
    path = BASE / "index.html"
    text = path.read_text(encoding="utf-8")
    text = text.replace("本版是 Go 设计及重构启动基线。Python P1/P2 的既有验收保留；Go 域计算已开始迁移，Go HTTP/存储/执行/完整框架尚未验收；", "本版是 Go 设计与迁移基线。Python P1/P2 的既有验收保留；P1 完整 Go 重构及本地运行验收已完成，测试网只读/隔离/存储子集 12 项通过，完整 Go 交易复验单独登记；P2 数值域已迁移，完整框架尚待迁移；")
    text = text.replace("当前 Go 已落地十进制、租约、情绪/账本、仓位/止损和身份类型；服务与工具迁移继续进行。", "P1 的 Go 服务、数据库、适配器、工作进程和独立入口已实现；P2 数值域、情绪/账本、仓位/止损和身份类型已迁移，完整框架及全局工具迁移继续进行。")
    path.write_text(text, encoding="utf-8")
    path = ROOT / "README.md"
    text = path.read_text(encoding="utf-8").replace("（Go 设计与迁移启动）", "（Go 设计与 P1 重构）")
    paragraphs = text.split("\n\n")
    paragraphs[2] = "后端正在由 Python 迁移为 Go；前端目标为 React/TypeScript 编译的 JavaScript。**P1 已完整重构为 Go 并完成本地运行验收**：交易核心、完整 API/CLI、PostgreSQL、SIM/Binance、保护/反馈/采集、独立进程及测试网验收工具均已迁移，运行路径不调用 Python。原生 Go 测试网只读/隔离/存储子集 12 项实际通过，本次没有交易所写请求；完整 Go 测试网交易复验与生产实盘准入单独登记。P2 数值域已迁移，完整框架尚待迁移。见 [迁移进度](doc/progress/GO_MIGRATION.md) 和 [Go 运行说明](doc/engineering/TRADING_GO.md)。"
    paragraphs[3] = "P1 提供 API/CLI、公共行情采集、可重放撮合、保护工作池、目标撤替/反向续接、账户与组合风险、多币种账务、外部事实恢复、PostgreSQL 持久化和受准入控制的签名执行。原 Python P1 的 Binance 合约测试网完整闭环已完成，历史证据保留在 [P1 记录](doc/progress/P1.md)；该记录不能代替 Go 的现场交易验收。"
    text = "\n\n".join(paragraphs).replace("运行 `python tools/init_private_config.py`", "运行 `go run ./tools/init-private-config`")
    text = text.replace("Go 目标设计；四组式样业务正文保持", "Go 设计与 P1 完整重构；四组式样业务正文保持")
    path.write_text(text, encoding="utf-8")
    for name in ("TRADING_DEVELOPMENT.md", "TRADING_RECOVERY_RUNBOOK.md"):
        path = ROOT / "doc/engineering" / name
        text = path.read_text(encoding="utf-8")
        notice = "\n\n2026-10-06：当前 P1 使用独立 Go 二进制，启动/配置/构建见 [Go 运行说明](TRADING_GO.md)。本文保留原操作和恢复职责；历史 Python 命令仅供冻结回归，不能与 Go 同时写同一账户。\n"
        if "2026-10-06：当前 P1" not in text:
            head, rest = text.split("\n", 1)
            path.write_text(head + notice + rest, encoding="utf-8")
    print("Updated v2.1.1 design status and P1 run links; specifications unchanged")


if __name__ == "__main__":
    main()
