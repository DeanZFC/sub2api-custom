# OpenAI OAuth 专属槽位管理

本功能为一个 OpenAI OAuth 母账号绑定一个 vm2api 专属槽位，并让该账号的推理和辅助请求经过受管理的路径。Sub2API 保留账号凭据、OAuth 刷新、调度、响应解析和计费；独立部署的 `vm2api-bridge` 管理外部槽位、单槽位 API key 和账号级 CONNECT 代理。

它由 **Sub2API 可选内置适配器 + 独立 bridge + vm2api 控制面/运行时**组成，不是 `.s2plugin` 插件包，不能通过插件市场上传或卸载。默认未启用；没有绑定的账号继续使用现有路由。

## 适用范围与实际隔离边界

- 仅支持带 bearer 凭据的 **OpenAI OAuth 母账号**。API Key 账号、其他平台、Agent Identity 账号和影子账号不能独立创建槽位。
- 已有或后续创建的 Spark 影子账号共享母账号的绑定和受管路由；影子账号不是第二个独立槽位。创建、重试和解绑均从母账号操作。
- `docker` 与 `kvm` 是提交给 vm2api 的运行时类型，是否可用取决于目标宿主机及 vm2api 的实际创建、启动结果。
- vm2api 的 OpenAI `kin-codex-kernel` 当前在运行时**宿主机**执行。槽位不等于在独立虚拟机里运行官方 Codex CLI，也不保证推理进程具有虚拟机级隔离。
- 一个槽位不自动获得一个独立公网 IP。多个槽位可共享宿主机出口或同一上游代理出口；公网 IP 的分配与隔离需由部署网络和代理提供。

### 区分账号上游代理与 Bridge 地址

| 配置 | 含义 | 当前支持 |
| --- | --- | --- |
| 账号原上游代理 | bridge 与 vm2api 最终访问 OpenAI 的出口 | 明确直连，或单个 `socks5://` / `socks5h://` 代理 |
| Bridge 管理地址 | Sub2API 管理槽位和转发 Responses 的入口 | `http://` 或 `https://` origin |
| Bridge 代理地址 | Sub2API 的 OAuth 刷新及辅助请求使用的账号级 CONNECT 入口 | `http://` 或 `https://` origin；参考 bridge 的同一监听器 |

原账号的 HTTP/HTTPS 上游代理和多代理池不受支持，创建会被拒绝，不会静默换成直连或另一种代理。原上游代理必须有效、未过期，并可从运行时所在网络访问。

如果原账号是直连，绑定后的直连出口是 **bridge / vm2api 执行宿主机的出口**，未必等于绑定前 Sub2API 所在服务器的公网 IP。bridge 应与 vm2api 的实际执行宿主机具有相同出口；尤其不能将二者部署到不同出口的服务器后仍使用空上游代理。

### CONNECT 白名单

账号级 CONNECT 仅允许下列主机的 **443** 端口：

- `api.openai.com`
- `auth.openai.com`
- `chatgpt.com`
- `chat.openai.com`
- `platform.openai.com`

不支持 IP 地址、通配域名、其他端口或普通 HTTP 通用转发。**图片 CDN 不在此固定白名单内时，图片下载或 Base64 回填会失败**；不能因为主 Responses 请求成功就认定图片、文件等所有辅助路径都可用。失败不会自动绕过 bridge 走原代理或本机直连。需要其他目标域名时，应先审查并修改 bridge 白名单和对应测试，再按正常发布流程部署。

## 部署与全局配置

### 1. 准备独立 bridge

同机 Linux amd64 完整容器部署可直接使用 [`deploy/openai-vm/README.md`](../deploy/openai-vm/README.md)：固定 vm2api `v1.3.225` 镜像及 digest，配套 bridge 镜像、随机密钥初始化、持久目录和只读检查。控制面仅监听回环；现有 Sub2API 在 Docker bridge 网络时，可将 bridge 显式绑定到该网络宿主机网关。此方案使用独立 Compose，不修改主站 Compose；没有 `/dev/kvm` 的主机选择 Docker 槽位。

