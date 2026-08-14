# sub2api-overdraft Patch 说明

## Patch 信息

- 文件：`sub2api-overdraft-v0.1.176-fbfdcef81.patch`
- 上游项目：<https://github.com/Wei-Shaw/sub2api>
- Sub2API 基础版本：`0.1.176`
- Git 描述：`v0.1.176-5-gfbfdcef81`
- 基础提交：`fbfdcef8184ae4b2e224d5cfc47cf1d0e3742710`
- 目标提交：`cdf8c1d6597cd5763660268e377f6717b0e84737`
- SHA-256：`60cb661e06406aa323344ccd2fe27fc31024e998148010e3ab987c5f0266bc79`
- 文件大小：158,794 字节

该 Patch 合并了以下两个提交：

```text
e91ba0a8dca3efb65d55e7f4b31597fbe432f658 feat: add Codex quota overdraft support
cdf8c1d6597cd5763660268e377f6717b0e84737 docs: publish sub2api-overdraft fork
```

内容包括 Codex 5h / 7d 额度透支后端、前端显示、测试、配置、源码构建 Compose 文件、品牌和公开部署文档，共修改 41 个文件。

## 在精确基线上应用

先把 Patch 下载到仓库外的临时位置：

```bash
curl -L \
  https://raw.githubusercontent.com/DeanZFC/sub2api-overdraft/codex-overdraft/patches/sub2api-overdraft-v0.1.176-fbfdcef81.patch \
  -o /tmp/sub2api-overdraft.patch
```

在 Sub2API 源码目录执行：

```bash
git switch -c codex-overdraft-patched \
  fbfdcef8184ae4b2e224d5cfc47cf1d0e3742710
git apply --check /tmp/sub2api-overdraft.patch
git apply --3way /tmp/sub2api-overdraft.patch
```

`git apply --check` 没有输出且退出码为 `0`，表示可以干净应用。

## 应用到较新的官方版本

Patch 的精确基线是 `fbfdcef81`。应用到后续 Sub2API 版本时，先创建独立分支，再使用三方合并模式：

```bash
git switch -c codex-overdraft-reapply
git apply --3way /tmp/sub2api-overdraft.patch
```

如果上游修改了相同文件，Git 可能报告冲突。这时应按照根目录的 `CODEX_QUOTA_OVERDRAFT_CUSTOMIZATION.md` 逐项处理，并重新生成 Wire、运行后端和前端测试。不要对有运行数据或未提交修改的服务器目录直接强制应用。

## 完整性验证

本 Patch 已在临时工作树中从基础提交正向应用，并通过反向检查。应用后的 Git tree 为：

```text
a555c6ef94e7e5a151a051c02eb600ee6f26e31a
```

它与目标提交 `cdf8c1d6597cd5763660268e377f6717b0e84737` 的 Git tree 完全一致。
