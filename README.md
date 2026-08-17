<div align="center">

# OpenList Sync

**双向同步 OpenList(AList 分支)远端目录与本地目录,集成 Web 管理界面与定时调度。**

[快速开始](#快速开始) · [功能特性](#功能特性) · [配置](#配置) · [使用说明](#使用说明) · [构建](#构建)

---

</div>

## 功能特性

| 能力 | 说明 |
| --- | --- |
| Web 管理界面 | 连接、任务、全局设置在浏览器中可视化配置,实时查看同步日志 |
| 三种同步方向 | 双向同步、只下载(远端 → 本地)、只上传(本地 → 远端) |
| 增量同步 | 按文件大小与修改时间比对,只传输差异文件 |
| 断点续传 | 下载中断后从 `.part` 断点继续,完成后自动改名 |
| 传输控制 | 并行传输、全局限速、失败自动重试 |
| 文件筛选 | 按文件类型(视频/音频/图片/文本)或扩展名 glob 包含/排除 |
| 清理模式 | 对侧已删除的文件,可选同步删除本地/远端/两侧 |
| 冲突策略 | 保留较新的、远端优先、本地优先、跳过 |
| 定时调度 | 每个任务独立设置同步间隔,或手动触发运行 |
| 多任务 | Web 界面可视化配置,或环境变量批量配置 |

## 快速开始

### Docker Compose(推荐)

```bash
docker compose up -d
# 打开 http://localhost:18222
```

数据与配置统一存放在宿主机 `./data` 目录(配置文件为 `./data/openlist-sync.json`,任务同步目录默认也是 `/data/backup` 之类的容器内路径),重建容器不丢失。容器启动时会自动将该目录属主修正为容器内用户,无需手动 chown。

> **安全默认**:Web 界面默认仅绑定本机(compose 端口映射 `127.0.0.1:18222`;二进制默认监听 `127.0.0.1`),不配 token 也不对外暴露。只需本机访问则保持默认即可;需局域网访问时把端口映射改为 `"18222:18222"`;**需公网暴露时务必设置访问令牌**:

```bash
cp .env.example .env   # 编辑 .env 填入 WEB_API_TOKEN
```

### 二进制

```bash
# ①Web 管理界面(内置调度器)
openlist-sync web --store ./openlist-sync.json

# ②手动同步一次
openlist-sync sync \
  --base-url http://192.168.1.10:5244 \
  --token your-admin-token \
  --remote-path /movies \
  --local-dir ./data/movies

# ③常驻守护,每 6 小时同步一次
openlist-sync daemon \
  --base-url http://192.168.1.10:5244 \
  --token your-admin-token \
  --remote-path /movies --local-dir ./data/movies \
  --interval 6h
```

> 无 token 时使用账号密码登录:`--username admin --password secret`。
> 不确定配置是否正确?加 `--dry-run` 演练,只打印将要执行的操作,不实际传输。

### Docker 命令行

```bash
# 依赖镜像内默认工作目录 /data,配置文件自动落在数据目录,无需指定 WEB_STORE
docker run -d --name openlist-sync \
  -p 18222:18222 \
  -v "$PWD/data:/data" \
  anyear/openlist-sync:latest
```

> 容器入口自动修正挂载目录属主(uid 10001)后降权运行,无需手动 chown。

## 配置

参数优先级:**命令行参数 > 环境变量 > 默认值**。运行 `openlist-sync --help` 查看全部参数。

### Web 界面

| 环境变量 | 说明 | 默认 |
| --- | --- | --- |
| `WEB_LISTEN` | HTTP 监听地址 | `127.0.0.1:18222`(仅本机) |
| `WEB_STORE` | 配置文件路径(默认相对工作目录 `/data`) | `openlist-sync.json` |
| `WEB_API_TOKEN` | 访问令牌(留空则无需认证) | 空 |

### 命令行 / daemon 模式

| 环境变量 | 对应参数 | 默认 |
| --- | --- | --- |
| `OPENLIST_BASE_URL` | `--base-url` | 必填 |
| `OPENLIST_TOKEN` | `--token` | — |
| `OPENLIST_USERNAME` / `OPENLIST_PASSWORD` | `--username` / `--password` | — |
| `OPENLIST_TASKS` | 每行 `远端路径\|本地目录` | — |
| `SYNC_DIRECTION` | `--direction` | `both` |
| `SYNC_CLEANUP` | `--cleanup` | `none` |
| `SYNC_CONFLICT` | `--conflict` | `newest` |
| `SYNC_TYPE` | `--type` | `video,audio,image,text` |
| `SYNC_INCLUDE_EXT` / `SYNC_EXCLUDE_EXT` | `--include-ext` / `--exclude-ext` | — |
| `SYNC_CONCURRENCY` | `--concurrency` | `4` |
| `SYNC_RATE_LIMIT` | `--rate-limit` | `0`(不限速) |
| `SYNC_RETRIES` | `--retries` | `3` |
| `SYNC_INTERVAL` | `--interval` | `1h` |
| `SYNC_DOWNLOAD_MODE` | `--download-mode` | `direct` |

## 使用说明

- **冲突策略** —— 本地与远端文件不一致时的处理方式:`newest` 保留较新的,`remote` 以远端为准,`local` 以本地为准,`skip` 跳过该文件
- **清理模式** —— 仅在对方已删除文件时生效;被筛选排除的文件不会被清理
- **调度间隔** —— 支持 `30s`、`1h`、`1d` 等格式;任务留空则使用全局默认间隔
- **认证** —— 优先使用令牌(token),请求返回 401 时自动重新登录并重试

## 构建

```bash
make build    # 当前平台二进制 → dist/
make image    # Docker 镜像
make test     # 运行单元测试
```