按 [`backend/cmd/vm2api-bridge/README.md`](../backend/cmd/vm2api-bridge/README.md) 构建、部署和配置进程。该文档列出了全部环境变量、TLS、状态持久化和接口契约。核心配置包括：

- `VM2API_URL`：vm2api 控制面地址。
- `VM2API_MASTER_KEY`：仅供 bridge 管理 vm2api，不能作为 Sub2API 的推理凭据。
- `VM2API_BRIDGE_SECRET`：至少 32 字节、无首尾空白的随机共享密钥；Sub2API 使用相同值。
- `VM2API_BRIDGE_LISTEN`：默认 `127.0.0.1:8097`，管理接口与 CONNECT 共用监听器。
- `VM2API_BRIDGE_STATE_DIR`：稳定的持久化目录，生产环境使用绝对路径。
- `VM2API_BRIDGE_NAMESPACE`：一个 Sub2API 安装独有且保持稳定的 namespace。

Sub2API 和 bridge 不需要 Docker socket；由 vm2api 控制面按其部署要求访问 Docker/KVM。每个 bridge 状态目录只运行一个进程，当前不支持 active-active 多副本。

跨不可信网络使用 bridge 原生 TLS，并将网络访问范围限制为 Sub2API。使用其他 TLS 入口时，入口必须能承载 HTTP/1.1 CONNECT 原始隧道；普通网站反向代理规则不一定具备此能力。

共享密钥、namespace 和状态目录属于一套恢复所需的配置。bridge 状态通过共享密钥派生密钥加密，包含槽位专属 key 和上游代理凭据。没有在线密钥轮换或 namespace 迁移工具；有资源时不能直接更换密钥、namespace，或删除状态目录。

### 2. 在 Sub2API 保存连接设置

管理员进入 **账号管理 → OpenAI OAuth 母账号操作菜单 → 专属槽位**，在“全局 Bridge 配置”中设置：

1. 勾选“启用专属槽位”。
2. 填写“Bridge 管理地址”和“Bridge 代理地址”。使用参考 bridge 时，两者通常填写同一个监听 origin，例如同机部署的 `http://127.0.0.1:8097`，或 TLS 部署的 `https://bridge.example.com:8097`。
3. 填写与 bridge 一致的“Bridge 管理密钥”，保存，并按提示完成管理员二次验证。
4. 点击“测试 Bridge 健康状态”。

地址只接受 origin，不含用户名、密码、业务路径、查询参数或 fragment。公开地址必须使用 HTTPS；HTTP 仅接受 localhost、回环或私有 IP。地址必须从 **Sub2API 后端进程所在网络**可达；容器内的 `127.0.0.1` 指向容器自身。

管理密钥不会回显。之后留空保存表示**保留已有密钥**，不表示清空。全局设置影响全部专属槽位；只要存在任意绑定，包括创建失败或清理失败的绑定，就不能修改连接地址、密钥或启用状态。保存完全相同的配置不会改变绑定。

测试只验证已保存配置对 bridge 鉴权健康接口的可达性和认证，不创建槽位、不执行 OpenAI 推理，也不证明 vm2api 控制面、运行时、账号上游代理或 OpenAI 出口已可用。未保存的设置需先保存才能测试或创建。

## 为账号创建槽位

1. 确认母账号是正常的 OpenAI OAuth 账号，存在可用 access token 和 `chatgpt_account_id`。缺少身份 ID 时先刷新或重新授权账号。
2. 确认原路由是明确直连，或只有一个有效的 SOCKS5/SOCKS5H 代理；多代理池需先在未绑定状态下整理为一个出口。
3. 保存并启用完整的全局配置。选择 Docker 或 KVM，点击“创建专属槽位”，完成二次验证。
4. 等待界面刷新，只有重新读取到绑定且状态为“已就绪”才表示创建确认完成。记录当前账号、槽位 ID 和运行时，便于后续排查。
5. 在目标环境验证正常推理、流式取消、OAuth 刷新、用量查询及需要的辅助功能。真实账号验证可能产生实际请求和计费，应按本地运维流程执行。

