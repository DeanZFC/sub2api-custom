# 固定 IPv6 与新增 OVH 出口的兼容边界

本次在原有固定 IPv6 之外引入 Yoshub 分享版的 OVH 分配器。旧的 `tools/ipv6-egress`、原 `OPENAI_IPV6_EGRESS_*` 配置和现有租约保持原协议。新分配器使用独立进程、配置、状态目录、控制端口和地址段；不自动转换或迁移已有账号出口。

## 来源与许可

来源是用户提供的 `sub2api-source` 分享源码，标记 `0.2.21-yoshub.29`。其 `SHARE_README_CN.md` 自述基准为 `3b7f228947c6c3ecfdb09331792a92b053b622b6`，随后做过分享清理；包内没有 Git 历史，因此该 SHA 不是本次导入文件与原仓库逐字一致的证明。来源包与本仓库的根 LICENSE 均为 LGPL-3.0；保留来源声明，未重新授权。移植范围及本地改动见 `backend/internal/ipv6egress/NOTICE.md`。

## 两套协议

| 项目 | 原固定 IPv6 | 新 OVH provider |
| --- | --- | --- |
| 程序 | `tools/ipv6-egress` | `backend/cmd/ipv6-egress` |
| 配置前缀 | `OPENAI_IPV6_EGRESS_*` | `OVH_IPV6_*` |
| 创建租约 | 调用方提交稳定租约 ID，可重试同一 ID | 分配器递增十进制编号，每次创建消耗新编号 |
| 代理用户名 | `v6-sub2-...` | `ovh6_<序号>` |
| 激活 | 创建接口就绪后返回 | 数据库提交后调用 `/activate`，未认领租约 15 分钟过期 |
| 删除结果 | HTTP 204，失败由同一操作重试 | HTTP 200 JSON，`retiring` 排空后变为 `retired` |
| 状态 | `/health` | `/v1/status` |
| 持久化 | 原状态文件及原租约表 | 独立双计数文件、leases.json 及 OVH 租约表 |

不能将新二进制覆盖旧 daemon，也不能将旧控制 URL 配给新客户端。应用层对两种出口都必须保持固定路由、账号家族独占、禁止代理池轮转和禁止失败回退直连。已从其他出口采集的门票不得用于新固定出口。新建、批量分配、预留、审核 Key 绑定等管理能力由主应用接入，出站 daemon 不接触账号/审核密钥。

两套分配器**不可使用重叠地址段**。旧分配器从 /64 随机选取地址，新分配器采用单调序号；它们没有共享地址账本。也不能让两个独立状态目录管理同一前缀。若拥有 /56，可以在确认服务商实际路由范围后划分互不重叠的子网；不要在一个仍使用完整 /64 的旧实例中直接划出更小子网给新实例。当前内核地址碰撞检查不等于跨实例永久去重。

## 主应用使用边界

账号接入首轮限定原生 OpenAI OAuth 母账号及其继承出口的影子。Excel OAuth/BPS、Agent Identity、API Key、其他平台和仍有 VM 退休/旧 IPv6 绑定的账号不能叠加 OVH 分配；内容审核 Key 通过独立入口使用。此限制避免绕过现有 BPS 的授权与 Mihomo/IP 池；不替换这些传输逻辑。

代理页可开启新地址分配、预领取待绑定地址、查看实际租约及存储统计；开关只控制新分配，关闭不解除已有地址。创建/导入/批量操作须明确选择 OVH 模式，普通 random/least_used 不选取托管地址，公共出口池也排除仍保留绑定的退休 VM 代理。账号更换或删除固定出口前，需要停用母账号与全部影子并核实在途请求为零；修改名称等不改变出口的编辑无需停用。切换到轮转代理池时先解除固定出口，再设置代理池，系统不会静默截成一个普通代理。

OAuth 授权码兑换与 PAT 导入校验沿用原验证代理；`proxy_mode` 用于账号创建后的出口分配。需要验证阶段即使用固定 IPv6 时，可先预领取地址并明确选用该代理。

保留现有 OAuth 账号禁止复制的限制。代理导出与普通详情不返回托管密码，导入不能复用租约；全量备份中的固定出口标记需要显式重新选择出口。账户单独导出维持原有不携带代理引用的格式。

