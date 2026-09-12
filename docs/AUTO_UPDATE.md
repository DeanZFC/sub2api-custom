# 自动更新说明

本 Fork 的自动更新由服务器宿主机更新器执行，网页只负责发起更新请求和显示结果。

## 项目信息

- 仓库：`https://github.com/DeanZFC/sub2api-custom.git`
- 分支：`sub2api-custom`
- 版本文件：仓库根目录 `FORK_VERSION`
- 当前部署方式：Linux Docker + systemd 宿主机更新器

每次发布更新时只需要递增 `FORK_VERSION`，例如 `v0.2.4-custom.31`，提交并推送到 `sub2api-custom` 分支。管理后台会用服务器当前源码目录的远程仓库和该版本文件检查更新。

## 服务器要求

服务器必须能读取 GitHub 仓库。公共仓库可直接使用 HTTPS；私有仓库必须配置有权限的 Deploy Key、SSH Key 或 GitHub Token。仓库可访问性变化后，在服务器执行：

```bash
cd /opt/sub2api-custom-18080
git remote -v
git ls-remote origin
```

`git ls-remote origin` 能返回提交记录，才表示更新器可以检查更新。远程地址建议使用：

```text
git@github.com:DeanZFC/sub2api-custom.git
```

## 网页更新流程

管理员点击页面版本菜单的“立即更新”后，更新器会：

1. 备份 PostgreSQL。
2. 拉取 `sub2api-custom` 分支。
3. 读取根目录 `FORK_VERSION`。
4. 重建源码镜像并重建应用容器。
5. 执行健康检查，失败时恢复更新前源码和应用镜像。

PostgreSQL 和 Redis 数据卷不会被删除。更新器自身在成功后会原子替换，后续版本继续使用同一入口。

## 手动排查

```bash
sudo systemctl status sub2api
sudo journalctl -u sub2api -n 200 --no-pager
sudo systemctl restart sub2api
```

如果网页显示“检查不到更新”，优先检查远程仓库、分支名称、服务器 GitHub 凭据和 `FORK_VERSION` 是否已经提交到远程分支。