创建先在 Sub2API 保存原代理、原代理池及回退来源信息，再为母账号及其影子账号安装账号级受管 CONNECT 代理。外部槽位只拿到 access token 和固定 ChatGPT 账号身份；**refresh token 始终由 Sub2API 持有和刷新，不提交给 bridge 管理接口**。bridge 自身不把 access token 写入状态文件，但 access token 会交给 vm2api，由 vm2api 按其实现保存和使用。

推理使用绑定槽位的专属 API key，不使用 vm2api master key。HTTP 和原生 WebSocket Responses 均经过 bridge 的账号专属槽位，Sub2API 继续处理响应、流和用量。compact 请求也转给 vm2api 的对应路径；固定上游版本不支持时保留其 404，不会改走 CONNECT 绕开槽位。OAuth 刷新、用量和其他辅助请求通过该账号的 CONNECT 凭据访问允许的目标。

有绑定时，账号及影子账号的路由、归属、平台/类型、删除，以及受管代理的关键参数受到保护。需要改变这些内容时先解绑；普通账号备注和正常凭据刷新不等于解绑。

## 状态、失败和重试

| 状态 | 含义 | 管理员操作 |
| --- | --- | --- |
| `provisioning` / 创建中 | 已保留绑定，尚未确认外部槽位就绪；也可能是进程中断留下的状态 | 先刷新；确认前次操作已结束后“重试创建槽位”，或解绑清理 |
| `ready` / 已就绪 | 已确认专属槽位；状态读取仍会核验 bridge 可用性 | 正常使用；需要移除时解绑 |
| `error` / 创建失败 | 创建或状态核验失败，界面显示实际错误 | 修复原因后重试创建，或解绑清理 |
| `deleting` / 清理中 | 已开始阻断新流量并清理外部资源 | 等待活动请求结束后刷新或“重试清理槽位” |
| `delete_failed` / 清理失败 | 外部清理或原路由恢复尚未完成 | 保留绑定，修复原因后重试清理 |

绑定存在但未就绪时，不会自动回退到原代理或本机直连。请求超时也不代表没有创建资源；界面会尝试重新读取绑定并保留原操作错误。先刷新状态，不要另建重复账号或手工删除数据库行来“解锁”。

创建重试复用原绑定的身份、上游代理和运行时，不创建另一种配置的槽位。要改变运行时或上游出口，先完成解绑再重新配置。清理重试继续完成删除和原路由恢复，即使配置表单有未保存的编辑，也仍可执行清理重试。

常见情况：

- **全局配置被锁定**：检查所有绑定，创建失败和清理失败同样需要解绑。停止账号调度不等于解除绑定。
- **Bridge 认证、证书或连通失败**：检查现有部署与保存的配置是否一致，恢复原监听地址、证书信任和原共享密钥。不要在仍有绑定时直接改 namespace 或密钥。
- **运行时或上游代理失败**：检查 vm2api 的实际 Docker/KVM 能力、原 SOCKS5 代理状态及可达性。健康检查通过不能排除这些失败。
- **缺少账号身份或凭据**：按现有 OAuth 流程修复；不要将 refresh token 放进 bridge 配置或工单。
- **提示操作正在执行或绑定已改变**：等待当前操作结束并刷新，使用最新绑定状态重试。
- **图片 CDN 被拒绝**：核对 CONNECT 白名单边界；不要通过账号直连回退规避失败。

界面单次 API 请求超时为 120 秒。bridge 默认清理等待时间为 30 秒；调整 bridge 超时应同步评估 Sub2API 和入口请求超时，避免只提高一侧超时后造成反复中断。