应用为每条新租约保存控制服务 origin。更改 `OVH_IPV6_CONTROL_URL` 后，旧租约的请求与清理会拒绝继续使用不匹配的控制服务，须恢复原配置或先完成旧租约退役。不要用新空状态目录替换同一地址的原 daemon。

## 新分配器配置

参考 `deploy/ovh-ipv6-egress/config.example.json`。该文件的 `2001:db8::/32` 是文档地址，不具备生产连通性；空控制令牌也不会通过校验。

- 使用真实可路由前缀、网卡和私网监听地址，不绑定通配或公网地址。
- `allowed_clients` 必填，只允许 Sub2API 实际连接来源的 CIDR；默认路由 `/0` 被拒绝。应用经过 NAT 时按实际 socket peer 配置，不信任 X-Forwarded-For。
- `allowed_hosts` 必填，为精确小写域名，不支持通配；示例只允许 OpenAI 域名。Grok、TypeSafe 或其他平台需要显式补充实际出站域名；私网/本机/分配地址段/IPv4 目标仍被拒绝。`probe_url` 的域名也必须列入白名单。
- 公网 IPv6 目标只允许 80/443 端口；HTTP 代理和 CONNECT 均校验目标，DNS 解析一次后拨号到已校验 IP，源地址固定为对应租约。
- 控制令牌至少 32 字符，配置文件 0600。控制与代理均要求认证。
- 宿主机只需要 `iproute2`、分配前缀、CAP_NET_ADMIN 和私网可达性，不需要 Docker socket、数据库或上游账号凭据。

应用配置：

```text
OVH_IPV6_CONTROL_URL=http://172.22.0.1:11881
OVH_IPV6_CONTROL_TOKEN_FILE=/run/secrets/ovh-ipv6-control-token
OVH_IPV6_STATE_STATS_DIR=/run/ovh-ipv6-state
```

`OVH_IPV6_CONTROL_TOKEN_FILE` 优先；配置了文件但不可读、超长或内容不合法时，配置失败，不回退到环境中的 Token。兼容旧的 `OVH_IPV6_CONTROL_TOKEN`，仅在没有配置文件路径时使用。客户端 URL 只能是 http/https origin，不能含用户名密码、路径、查询或 fragment，且任何重定向都不跟随。

`ovhipv6.LoadFromEnv()` 返回 `(*Client, error)`，无相关配置返回 `(nil,nil)`；`FromEnv()` 保留可选 provider 的旧签名，缺失或无效配置返回 nil。`Call(ctx,method,path,input,output)` 只接受状态、创建、激活和删除这四类控制请求，响应上限 1 MiB，错误不带令牌或响应正文。

## 部署新 provider 的操作顺序

以下命令是部署参考，本次开发不会执行宿主变更或服务器安装。

1. 核实目标机实际容器/服务、旧 IPv6 daemon 的配置与已用前缀，确定互不重叠的新前缀和独立端口。先保留旧实例运行。
2. 在 `backend` 内交叉构建 Linux 产物，例如 `GOOS=linux GOARCH=amd64 go build -trimpath -o ipv6-egress ./cmd/ipv6-egress`。按目标架构调整 GOARCH。
3. 安装到 `/usr/local/lib/sub2api-ovh-ipv6/ipv6-egress`，配置放在 `/etc/sub2api-ovh-ipv6-egress/config.json`，权限 0600；参考 `sub2api-ovh-ipv6-egress.service` 创建独立服务。
4. 仅对全新状态目录执行一次 `--initialize`。已有或部分已有状态时初始化会拒绝，不能删除旧状态来绕过。
5. 启动新服务，使用只读 `/v1/status` 检查配置。控制端口与代理端口仅允许应用实际来源。
6. 向应用只读挂载控制令牌；按需挂载状态目录以显示空间统计。启用新功能后，用新建测试账号验证分配、激活、请求实际 IPv6、异常拒绝直连和退役排空，再扩大使用。

空间统计只读取三个固定文件的元数据，不读文件正文/代理密码。挂载整个目录，不能挂单个文件（原子 rename 会换 inode）。可使用 `storage-stats-acl.py` 给应用实际 UID 仅目录 traverse 权限；示例 UID 1000 必须按现场确认。脚本和 systemd drop-in 不是运行必需项，不为接入统计重启承载流量的旧服务。

