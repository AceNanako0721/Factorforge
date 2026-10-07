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

2026-10-08，提交 `026b0e1f74526bdf07f3d05dc74473fe3e6313f3` 已通过既有 SSH 认证实际推送，远端接受工作流变更。[Go CI 首轮记录](https://github.com/AceNanako0721/Factorforge/actions/runs/37644618514) 已启动四项必需检查；上传权限问题已解决，CI 和 main 合并结果另行核验。

推送前历史扫描发现旧 G3 校验器不认识 P3 分支的空 `application.read_api` 模板。采用 P3 已有的完整空模板及严格校验，保留冻结旧模板仅在历史中的兼容，实际暂存 314 个文件版本、所有可达历史 703 个文件版本扫描通过。没有放宽真实值或私有文件限制。

开发机旧版 `gh pr edit` 另返回已退役 Projects classic 的 GraphQL 错误；这不是 workflow 权限问题。PR 正文通过 `gh api` 的 REST PATCH 与结构化 JSON 文件更新，ready 状态通过专门 GraphQL mutation 更新，不调用已退役字段。