## 解绑、恢复原路由

1. 从母账号打开“专属槽位”，点击“解绑并销毁槽位”，阅读并确认。
2. bridge 将绑定置于删除状态，阻断新的推理和 CONNECT，等待现有请求与隧道排空。
3. bridge 撤销该槽位专属 key，停止并删除对应槽位，确认外部资源消失后移除 bridge 本地绑定。
4. Sub2API 在事务中恢复母账号及其影子账号的原代理、原代理池及回退来源，移除受管标记和绑定，停用并软删除专属受管代理，并刷新调度快照。
5. 界面重新读取到“尚未绑定专属槽位”，且显示原路由已恢复后，再核对账号代理和正常请求路径。

原代理必须仍然存在、有效且未过期。若提示原代理不可用，先恢复原代理的有效状态，再重试解绑；系统不会将无法恢复的原代理静默替换成直连。

已有 CONNECT 可能来自 HTTP 连接池，只有隧道关闭才释放租约。清理超时或 `account_busy` 时，新请求继续被阻断，已有资源与绑定保留；等待调用端连接池释放后重试。必要时停止该账号的新调度并等待连接排空，但不能把停止调度当作清理完成。

若外部资源已删除而 Sub2API 原路由恢复失败，继续使用“重试清理槽位”，让幂等删除和本地恢复完成。清理只针对当前绑定槽位及其专属 key，不应停止全部 vm2api，也不删除可能被其他槽位共享的上游代理记录。

## 停用、迁移部署与旧版本回滚

顺序必须是：**全部解绑并确认原路由恢复 → 禁用全局配置 → 停止或移除 bridge**。

1. 保持当前适配器、bridge、vm2api 及其密钥和状态目录可用，逐个清理全部绑定，包括失败状态。
2. 核对绑定数量为 0，并检查没有遗留受管标记或仍使用受管代理的账号。可以在授权的数据库会话中进行以下只读检查；不要把其中任何一行改成直接删除绑定的 SQL：

   ```sql
   SELECT account_id, slot_id, status, updated_at
   FROM openai_vm_bindings
   ORDER BY account_id;

   SELECT id, parent_account_id, proxy_id
   FROM accounts
   WHERE deleted_at IS NULL
     AND extra->>'openai_vm_bound' = 'true'
   ORDER BY id;
   ```

3. 在界面取消“启用专属槽位”并保存。禁用不会清空已保存的共享密钥；密钥输入留空仍表示保留。
4. 核对 vm2api 中相应槽位及专属 key 已回收，确认母账号和影子账号使用恢复后的原路由。再停止 bridge，按凭据和备份管理要求处理其服务定义、环境文件及状态目录。
5. 如需换宿主机、管理地址、共享密钥、namespace 或运行时，在全部解绑后完成新部署及配置，再逐账号创建。

**回滚到不支持本功能的旧 Sub2API 二进制，也必须先完成全部解绑和原路由恢复。** 旧二进制不了解绑定状态和 bridge 路由约束，不能用替换二进制来代替解绑。回滚前另行验证数据库兼容性并保留备份；本功能没有自动降级迁移流程。

仅停止 bridge、删除可执行文件、删除本地状态目录或手工删除 `openai_vm_bindings` 都不是解绑，会遗留外部资源、专属 key 或受管代理路由。bridge 收到退出信号不会自动销毁槽位；保留相同密钥、namespace 和状态目录重启后，可继续重试清理。

## 从源码移除适配器

源码移除用于维护自有构建，不是管理员界面的卸载按钮。先完成上一节的运行中资源清理和停用；**不能先删代码，再尝试解绑**。在独立提交中只移除本功能，保留其他并行功能的改动。