## 状态、退役与恢复

新分配器先持久化 `counter.json` 与 `counter.guard.json` 的高水位，再配置地址；失败也消耗序号。退役立即拒绝新连接，旧 CONNECT 会话自然结束后清理地址及租约记录，已退役序号不再分配。未提交应用事务产生的 pending 租约超时后清理。

两个计数文件和 leases.json 均独立于数据库备份。恢复数据库不能倒退分配器的计数。双计数文件同时丢失/回滚时，不可从现有账号猜测已用序号；应停止新分配并恢复可信最新高水位。新旧 daemon 状态目录不能互换。

新增地址为 `/128`，`preferred_lft 0 valid_lft forever`，保留 DAD，不修改主地址及系统路由。生产验证要分别确认普通未绑定请求的默认出口未变化，以及显式绑定租约的请求使用新 IPv6。

## 审核 Key 的独立 IPv6

OpenAI 与 TypeSafe 的 Key 分开管理，同一字符串在两个引擎中使用不同的绑定标识。开启 `auto_assign_ipv6` 只给之后新增并保存的 Key 分配地址；原有 Key 保持已有出口，也可由完整管理员通过单独分配接口绑定。关闭自动分配不会解除已有绑定。删除 Key 会在同一配置事务中将其地址标记退役；重新添加不会复用已退役地址。

配置写入先锁定当前配置并比较保存前的值，随后将新代理、绑定和配置一起提交；上游激活在提交成功后进行。冲突不会覆盖其他管理员刚保存的绑定。每次最多自动分配 100 个新增 Key，未保存的新增 Key 在自动分配模式下不能直接测试。

已绑定 Key 的调用、轮询、重试和测试都使用自身代理；每次调度检查数据库中的归属、绑定状态、激活确认和控制服务身份，并重新检查代理可用性。任何失败都返回错误，不使用共享代理或直连。未绑定的已有 Key 保持原共享代理/直连路径及其 atomic URL 缓存。现有审核白名单、引擎切换和关键词逻辑保留。

示例出站白名单仅列出 OpenAI 域名。使用 TypeSafe 时必须显式加入 `api.typesafe.ai`（或实际配置的 Base URL 域名），并核实该上游提供可达的公网 IPv6。地址分配成功不代表上游已具备 IPv6 连通性。

## 验证边界

本地单元测试使用 fake 地址管理、合成 HTTP/CONNECT 及临时状态目录。Linux 内核测试需要 `integration` tag、`IPV6_EGRESS_KERNEL_TEST=1`、iproute2 与 CAP_NET_ADMIN，必须放在独立可销毁网络命名空间执行。普通单测不代表服务商 IPv6 路由、NDP、真实上游资格或生产吞吐已验证。

## 2026-11-13 本地验证记录

- `go test -tags=unit ./...`：73 个有测试的包通过，包含现有 SmartWS/BPS/票据/影子/计费相关回归。
- `go test -tags=integration ./internal/repository -count=1 -timeout=10m`：完整仓库集成回归通过，使用临时 PostgreSQL 18.1 和 Redis 8.4。全量验证发现的普通 JSON extra 与延迟触发器兼容问题已修复。
- 新旧 IPv6、审核 Key、实际 AdminService 的专项集成再次通过：涵盖创建、预留绑定、活跃账号同出口编辑、在途请求与影子排空、批量独占换址、事务回滚、控制 origin 变更、退役重试、普通账号 JSON/DDL 兼容。
- 新 daemon/client 及审核 Key 的 race 测试通过；daemon Linux amd64 和主服务本地构建通过。
- 退休 VM 代理按真实绑定从公共池与 random/least_used 排除；真实数据库与原选择逻辑回归通过。
- PAT/OAuth 创建与 Grok SSO 导入补齐代理模式传递，32 个专属回归场景通过；自动选择只影响保存后的账号出口。
- 前端完整测试 494 个文件 / 4289 项通过，lint、i18n、类型检查和 Vite 构建通过。
- 原有 15 个未提交文件逐个 SHA-256 一致；409 个历史 SQL 迁移逐个 SHA-256 一致。仅新增迁移 353 和 354。

以上都是本地验证。未发布版本或镜像，未登录生产服务器，未检查真实 IPv6 出口、服务商路由或上游 IPv6 连通性。
