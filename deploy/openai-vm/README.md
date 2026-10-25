# 可选 OpenAI VM 容器部署

此目录部署独立的 **vm2api 完整控制面容器 + Sub2API bridge 容器**，不改现有 Sub2API、PostgreSQL、Redis 或主 Compose。只面向同一台 **Linux amd64、rootful Docker Engine、Docker Compose v2+、Python 3.9+** 主机。macOS/Windows Docker Desktop、ARM 仿真和远程 Docker 不作为生产目标。

vm2api 固定为 `ghcr.io/dofastted/vm2api:v1.3.225` 的 Linux amd64 manifest：

```text
sha256:420b5292047751e2b412b694176c71987320ccb132e09e3b51e0a11e8f19e23e
```

该 digest 与 `v1.3.225` 标签已于 2026-10-25 从 GHCR manifest 核对。使用完整预构建控制面镜像，包括上游 release payload；不会把只有源码、缺少 `kin-codex-kernel` 等 payload 的目录误当作完整运行时。bridge 从当前 Sub2API 源码构建，其构建上下文只包含 bridge 及相关 Go 包，不打包 `.env`、账号状态或主站数据。

## 部署边界

- 两个容器使用同一宿主机 host network，使本地直连出口一致。每个槽位仍可能共享公网 IP；这不是独立公网 IP 或官方 Codex CLI 虚拟机隔离保证。
- vm2api 控制面固定监听 `127.0.0.1:8787`，不发布公网端口，不配置 Docker TCP 端口；管理台应通过受控 SSH 隧道使用。
- 仅 vm2api 控制面持有 `/var/run/docker.sock` 和上游要求的 `NET_ADMIN` / `NET_RAW`。它具有管理本机容器、槽位和网络的高权限，应部署在可信主机；不要开放控制面给公网。
- bridge 使用非 root UID/GID、只读根文件系统、移除所有 capabilities、启用 `no-new-privileges`，没有 Docker socket。
- bridge 默认 `127.0.0.1:8097`；现有 Sub2API 在 Docker bridge 网络时，初始化时显式选择该网络宿主机网关，例如 `172.18.0.1:8097`。禁止 `0.0.0.0`、公网或任意主机名监听。
- 此模板只使用同机回环/RFC1918 的 HTTP。跨主机或跨不可信网络部署应按 [bridge README](../../backend/cmd/vm2api-bridge/README.md) 单独配置 HTTPS/CONNECT 入口和访问规则，不放宽本模板为公网明文。
- KVM 需要实际 `/dev/kvm`；没有 KVM 的主机应在 Sub2API 选择 Docker。本模板不会开启软件仿真或伪造 KVM 可用状态。

## 1. 初始化一次

在保留当前 bridge 源码的仓库副本执行。以下示例使用独立数据目录，且将 bridge 绑定到已确认属于主站 Docker 网络的宿主机地址：

```sh
sudo ./deploy/openai-vm/init.sh \
  --data-dir /opt/sub2api-openai-vm/state \
  --listen 172.18.0.1:8097
sudo ./deploy/openai-vm/check.sh
```

原生或 host-network Sub2API 可省略 `--listen` 使用回环。`172.18.0.1` 仅为例子，必须以当前 Sub2API 容器网络网关为准；不要将其他站点的 Docker 网络误认为主站网络。检查现有防火墙是否允许该容器来源到该地址即可，本脚本不改防火墙，也不发布公网端口。绑定私网地址限制监听接口，并不代替来源访问控制。

可用参数：

```text
--env-file /absolute/path/.env    默认本目录 .env
--data-dir /absolute/path/state  默认 .env 同级 state
--listen 172.18.0.1:8097          默认 127.0.0.1:8097
--vm2api-port 8787               控制面仍固定绑定 127.0.0.1
--project sub2api-openai-vm       独立 Compose 项目名
```

初始化生成互不相同的随机 master key、数据库密钥、管理密码、bridge 共享密钥和稳定 namespace，保存为权限 `0600` 的 `.env`。默认 root 初始化时 bridge 使用 `10001:10001`，普通用户初始化时使用该用户的非 root UID/GID；状态目录归属与之对应，权限 `0700`。

**再次执行保留现有密钥、namespace、路径和监听配置**。若传入不同配置会退出，不会轮换密钥或覆盖现有账号状态。切勿在已有绑定时删除 `.env` 后重新初始化。密钥不打印到终端；需要填写 Sub2API 时在受控本地编辑器读取 `.env` 的 `VM2API_BRIDGE_SECRET`，不要粘贴到日志、工单或聊天。

目录布局：

```text
state/
  vm2api/vms/          槽位定义、凭据和运行目录
  vm2api/data/         上游数据库与运行数据
  vm2api/bin/          镜像入口写入的版本配套 payload
  vm2api/share/        wrapper / payload 共享文件
  vm2api/src/config/   上游可编辑配置
  bridge/state/       bridge 加密状态与单进程锁
```

