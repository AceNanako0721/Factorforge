# GitHub 工作流维护与认证故障记录

更新：2026-10-08。属于仓库操作记录；不修改冻结式样、设计、VERSION 或发布基线。

## 已确认的现象与原因

普通源码推送和 PR 操作可用，修改 `.github/workflows/contracts.yml` 被拒绝。这是工作流文件的写入权限问题，不代表已有 GitHub Actions 不能运行。

- 开发机当前 GitHub CLI OAuth scopes 为 `gist, read:org, repo`，缺少 `workflow`。HTTPS 推送新增或修改工作流会返回 `refusing to allow an OAuth App to create or update workflow ... without workflow scope`。
- GitHub 连接器修改同一文件返回 HTTP 403 `Resource not accessible by integration`。连接器和开发机 Git 认证是不同通道；不能以连接器拒绝推断 SSH 密钥失效。
- 本对话历史中，2026-10-04 07:33 UTC 已出现相同 OAuth 拒绝；随后以仓库限定的 SSH push URL 成功推送 P1 分支。2026-10-05 也出现过同类拒绝。因此没有证据表明这次权限突然丢失。
- 2026-10-08 复核：开发机已有 SSH 认证仍能读取本仓库 HEAD。Go 工作流之前仅保存在本地，远端仍执行已退役 Python 工具，导致 PR #13/#14 的三个 job 失败；这不等同于 Go 运行测试失败。

