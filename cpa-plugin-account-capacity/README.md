# CPA Account Capacity

这是一个 CLIProxyAPI 原生动态库插件，按认证账号设置最大并发容量，并提供请求、Token 和费用统计。

## 能力

- 调度时只选择 `active < capacity` 的认证账号。
- 请求经过实际账号选择后绑定账号，在成功、失败、拒绝和取消时释放并发。
- 通过 `usage.handle` 按 CPA 提供的 `AuthID` 累计请求和 Token。
- 管理页面可编辑容量、启用/停用账号、清空统计。
- 状态 JSON 和价格快照原子写入，放在插件挂载目录中，容器更新不会丢失。
- 费用规则参考 CPA Usage Keeper：从 Models.dev 同步模型价格，分别计算普通输入、缓存读取、缓存写入和输出。

## 构建 Linux amd64

插件必须使用与 CPA 匹配的 Go SDK。构建时需要准备 CPA 源码目录，并通过 `go mod edit` 指向它。假设源码目录是 `/tmp/cliproxyapi-reference`：

```bash
cd cpa-plugin-account-capacity
./build-linux.sh
```

脚本会自动准备 `/tmp/cliproxyapi-reference` 中的 CPA 源码并生成 `account-capacity.so`。也可以手动执行：

```bash
git clone --depth=1 https://github.com/router-for-me/CLIProxyAPI.git /tmp/cliproxyapi-reference
go mod edit -replace github.com/router-for-me/CLIProxyAPI/v7=/tmp/cliproxyapi-reference
GOOS=linux GOARCH=amd64 CGO_ENABLED=1 \
  go build -buildmode=c-shared -o account-capacity.so .
rm -f account-capacity.h
```

如果 CPA 源码不在该目录，把 `go mod edit` 命令中的路径替换成实际路径。不要把 macOS 生成的 `.dylib` 放到 Linux CPA 容器中；Linux 必须生成 `.so`。构建完成后可以删除本地 `replace`：

```bash
go mod edit -dropreplace github.com/router-for-me/CLIProxyAPI/v7
```

## CPA 配置

插件目录必须挂载到容器的 `/CLIProxyAPI/plugins`：

```yaml
services:
  cli-proxy-api:
    volumes:
      - ./cliproxyapi/config.yaml:/CLIProxyAPI/config.yaml
      - ./cliproxyapi/auths:/root/.cli-proxy-api
      - ./cliproxyapi/logs:/CLIProxyAPI/logs
      - ./cliproxyapi/plugins:/CLIProxyAPI/plugins
```

在 CPA 的 `config.yaml` 中加入或确认：

```yaml
plugins:
  enabled: true
  dir: "/CLIProxyAPI/plugins"
  configs:
    account-capacity:
      enabled: true
      priority: 100
      state_file: "/CLIProxyAPI/plugins/account-capacity-state.json"
      pricing_file: "/CLIProxyAPI/plugins/account-capacity-pricing.json"
      pricing_url: "https://models.dev/api.json"
      pricing_refresh_hours: 24
      default_capacity: 1
      reject_when_full: true
```

CPA 的 `UsageRecord` 只提供 Token，不提供统一费用字段。插件会读取 Models.dev 的模型价格目录并保存本地快照；模型未匹配到价格时，该请求费用为 0。价格单位与 Keeper 一致，为 USD/百万 Token。

费用计算与 Keeper 一致：普通输入为 `max(input - cache_read - cache_creation, 0)`，再分别乘以普通输入、缓存读取、缓存写入和输出价格。插件启动时加载本地快照，并每小时检查一次，快照超过 `pricing_refresh_hours` 后自动更新。

## Docker 两套安装

分别准备目录并复制同一个 `account-capacity.so`：

```bash
sudo mkdir -p /root/cpa-1/cliproxyapi/plugins /root/cpa-2/cliproxyapi/plugins
sudo cp account-capacity.so /root/cpa-1/cliproxyapi/plugins/
sudo cp account-capacity.so /root/cpa-2/cliproxyapi/plugins/
```

然后分别重启：

```bash
cd /root/cpa-1 && sudo docker compose up -d --force-recreate cli-proxy-api
cd /root/cpa-2 && sudo docker compose up -d --force-recreate cli-proxy-api
```

管理页面在 CPA 管理页面的插件菜单中打开，或直接访问：

```text
/v0/resource/plugins/account-capacity/dashboard
```

页面中的修改操作填写对应这一套 CPA 的 `CPA Management Key`。两套 CPA 使用各自的 `config.yaml`、插件状态文件和管理密钥，互不影响。

## 注意

当 CPA 自身开启 Home 模式时，Home 控制平面会接管认证调度；本插件面向普通单机调度模式。插件更新时不要删除 `account-capacity-state.json`，否则会丢失容量设置和用量统计。