| 范围 | 主要文件或位置 | 移除时要点 |
| --- | --- | --- |
| 独立 bridge 与客户端 | `backend/cmd/vm2api-bridge/`、`backend/internal/pkg/vmbridge/`、`backend/internal/pkg/vm2api/` | 确认没有资源依赖后移除相应命令、包和专属测试 |
| 生命周期与持久化 | `backend/internal/service/openai_vm_service.go`、`openai_vm_types.go`、`backend/internal/repository/openai_vm_repo.go` 及专属测试 | 移除专属接口与实现，同时处理被其他服务调用的代理校验/解析 helper |
| 管理 API 与依赖注入 | `backend/internal/handler/admin/openai_vm_handler.go`、`handler.go`、各层 `wire.go`、`backend/internal/server/routes/admin.go` | 移除 handler、路由和 provider，再按仓库流程重新生成 `backend/cmd/server/wire_gen.go` |
| 请求链集成 | OpenAI gateway、HTTP/plugin transport、WebSocket ingress、测试、用量、模型、配额、图片回填及 OAuth/token refresh 服务 | 仅撤去 VM 适配器分支和受管代理限制，恢复各路径原有代理解析；保留现有插件和其他平台逻辑 |
| 管理界面 | `frontend/src/api/admin/openaiVm.ts`、`OpenAIVMModal.vue`、账号操作菜单、`AccountsView.vue`、中英文账号文案及对应测试 | 同步移除入口、事件、状态、API 和专属测试；不要删除共享账号测试文件中的其他用例 |
| 数据库 | `backend/migrations/308_openai_vm_bindings.sql` 引入的配置表、绑定表及保护触发器/函数 | 已应用迁移不能靠删除或改写历史文件撤销；默认保留空表与历史迁移 |

可先用以下命令定位当前代码引用，最终以实际引用和编译结果为准：

```sh
rg -n 'OpenAIVM|openaiVM|openaiVm|openai_vm|openai-vm|vm2api|vmbridge|resolveOpenAIAccountProxyURL|validateOpenAIVMProxy' backend frontend/src docs
```

如果必须清理数据库对象，应在备份及确认 **无绑定、无受管标记、无受管路由残留** 后，新增独立、可审查的前向迁移：先处理 `accounts`、`account_proxies`、`proxies` 上的专属保护触发器与函数，再删除专属表。保留已应用的迁移记录，不在运行中的旧迁移文件上直接删表或改校验内容。数据库清理与源码删除应分阶段验证，不能把清理 SQL 当作外部槽位销毁工具。

移除后按仓库流程重新生成依赖注入代码，运行受影响后端测试、前端类型检查和构建，并验证未绑定 OpenAI OAuth、Spark 影子账号和其他平台仍使用原有路由。部署或回滚是否完成，以目标环境的实际二进制和路由核验为准。

## 管理 API 参考

以下路径均位于 `/api/v1` 下，需要管理员认证；保存、测试、创建、重试和删除还要求现有管理员 step-up 验证。

| 方法 / 路径 | 用途 |
| --- | --- |
| `GET /admin/openai-vm/config` | 读取全局配置，只返回 `secret_configured`，不回显密钥 |
| `PUT /admin/openai-vm/config` | 保存 `enabled`、`base_url`、`proxy_url` 和可选 `secret` |
| `POST /admin/openai-vm/test` | 鉴权健康检查，不创建资源或执行推理 |
| `GET /admin/accounts/:id/vm` | 读取 `binding`、`eligible`、`configured` |
| `POST /admin/accounts/:id/vm` | 使用 `{"runtime_type":"docker"}` 或 `{"runtime_type":"kvm"}` 创建 |
| `POST /admin/accounts/:id/vm/retry` | 按现有绑定重试创建或清理；清理完成时绑定返回 `null` |
| `DELETE /admin/accounts/:id/vm` | 清理外部资源并恢复原路由 |

API 返回成功后仍应重新读取账号状态确认最终结果。创建以有效且 `ready` 的绑定为准，清理以绑定不存在且原路由恢复为准，不能仅凭某次 HTTP 响应、停止进程或日志中的单条成功记录判断整个生命周期已经完成。