这套数据属于同一个安装。备份时一起保留 `.env`、全部持久目录和 Sub2API 数据库；`.env` 中数据库/bridge 密钥与各自状态相互依赖。若已有数据权限或归属不匹配，检查脚本会报错，不递归改权或删除数据。

## 2. 拉取、构建并启动

`init.sh` 和 `check.sh` 都不会启动服务。确认预检通过后执行：

```sh
sudo ./deploy/openai-vm/compose.sh pull vm2api
sudo ./deploy/openai-vm/compose.sh build bridge
sudo ./deploy/openai-vm/compose.sh up -d --no-build
sudo ./deploy/openai-vm/check.sh --running
sudo ./deploy/openai-vm/compose.sh ps
```

`compose.sh` 始终选择本目录的独立 Compose 和已保存 `.env`，并去除可能覆盖这些设置的同名 shell 环境变量。若使用自定义 `.env`，每个命令均在 Compose 子命令之前提供 `--env-file /absolute/path/.env`。不要运行会输出展开密钥的 `compose.sh config`；检查脚本只在内存解析 Compose 配置并输出结论。

拉取 vm2api 与 bridge 构建需要访问镜像仓库；上游入口还可能拉取配套槽位镜像。创建槽位前应确认相关镜像可用，不能把两个容器启动成功等同于 Docker/KVM 槽位创建成功。

检查模式：

- `check.sh --static`：核对 `.env`、目录/属主、固定镜像、Compose 网络与权限，不访问 Docker daemon 或 HTTP。适合本地检查。
- `check.sh`：在 Linux amd64 部署主机上检查本机 rootful Docker、socket、目录和监听地址/端口可用性；服务启动后端口已占用，应改用下一模式。
- `check.sh --running`：检查主机条件、vm2api `/healthz`、bridge 鉴权 `/v1/health`。只读，不创建槽位或发起 OpenAI 推理。

## 3. 连接现有 Sub2API

从现有主站后端容器确认 `http://172.18.0.1:8097/health` 可达，再在母账号的“专属槽位”全局配置填写：

| 字段 | 示例 |
| --- | --- |
| Bridge 管理地址 | `http://172.18.0.1:8097` |
| Bridge 代理地址 | 同一个 `http://172.18.0.1:8097` |
| Bridge 管理密钥 | `.env` 的 `VM2API_BRIDGE_SECRET` |

这里不能填写 vm2api 控制面的 `127.0.0.1:8787`，也不能填写 vm2api master key。Docker bridge 网络内的 `127.0.0.1` 指向 Sub2API 容器自身；必须使用初始化时选择的宿主机可达地址。两个服务的健康检查不能替代从 Sub2API 后端网络核验可达性。

保存后进行管理健康测试，再选择一个测试 OAuth 母账号验证 Docker 槽位创建、HTTP / WebSocket Responses、流式取消、OAuth 刷新、用量及解绑恢复。不要在没有 KVM 的主机选择 KVM。compact 会转给 vm2api 的专属槽位路径；固定上游版本不支持时返回其 404，不会通过 CONNECT 绕开槽位。真实账号测试会产生实际外部请求和可能的费用。完整生命周期与白名单限制见 [管理说明](../../docs/openai-vm.md)。

## 4. 更新、停用与恢复

- vm2api 更新需要显式审查新版本与 payload/API 兼容性，然后同步修改 Compose 和 `manage.py` 的固定 digest；不会自动跟随 `latest`。
- bridge 更新先构建，核验后再 `compose.sh up -d --no-build bridge`；任何重启都需要安排在活动请求可中断的窗口，并保留旧镜像作为恢复点。
- 停用前先从 Sub2API **逐个解绑并确认原路由恢复**，再禁用全局配置，最后执行 `compose.sh down`。不要使用 `down -v` 或删除持久目录来代替解绑。
- `down` 只移除这套 Compose 服务；vm2api 经宿主机 Docker 创建的兄弟槽位容器不属于 Compose，必须由解绑回收。
- 出错时保留 `.env`、namespace 和数据，按原配置恢复服务并重试清理。旧 Sub2API 二进制回滚也必须先清完全部绑定。
- 无账号绑定的预部署可以单独停止服务；主站 Compose 与数据库不受本目录脚本改写。

## 本地静态验证

```sh
python3 deploy/openai-vm/test_config.py
sh -n deploy/openai-vm/init.sh deploy/openai-vm/check.sh deploy/openai-vm/compose.sh
```

测试使用临时目录和临时随机值，覆盖重复初始化、密钥不输出、不覆盖配置、监听限制、私有目录、固定 digest、无公网控制面、bridge 无 socket，以及环境变量覆盖防护。它会调用 Docker Compose 的配置解析，不启动容器、不拉镜像，也不触及真实账号。
