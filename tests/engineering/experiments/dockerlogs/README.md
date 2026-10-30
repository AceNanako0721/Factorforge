# Docker 日志持久化先行试验

仅为可复现的部署方法试验，不是生产日志组件，不由常规 `go test ./...` 自动启动。

从仓库根目录手动运行，要求本机 Go 和 Docker Compose，验证目标为 Linux/ARM64：

```sh
go run ./tests/engineering/experiments/dockerlogs --run
```

程序从本目录 `probe/` 构建静态合成进程，使用 `FROM scratch`，不拉取其他项目镜像。它为每次执行生成独有的 `ff-log-lab-*` Compose 项目和 `factorforge-log-lab:*` 镜像，只挂载本次新建的 `runtime/docker-log-lab-*` 日志目录；不开端口、无网络、不挂 Docker socket、不读取配置、提示词或凭据。进程以宿主 UID/GID 运行，限制 CPU、内存和进程数。

试验包装器将合成子进程的 stdout/stderr 同时写入私有日志文件和 Docker 输出，使用独占文件锁、文件同步、大小轮转及信号转发。小容量参数仅用于快速触发试验：持久日志每份 16 KiB、3 份备份加当前文件；Docker 本地日志配置另为每份 16 KiB、2 份。这些不是生产保留策略。

验证内容包括正常重启、删除后重建、历史 stdout/stderr 与停机尾日志、SIGTERM 转发、子进程退出码、多个写入者冲突、只读目录启动失败、轮转上限及原有容器不受影响。

成功或失败都会尝试清理本次 Compose 容器和镜像；日志与 `result.json`、构建日志、分场景 Compose 配置保留在打印出的 runtime 目录。若清理失败，程序报错并保留明确项目名，禁止通过全局 prune 清理。不要将其他项目的资源填入该工具，程序没有接受任意项目名或日志目录的入口。

本试验不证明宿主断电/磁盘故障时零丢失，也不定义生产日志失败时交易保护应如何继续。生产采用前仍需完成配对设计，确定角色命名、字段与脱敏、保留期、信号/子孙进程、磁盘满、备份恢复和生产进程接入；不得直接把实验程序作为生产 entrypoint。

结果及限制见 [试验记录](../../../../doc/engineering/DOCKER_LOG_PERSISTENCE_EXPERIMENT.html)。
