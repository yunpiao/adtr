# 固定容器镜像来源（2026-10-09）

本次 CI 恢复只为原镜像增加 `public.ecr.aws/docker/library/` 前缀，保留完整 tag 与
OCI index digest。PostgreSQL 的 Python 集成/浏览器/LDAP 夹具与 Compose 使用同一固定镜像；
Go 只用于 Dockerfile 编译阶段，运行阶段仍为 `scratch`。不新增账户登录、凭据、镜像回退、
工作流或超时修改。[PR #84](https://github.com/yunpiao/adtr/pull/84) 的后续精确 head CI 仍是运行门禁。

## 官方来源与固定身份

[Docker 官方说明](https://www.docker.com/blog/news-from-aws-reinvent-docker-official-images-on-amazon-ecr-public/)
明确给出该命名空间和自动发布方式；[AWS 官方公告](https://aws.amazon.com/blogs/containers/docker-official-images-now-available-on-amazon-elastic-container-registry-public/)
确认 Docker 是发布者，公开拉取无需 AWS 账户。来源判断不只依赖仓库名称。
[digest 固定](https://docs.docker.com/reference/cli/docker/image/pull/) 保留内容身份，不代表漏洞状态或未来可用性改善。

- PostgreSQL：`public.ecr.aws/docker/library/postgres:17.6-alpine@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94`
  - `linux/amd64` manifest：`sha256:747d5ed1fdeeb124b880fbe3d7c6557d2c4064ae41d6b6297d417882effce4be`
  - config：`sha256:d741b376874687de90374fd34f55c6b2760e8f7bd7e4ae5cd47f50757fc08cf8`
  - manifest 2,865 字节，config 8,488 字节；10 层压缩数据共 110,612,096 字节
- Go：`public.ecr.aws/docker/library/golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414`
  - `linux/amd64` manifest：`sha256:cd9a32216aee5667f957a62d13a10032a63fd58e14b3f3d9cc8c2122f501e95e`
  - config：`sha256:340a7d186a598086366ea5264e8c6e74e4dee36dc2d70ac0f621b6b28930a786`
  - manifest 1,921 字节，config 2,187 字节；5 层压缩数据共 75,175,309 字节

## 已验证范围

2026-10-09 的独立原始字节复核覆盖 Docker Hub 与 ECR Public 两侧：
两份 index 各 10,293 字节，SHA-256 分别等于上述原固定 digest；各自选中的
`linux/amd64` manifest、config 和全部压缩层逐字节相同，descriptor 长度与 SHA-256 均匹配。
PostgreSQL 10/10 层、Go 5/5 层均通过；两侧压缩层分别流式解压并核对 config 的
`rootfs.diff_ids`，未解包执行镜像。完整内容闭包仅覆盖 `linux/amd64`；其他平台和证明附件
只保留相同 index 中的 descriptor，未逐个下载核验。

复核记录为 `verified-equivalence.json` 与独立实现输出 `independent-mirror-byte-audit.json`
（后者完成时间 `2026-10-09T22:13:38.423590+00:00`），由 PR 证据记录关联。
仓库离线契约 `scripts/test_container_image_pins_contract.py` 仅检查来源、完整 pin 与引用一致性，
不联网验证字节，也不运行容器。

## 配额与剩余门禁

- 截至 2026-10-09，[AWS 公共仓库配额](https://docs.aws.amazon.com/AmazonECR/latest/public/public-service-quotas.html)
  列明匿名下载最大 500 GB/月及 1 次镜像拉取/秒，均不可调整；
  [AWS 计费说明](https://aws.amazon.com/ecr/pricing/) 明确匿名传输按来源 IP 限制，共享 runner 出口仍可能受限
- Docker 上述说明提醒 AWS 外部的匿名访问有限额，通常建议外部客户使用 Hub；
  本次为已观察到的 Hub 拉取限流更换等价官方来源，不保证 ECR 无限额或持续可达
- [AWS 公共仓库说明](https://docs.aws.amazon.com/AmazonECR/latest/public/public-registries.html)
  提醒过期的既有 ECR 登录状态可能影响匿名拉取；本次不更改 Docker 登录或配置

当前本地环境没有 Docker，字节一致性、离线契约与 `make check` 不能替代真实拉取、
Compose/BuildKit、PostgreSQL、四个认证分片及全部 22 个浏览器套件的运行。
必须对发布后的精确 head 完成原有全部 CI，并在独立代码审查、base/head 与依赖复核后才推进合并；
合并后另核对 main 内容树/父提交与 main-push CI。产品验收仍为 **0/209**，不涉及生产部署。

## Go 构建源修复（2026-10-13，当前候选）

上文保留 2026-10-09 的来源调整与历史验证。本次仅将 Go 编译阶段固定为
`docker.io/library/golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414`。
完整 index、`linux/amd64` manifest/config 身份与上文一致；运行阶段仍为 `scratch`，
PostgreSQL 的固定 ECR 来源与共享镜像校验流程不变。没有动态源回退或 Docker 登录。

精确 head `19650a58019eab5eb5d9b70d38c64b89ba0b8543` 的
[CI run 38369355048](https://github.com/yunpiao/adtr/actions/runs/38369355048)
两次 contracts 尝试均已通过 make check 和真实 PostgreSQL/schema 检查，随后在
Compose 构建读取 Go 基镜像 manifest 时收到 ECR `429 / Data limit exceeded`：
[attempt1](https://github.com/yunpiao/adtr/actions/runs/38369355048/job/115202366341)、
[attempt2](https://github.com/yunpiao/adtr/actions/runs/38369355048/job/115216503428)。
两次 lifecycle 均失败，不能写成通过；日志未提供 Retry-After 或额度恢复时点。

2026-10-13 Docker Hub 公开 tag API 返回与上述 pin 相同的 index/amd64 digest；
这是元数据声明核对。本地直接 ECR manifest 请求返回 401 后停止，未取得原始字节，
没有获取或传输 token。2026-10-09 的独立层字节复核仍是历史证据，不是今日重验。

`scripts/verify_go_build_image.py` 在 CI 使用 Docker 原生公共读取与 pull：
原始二进制 stdout 不做 strip 或 JSON 重编码，逐级核验固定 index/amd64 manifest
的 SHA-256、descriptor 长度、config 与五层描述；随后以同一完整 digest 拉取
`linux/amd64`，检查实际 image/config ID、平台与 RepoDigest。
压缩层内容由 Docker pull 的内容寻址校验保证；脚本不声称独立下载重算全部层字节，
不运行镜像、配置登录或自行获取 token。任一步失败均返回非零，不回退、不忽略。

`--evidence-dir` 指定 CI 临时证据目录，用来保留成功核验的原始 index、manifest 和
固定身份摘要；它对脚本运行是必需的，缺少时不执行核验。目录不应包含认证文件。
工作流包装步骤保留脚本的安全分类日志与实际退出码；上传步骤在核验成功或失败后
均保留这份证据，失败仍阻止后续 make check 和 Compose 生命周期。失败目录中的
日志和退出码不是成功身份核验结果，不能替代 identity.json 或真实生命周期通过。
该步骤用于在昂贵检查前确认构建输入身份；删除步骤虽仍保留 Dockerfile 的 digest pin，
但会失去显式平台/config 证据及提前失败诊断。额外元数据请求、拉取与小型证据文件
带来网络/磁盘开销；Docker Hub 也有配额，换官方等价来源不保证永久可用。

离线契约仅验证固定源、字节处理和拒绝边界。当前本地没有 Docker，候选仍须独立
实际 diff 复审和新精确 head 的真实拉取、完整 Compose 生命周期及所有必需 CI。
不更改既有身份/权限/证书校验、超时和产品验收门禁，产品验收保持 0/209。

## PostgreSQL 固定源修复（2026-10-13，PR #153 后续候选）

本节记录当前 PostgreSQL 来源策略；上文保留 2026-10-09 ECR 调整与历史字节复核。
本次将集成、浏览器/LDAP 夹具、Compose 和共享镜像 producer 的唯一固定来源改为
`docker.io/library/postgres:17.6-alpine@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94`。
index、`linux/amd64` manifest `747d5ed1fdeeb124b880fbe3d7c6557d2c4064ae41d6b6297d417882effce4be`
与 config/image ID `d741b376874687de90374fd34f55c6b2760e8f7bd7e4ae5cd47f50757fc08cf8`
均不变。没有动态源选择、fallback、登录、token 或权限变更。

触发证据为 [PR #153](https://github.com/yunpiao/adtr/pull/153) 精确 head
`4a6b5ff9c7b96d394ae04083c0c090c60a9e1744` 的
[run 38387145178 attempt 1](https://github.com/yunpiao/adtr/actions/runs/38387145178)：
[prepare-postgres job 115261627290](https://github.com/yunpiao/adtr/actions/runs/38387145178/job/115261627290)
在 10:59:48、10:59:58、11:00:28 UTC 三次固定 ECR pull 均收到
`toomanyrequests: Data limit exceeded`。原有 10s/30s 有界重试耗尽；日志未给出
额度恢复时间或 Retry-After。该轮 group-members-v4 未运行，没有实际 UI/截图证据；
这次基础设施失败保留，不能记为 UI 断言失败或通过，也未再盲目重跑。

[PR #84 历史记录](https://github.com/yunpiao/adtr/pull/84) 中的同 digest Docker Hub/ECR
原始 index、amd64 manifest/config 和 PostgreSQL 10 层等价复核仍是 2026-10-09 的
历史证据。记录的审计 SHA256 为
`c72cd1c97ac6f8ca8972d743bb158c361077ce0e7af4d7c0d79b99f630af1151`，独立来源复核为
`5f571af182a1703048f5ec9ae03f690974ed013e182f9e1539363c43dbf2e7bf`。
本次没有重新获取两侧完整层字节；今日 Hub tag API 未经可用工具成功读到，因此不声称
当前 Hub 可达、今日原始字节重验或永久免限流。Hub 曾有匿名限流，改固定官方等价源仍有
网络/配额风险；只有新精确 head 的实际 CI 才能证明本次链路可执行。

Docker 可把完整仓库名规范为短名。`prepare_ci_image.py` 仅接受固定 digest 对应的
`docker.io/library/postgres@sha256:...` 和 `postgres@sha256:...` 两个精确 RepoDigest
标识；列表必须非空且每项均属于这两个标识。旧 ECR、其他仓库、额外未知标识、错误
index digest/config/platform 均拒绝。这解决合法 Docker 短名导致的误拒绝，属于身份
规范化，不增加拉取源；去掉短名支持会使正确镜像在部分 Docker 输出下失败，放宽为任意
同 digest 仓库则会失去当前固定来源策略。含额外未知 RepoDigest 的已有缓存会被拒绝，
而不是静默视为当前来源；CI 仍必须明确验证其拉取/缓存身份。

现有 `ci_postgres_image.py` 原始 index/manifest 哈希与长度验证、固定 config 和十层
描述、实际拉取后身份检查、同 run/event/head/base/producer-attempt 的 archive 校验及
消费者 `--pull=never`/Compose `pull_policy: never` 全部保留。工作流、预期 56 jobs、
八个 auth shards、两个 workflow hash、超时、重试与权限不变。该 producer 已是所有
数据库消费者的前置门禁，无需新增工作流步骤或第二套镜像验证器。

离线回归分别验证两合法 alias、旧 ECR/异仓/错误 digest/config/platform 拒绝，明确不同
且断言不等的 source mutation 在 Docker 调用前拒绝，并以独立固定常量检查 prepare、
transfer、runtime/LDAP/browser 和 Compose 的一致性。离线/mock 检查不代表 Docker
实际成功；发布前仍需独立实际 diff 复审，发布后需新精确 head 的共享镜像真实身份产物、
完整 CI、真实 group-members-v4 JSON/六张 PNG 与内容核验。产品验收维持 0/209。
