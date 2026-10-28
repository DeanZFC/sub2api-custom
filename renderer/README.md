# 鹈鹕自动评审渲染器

渲染器把生成的 HTML/SVG 在独立 Chromium 中运行，返回 960×720 的四张 PNG（约第 0、0.7、1.7、2.9 秒，避免整秒采样与固定周期动画重合），供可信视觉裁判评审。主服务不安装浏览器、不运行生成的 JavaScript。

## Docker 部署

在仓库根目录运行；已有部署应保留原 compose 文件和项目名，只追加最后一个 override：

```sh
docker compose -f deploy/docker-compose.local.yml -f deploy/docker-compose.custom.yml \
  -f deploy/docker-compose.review-renderer.yml up -d --build
```

这里会构建渲染器镜像，并通过专用 named volume 共享 UNIX socket；没有新增网站端口。主服务用户与 `pwuser` 都是 UID 1000。若自定义了主服务 UID，需同步调整 socket 用户或组，不能把 socket 对所有用户开放。

渲染器容器没有 IP 网络，根文件系统只读，不挂载业务数据或凭据，仅保留 Chromium namespace sandbox 需要的 `SYS_CHROOT` capability，并限制 CPU、内存和进程数。`seccomp.json` 使用 [Playwright v1.56.1 官方 Docker 示例配置](https://github.com/microsoft/playwright/blob/v1.56.1/utils/docker/seccomp_profile.json)，允许 Chromium sandbox 所需的 namespace 系统调用。宿主机必须允许非特权 user namespace；如果 Chromium 启动失败，应检查宿主限制，不能改成 `--no-sandbox`。

隔离浏览器额外阻断网络请求、WebSocket、service worker、弹窗、下载和页面跳转，CSP 只允许内联脚本/样式及嵌入图片/字体。渲染使用新浏览器实例，每次最多 20 秒、单并发。超时、浏览器失败或资源超限返回错误，主服务应显示待重试，不能算账号智力失败。主服务本地串行排队，其他服务实例同时访问可能收到忙碌错误。

## 非 Docker 主服务

仍使用独立容器渲染，向主服务挂载同一个 socket，并设置：

```sh
SCHEDULED_TEST_RENDERER_SOCKET=/path/to/renderer.sock
```

不要将渲染器放到主服务容器、使用 host 网络、挂载 Docker socket 或复用管理员浏览器。

## 检查

```sh
cd renderer
npm ci --ignore-scripts
npm test
```

单元测试覆盖协议、大小和并发限制。实际浏览器集成测试在镜像中运行：

```sh
docker compose -f deploy/docker-compose.local.yml -f deploy/docker-compose.custom.yml \
  -f deploy/docker-compose.review-renderer.yml build review-renderer
docker compose -f deploy/docker-compose.local.yml -f deploy/docker-compose.custom.yml \
  -f deploy/docker-compose.review-renderer.yml run --rm --no-deps review-renderer \
  npm run test:integration
```

该检查只渲染本地动画，不调用付费模型。
