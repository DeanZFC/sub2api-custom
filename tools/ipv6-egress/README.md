# SmartWS 与账号固定 IPv6

本次仅合并这两个能力，默认不开启。SmartWS 不改变真实首字计时、计费和账号选择。

## SmartWS 开启方式

1. 开启网关 OpenAI WebSocket 总开关、OAuth 支持与 Responses WebSocket V2，账号也启用 Responses WebSocket V2，关闭强制 HTTP。
2. 在「分组管理 → OpenAI 分组 → SmartWS 连接复用」开启分组开关。
3. 符合条件的完整 OAuth 流式 Responses 请求在最终发送前尝试 WS；接口仍返回 SSE。

连接按完整认证头、会话、分组、用户、API Key 和代理地址隔离。没有可信会话标识的连接不跨请求复用。
服务器续接请求（previous_response_id / conversation）、非流式、非标准 Codex Responses 地址不使用此适配器。
票据绑定和独立插件的原有传输逻辑保留；VM Bridge 已退休，旧绑定账号保持停用隔离。票据绑定请求目前不启用 SmartWS，避免混用 SSE/WS 门票。
连接建立失败且未发送生成请求时可以回退原 HTTP；可能已经发送后绝不自动重放。显式上游认证/限流错误保留原处理。
设置使用 revision 防止覆盖并发修改；运行时缓存 1 秒。关闭后在途响应正常结束，不再接收新的 SmartWS 请求。

## IPv6 出口服务

需要 Linux、iproute2、可分配地址且上游可达的公网 IPv6 /64，以及服务商支持的路由/NDP 配置。示例的 2001:db8:: 是文档地址，必须替换。
服务从指定 /64 持久化分配一个 IPv6，通过带独立认证的 CONNECT 代理将其用于该账号。
同账号和影子账号共享出口；不同母账号使用独立租约。只有 IPv6 上游，失败不回退 IPv4 或宿主机直连。

在本目录构建：

    GOTOOLCHAIN=go1.27.3 go build -trimpath -o sub2api-ipv6-egress .

macOS 构建 Linux 产物需指定 GOOS=linux 和目标 GOARCH。将二进制安装到 systemd 单元中的 /usr/local/libexec/sub2api-ipv6-egress。
参考 config.example.json 和 sub2api-ipv6-egress.service：

- 创建专用 sub2api-egress 用户及组；systemd StateDirectory 创建状态目录。
- 将配置保存到 /etc/sub2api-ipv6/config.json，仅服务用户可读（0600），填写真实网卡、/64 和至少 32 字符随机控制密钥。
- 控制端与代理端只监听可信内网地址，使用防火墙限制访问；allowed_clients 必须覆盖 Sub2API 实际源地址。
- allowed_hosts 为精确域名白名单，默认只有 chatgpt.com、auth.openai.com、api.openai.com。图片下载等额外域名须经确认后显式加入。
- 状态文件包含租约密码，0600；与 Sub2API 数据库一起备份。单份状态文件只运行一个服务实例。
- 控制服务是 HTTP；公网使用时须在可信 TLS 入口后部署。SmartWS 当前只支持 HTTP/SOCKS CONNECT 代理；HTTPS 代理保留 HTTP 上游路径。

Sub2API 后端环境变量（全部副本保持一致）：

    OPENAI_IPV6_EGRESS_CONTROL_URL=http://172.18.0.1:18081
    OPENAI_IPV6_EGRESS_PROXY_ADDRESS=http://172.18.0.1:18080
    OPENAI_IPV6_EGRESS_CONTROL_TOKEN_FILE=/run/secrets/ipv6-egress-token

令牌文件只包含与 control_token 一致的密钥，不把密钥填进 URL。Docker 部署需只读挂载该文件；控制服务只接受 Bearer 认证，代理只接受每租约 Basic 认证。

## 账号绑定与恢复

后端启动时新增迁移 350_openai_ipv6_egress.sql；不修改现有账号出口。

1. 在账号管理中将母账号及其全部影子账号设为「停用」，等待在途请求结束。
2. 账号操作菜单 →「固定 IPv6 出口」→「绑定固定 IPv6」。需要管理员二次验证。
3. ready 后再手动启用账号。可同时在分组启用 SmartWS。
4. 要恢复原出口，先停用整个账号家族并排空，再点「解绑并恢复」确认。原代理、备用代理及代理池必须仍有效。

仅支持 OpenAI OAuth 母账号；VM 退休绑定、Agent Identity、Excel BPS 和已有 OVH IPv6 绑定不支持叠加绑定。已有代理池会被保存，解绑时恢复。原影子路由若与母账号不同，须先统一，避免覆盖独立配置。
数据库保护阻止绑定期间的代理修改、池轮转、删除账号及将专用代理分给其他账号。删除账号前必须先解绑。
绑定失败保留预留租约 ID，重试不会重复分配 IPv6。解绑失败保留 delete_failed，继续点解绑重试；失败期间不允许启用账号。
不要在仍有绑定时更换控制服务/代理地址或删除状态文件。重试时检测配置变更；恢复原配置后再解绑迁移。

票据采集跟随固定 IPv6，覆盖全局采集池/换节点设置，固定账号仅使用单路手动采集。其他出口的旧票据不准覆盖绑定出口，也不借用其他账号出口。
绑定后需在此出口重新采集门票；解绑后旧 IPv6 门票不再使用。票据请求保持现有传输，普通符合条件的流式请求可用 SmartWS。

## 验证范围

本地使用合成请求、httptest WebSocket/CONNECT、临时 PostgreSQL/Redis 容器验证，不请求真实 OpenAI 账号。
Linux 地址分配、服务商 IPv6 路由、真实上游连通与性能需要在目标机器部署后验证；本地合并不等于生产安装。
