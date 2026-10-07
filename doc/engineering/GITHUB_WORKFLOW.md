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

当前等待通过 SSH 推送并核验新工作流的实际 GitHub 结果。完成后在本节、Go 迁移进度和对应 PR 正文登记提交与 CI 证据，不能提前写成已解决。