GitHub 的权限规则见 [OAuth scopes](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps) 和 [Contents API](https://docs.github.com/en/rest/repos/contents#create-or-update-file-contents)。OAuth 工作流写入需要 workflow scope；应用令牌另有 Workflows 写权限。普通仓库权限不能代替它。

## 处理顺序

1. 先检查具体失败通道、CLI scope 和错误码；区分修改 YAML、运行 Actions 和测试本身失败。
2. 本开发机优先沿用已有且已获授权的 GitHub SSH 认证。仅对本次推送指定 URL，不改全局 Git 认证、不写入 Token、不触碰 main 保护规则：

   ```sh
   git -c remote.origin.pushurl=git@github.com:AceNanako0721/Factorforge.git push origin HEAD
   ```

3. 推送前按 CONTRIBUTING 检查实际暂存内容及可达历史。推送后确认远端提交包含新 YAML，查看该提交的四项必需 CI；全部通过后经 PR squash 合并，并再次核验 main 检查。
4. 若 SSH 实际写入仍被拒绝，保留明确错误再决定是否补授权。只有选择 OAuth 通道时，才需要用户完成 `gh auth refresh -h github.com -s workflow` 的 GitHub 授权。设备码有期限，不能复用过期码；无浏览器的开发机使用 `GH_BROWSER=true` 防止启动远端 GUI。
5. 不在公开文件、PR、日志中记录密钥、Token、授权设备码或真实配置。CI 使用合成夹具和本地数据库，不能访问真实交易或模型配置；CI 通过不代表生产 LIVE 准入。

## 解决结果

2026-10-08，提交 `026b0e1f74526bdf07f3d05dc74473fe3e6313f3` 已通过既有 SSH 认证实际推送，远端接受工作流变更。[Go CI 首轮记录](https://github.com/AceNanako0721/Factorforge/actions/runs/37644618514) 的四项必需检查全部通过；上传权限问题已解决。

- [PR #13](https://github.com/AceNanako0721/Factorforge/pull/13)：四项检查通过后 squash 合并，main 提交 `46182a8e57b813cb7f00f774adb0c1da0e7d43c9`；[合并后 main CI](https://github.com/AceNanako0721/Factorforge/actions/runs/37645172048) 四项通过。
- [PR #14](https://github.com/AceNanako0721/Factorforge/pull/14)：先纳入上述 main，再核验 [PR CI](https://github.com/AceNanako0721/Factorforge/actions/runs/37645316635) 四项通过，squash 合并提交 `f3832a8254f5c90c0e39662c73d0c4e126b98752`；[合并后 main CI](https://github.com/AceNanako0721/Factorforge/actions/runs/37645623981) 四项通过。

两次合并都指定已验收的 PR head，不跳过检查或改 main 保护。GitHub 已删除合并分支；再次删除返回 Reference does not exist 不代表合并失败。该轮 GitHub CI 与合并收尾完成；G4 上层实现另记进度，不把本次通过记成 JEV 联调或生产实盘准入。

推送前历史扫描发现旧 G3 校验器不认识 P3 分支的空 `application.read_api` 模板。采用 P3 已有的完整空模板及严格校验，保留冻结旧模板仅在历史中的兼容，实际暂存 314 个文件版本、所有可达历史 703 个文件版本扫描通过。没有放宽真实值或私有文件限制。

开发机旧版 `gh pr edit` 另返回已退役 Projects classic 的 GraphQL 错误；这不是 workflow 权限问题。PR 正文通过 `gh api` 的 REST PATCH 与结构化 JSON 文件更新，ready 状态通过专门 GraphQL mutation 更新，不调用已退役字段。

G4 首批实例链路的 [PR #15](https://github.com/AceNanako0721/Factorforge/pull/15) 在 [PR CI](https://github.com/AceNanako0721/Factorforge/actions/runs/37703890677) 四项成功后以 head `01d23c136ba2693174ddb69163051c7ba742c2f5` squash 合并；main 提交 `fc5aa89ffc71bf0c704eb9ea96fcbc2a6f3e73e9` 的 [CI](https://github.com/AceNanako0721/Factorforge/actions/runs/37704205540) 四项成功。GitHub 交付覆盖已实现范围，不关闭尚缺生产标定或时段装配。

管理台追加 console-checks，第五项固定 Go/Node/pnpm 版本、无依赖生命周期脚本构建、TypeScript/单元/实际 Chromium 测试及静态资产检查。Node 只在构建测试 job 使用；四项既有检查名称保持。涉及 YAML 的推送继续沿用上述仓库限定 SSH；本任务等待全部五项成功再合并，不把四项旧保护检查通过当第五项已通过。

[PR #16](https://github.com/AceNanako0721/Factorforge/pull/16) 的 [五项 CI](https://github.com/AceNanako0721/Factorforge/actions/runs/37708667804) 全部成功，以精确 head `764e3996cd6029a84a4edcd38819a8df0db17cc2` squash 合并。main 提交 `ee1cb938f39f27986d176960d376e526e2e5730c` 的 [五项 CI](https://github.com/AceNanako0721/Factorforge/actions/runs/37708881703) 全部成功；主分支保护没有放宽。推送前索引 420 个、可达历史 816 个 Git 文件版本扫描通过。

[PR #17](https://github.com/AceNanako0721/Factorforge/pull/17) 以精确 head 59aa35938c3725496e3a7e50b42f64fa094afa56 的五项 CI 成功后 squash 合并；main 59721f1fbdb883058b7442b76c94c411bc3a25aa 的 [五项 CI](https://github.com/AceNanako0721/Factorforge/actions/runs/37772988086) 均成功。运行记录/周期报告和模型时钟一致性修复已交付；后续日历/报告投影设计发布独立修订版，旧 HTML 不覆盖。

PR #18 的 [首轮 CI](https://github.com/AceNanako0721/Factorforge/actions/runs/37774156935) 中 contract-checks 拦截管理台契约未同步：实例新增 `report_details` 后，console 投影已生成，但 `contracts/v2/console/openapi.json` 仍是旧产物。该次其余四项成功，失败不是认证或 Actions 不可用。补齐 `tools/export-console-contract` 的生成结果，重新依序运行四类契约导出、投影输入校验与契约校验；提交实际生成文件后，等待新 head 的全部五项检查，再合并并确认 main。不得跳过生成文件差异检查。
