# 轻羽图床 Feather-ImgBed

[![Release](https://img.shields.io/github/v/release/Liebesfreud/Feather-ImgBed?display_name=tag&sort=semver)](https://github.com/Liebesfreud/Feather-ImgBed/releases)
[![CI](https://github.com/Liebesfreud/Feather-ImgBed/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Liebesfreud/Feather-ImgBed/actions/workflows/ci.yml)
[![GHCR](https://img.shields.io/badge/GHCR-v0.1.2-2496ED?logo=docker&logoColor=white)](https://github.com/Liebesfreud/Feather-ImgBed/pkgs/container/feather-imgbed)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

轻量、现代、可自托管的多存储图床。提供图片上传、管理、分享和随机图 API，数据与存储凭据完全由你掌控。

当前稳定版本：**v0.1.2**

## 功能特性

- 支持单图、批量和安全图片 URL 导入，兼容 JPEG、PNG、GIF 和 WebP。
- 支持本地、S3 兼容对象存储、WebDAV 和 Telegram 存储。
- 提供图片搜索、日期/收藏/标签/相册/存储筛选、双向游标分页和详情查看。
- 支持批量复制、批量收藏、批量标签、软删除、回收站恢复与可重试的永久清理。
- 上传时生成缩略图；可选生成非破坏式 WebP 和文字水印派生版本，原图始终保留。
- 支持收藏、标签与相册整理，相册删除不会删除图片。
- 提供公开随机图 API，可直接返回图片或 JSON 数据。
- 支持 API Token，方便 PicGo、自定义脚本和其他客户端接入。
- 使用 SHA-256 检测重复文件，支持随机、日期目录和原文件名等命名规则。
- 管理员密码使用 bcrypt 哈希；存储凭据使用 AES-256-GCM 加密。
- 内置会话、CSRF、上传与登录限流、请求 ID、健康检查和优雅关闭。
- 使用 SQLite WAL 持久化，自动检查并执行数据库迁移。
- 内置离线备份/恢复、只读诊断和缩略图回填维护命令。
- 单个 Go 二进制内嵌前端资源，Docker 容器以非 root 用户运行。

> 当前版本暂未启用 AVIF、HEIC/HEIF 解码。GIF 默认不生成 WebP 或水印派生，以免静默丢失动画。Telegram 图片由 Feather-ImgBed 通过保存的 `file_id` 回源并代理提供；服务器无法直连 Telegram Bot API 时可另外配置 API 代理。

## 快速开始

推荐使用正式版 GHCR 镜像。以下示例将所有数据库、主密钥和本地图片持久化到 `feather-data` 数据卷：

```bash
docker run -d \
  --name feather-imgbed \
  --restart unless-stopped \
  -p 8080:8080 \
  -v feather-data:/data \
  -e FEATHER_SECURE_COOKIE=false \
  ghcr.io/liebesfreud/feather-imgbed:0.1.2
```

启动后访问 <http://127.0.0.1:8080>，根据页面提示创建管理员账户并填写站点访问地址。

查看运行状态：

```bash
curl http://127.0.0.1:8080/healthz
```

停止或升级容器时不要删除数据卷。`/data` 中包含数据库、图片和加密主密钥，必须整体持久化和备份。

## Docker Compose

仓库内的 `compose.yaml` 默认用于本地源码构建：

```bash
git clone https://github.com/Liebesfreud/Feather-ImgBed.git
cd Feather-ImgBed
docker compose up -d --build
```

生产环境建议改用固定版本镜像：

```yaml
services:
  feather-imgbed:
    image: ghcr.io/liebesfreud/feather-imgbed:0.1.2
    container_name: feather-imgbed
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - feather-data:/data
    environment:
      FEATHER_SECURE_COOKIE: "true"

volumes:
  feather-data:
```

生产服务应放在 HTTPS 反向代理后，并将 `FEATHER_SECURE_COOKIE` 设为 `true`。如果直接通过 HTTP 在本机试用，请暂时设为 `false`，否则浏览器不会发送安全 Cookie。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `FEATHER_LISTEN` | `:8080` | HTTP 监听地址 |
| `FEATHER_DATA_DIR` | `./data` | 数据库、主密钥和本地图片目录 |
| `FEATHER_LOG_LEVEL` | `info` | 日志级别，支持 `info`、`debug` |
| `FEATHER_MASTER_KEY` | 空 | 32 字节 base64url 主密钥，优先级最高 |
| `FEATHER_MASTER_KEY_FILE` | `{数据目录}/master.key` | 主密钥文件；不存在时以 `0600` 权限自动生成 |
| `FEATHER_SECURE_COOKIE` | `false` | HTTPS 生产环境必须设为 `true` |
| `FEATHER_TRUSTED_PROXIES` | 空 | 可信反向代理 CIDR，多个值用逗号分隔 |

### 命令行

不带子命令时仍会启动服务，与旧版本兼容：

```bash
feather-imgbed
feather-imgbed serve -listen :8080 -data ./data
```

运维命令：

```bash
# 默认只读诊断；存在警告返回 1，阻止运行的问题返回 2
feather-imgbed doctor -data ./data
feather-imgbed doctor -json -network -data ./data

# 必须停服务后执行离线备份或恢复
feather-imgbed backup create -data ./data -output ./feather-backup.tar.gz
feather-imgbed backup restore -data ./data ./feather-backup.tar.gz

# 为缺少缩略图的旧记录回填；缺少 file_id 的旧 Telegram 记录会跳过并写入报告
feather-imgbed thumbnails rebuild -data ./data
```

服务参数包括 `-listen`、`-data`、`-log-level` 和 `-master-key-file`。诊断命令支持 `-json` 和 `-network`；未传 `-network` 时不会连接远程存储。

### 存储配置

- 本地：`data_dir`、`public_url`
- S3：`endpoint`、`region`、`bucket`、`access_key`、`secret_key`、`public_url`、`path_style`
- WebDAV：`url`、`username`、`password`、`target_dir`、`public_url`
- Telegram：`bot_token`、`chat_id`、可选的 `proxy_url`

更新存储时，敏感字段留空表示保持原值。停用或删除默认存储前，必须先更换系统默认存储；仍有关联图片的存储不能删除。

Cloudflare R2 的 `https://<ACCOUNT_ID>.r2.cloudflarestorage.com` 填写在 `endpoint`；`public_url` 可填写图床的外部访问地址（例如 `https://img.example.com`），留空时自动使用系统的“站点访问地址”。R2 原图和派生图会由 Feather-ImgBed 使用保存的 S3 凭据回源，并通过本站 `/s3-files/` 路由提供；Bucket 无需开启公开访问。误填在 `public_url` 中的 R2 Endpoint 会被忽略。程序启动或站点地址变更时，已有图片链接会一并更新为完整代理地址。

Telegram 上传记录会保存 Bot API 返回的 `message_id` 和 `file_id`：前者用于删除频道或群组中的消息，后者用于通过本站的 `/tg-files/` 路由回源读取文件。`proxy_url` 不是图片公开域名，而是可选的 Telegram Bot API 反向代理地址；留空时直接使用 `https://api.telegram.org`。升级前已经上传且没有 `file_id` 的 Telegram 对象仍无法回读，但保留原有删除兼容性。

## API

业务接口统一使用 `/api/v1` 前缀。响应包含 `request_id`，响应头同步返回 `X-Request-ID`。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/auth/status` | 查询初始化和认证状态 |
| `POST` | `/auth/initialize` | 首次创建管理员 |
| `POST` | `/auth/login`、`/auth/logout` | 登录、退出 |
| `PUT` | `/auth/password` | 修改密码并注销全部会话 |
| `GET`、`POST` | `/tokens` | 查询、创建 API Token |
| `DELETE` | `/tokens/{id}` | 撤销 API Token |
| `GET`、`POST` | `/images` | 查询、上传图片 |
| `POST` | `/upload` | 上传兼容入口 |
| `POST` | `/images/import-url` | 从直接图片 URL 安全导入 |
| `POST` | `/images/bulk` | 批量移入回收站或设置收藏 |
| `GET` | `/random` | 随机图片或 JSON 数据 |
| `GET`、`PATCH`、`DELETE` | `/images/{id}` | 图片详情、设置收藏、移入回收站 |
| `GET`、`PUT` | `/images/{id}/tags` | 查询或替换图片标签 |
| `POST` | `/images/bulk/tags` | 批量添加或移除标签 |
| `GET` | `/trash` | 查询回收站 |
| `POST` | `/trash/{id}/restore` | 恢复图片 |
| `DELETE` | `/trash/{id}` | 永久删除单张图片 |
| `POST` | `/trash/purge` | 批量永久删除或清空回收站 |
| `GET`、`POST` | `/tags` | 查询、创建标签 |
| `PUT`、`DELETE` | `/tags/{id}` | 更新、删除标签 |
| `GET`、`POST` | `/albums` | 查询、创建相册 |
| `GET`、`PUT`、`DELETE` | `/albums/{id}` | 相册详情、更新、删除 |
| `POST` | `/albums/{id}/images` | 批量加入相册 |
| `DELETE` | `/albums/{id}/images/{image_id}` | 从相册移除图片 |
| `GET` | `/storages` | 查询脱敏后的存储配置 |
| `PUT`、`DELETE` | `/storages/{id}` | 保存、停用或删除存储 |
| `POST` | `/storages/test?storage_id={id}` | 测试存储连接 |
| `GET`、`PUT` | `/settings` | 查询、更新系统设置 |
| `GET` | `/system` | 查询版本和系统状态 |

图片列表支持 `limit`、`cursor`、`storage_id`、`search`、`from`、`to`、`order`、`favorite`、`tag_id` 和 `album_id` 参数。上传接口使用 `multipart/form-data`，可重复提交 `file` 字段实现批量上传；部分文件失败时返回 HTTP `207`。

URL 导入只接受直接图片地址，仍会执行与本地上传相同的大小、MIME、像素、哈希和存储检查。服务会拒绝私网、环回、链路本地、多播等地址，每次重定向都会重新解析验证并固定到已验证 IP。

图片详情的 `url` 始终是原图地址；可用派生版本在 `variants` 数组中返回，`kind` 当前包括 `thumbnail`、`webp` 和 `watermarked`。

随机图可以直接用作图片地址：

```html
<img src="https://img.example.com/api/v1/random" alt="随机图片">
```

获取 JSON 数据：

```bash
curl 'https://img.example.com/api/v1/random?format=json'
```

## 备份、恢复与升级

备份与恢复是离线操作。执行前必须停止服务，避免 SQLite 与本地图片在归档期间变化：

```bash
feather-imgbed backup create -data /data -output /safe/feather-backup.tar.gz
feather-imgbed backup restore -data /data /safe/feather-backup.tar.gz
```

归档包含清单、逐文件大小和 SHA-256。恢复会先在同卷临时目录中校验路径、数量、大小与全部摘要，全部通过后才切换数据目录；验证失败时原目录保持不变。`backups/` 与临时目录不会递归进入归档。

数据目录包含：

- `feather.db`：SQLite 数据库。
- `images/`：本地存储的图片。
- `master.key`：远程存储凭据的加密主密钥。

远程 S3、WebDAV、Telegram 图片不在本地归档中；归档只包含它们的元数据和加密配置。位于数据目录外的绝对路径本地存储也不会自动纳入归档，命令会输出必须单独备份的明确警告。主密钥丢失后，已保存的远程存储凭据无法恢复。

升级前应先完成备份，然后拉取新的固定版本镜像并重新创建容器。程序启动时会自动执行兼容的数据库迁移；数据库版本高于当前程序支持版本时，服务会拒绝启动。

## 从源码开发

需要 Go 1.25+ 和 Node.js 22+：

```bash
git clone https://github.com/Liebesfreud/Feather-ImgBed.git
cd Feather-ImgBed/frontend
npm ci
npm run build
cd ..
go run . -listen :8080 -data ./data
```

运行检查：

```bash
go test ./...
go vet ./...
go build ./...
```

前端使用 Vue 3、Vite、TypeScript 和 Pinia，后端使用 Go 与 SQLite。前端生产资源构建后嵌入 Go 二进制。

## 分支与发布

- 仓库只维护 `main` 分支，用于日常开发、测试和发布。
- 普通推送、Pull Request 和单独推送标签都不会发布镜像。
- 只有发布标签格式为 `vX.Y.Z` 的正式 GitHub Release，才会自动构建并推送 GHCR 镜像；标签提交必须存在于 `main` 分支历史中。

`v0.1.2` 正式镜像地址：

```text
ghcr.io/liebesfreud/feather-imgbed:0.1.2
```

完整的分支保护、镜像标签和 Release 流程见 [分支与发布自动化](docs/automation.md)。

图片格式扩展的技术边界与后续验证矩阵见 [HEIC/HEIF 技术评估](docs/heic-evaluation.md)。

## 参与贡献

欢迎提交 [Issue](https://github.com/Liebesfreud/Feather-ImgBed/issues) 和 Pull Request。提交信息请使用中文，并遵循仓库中的 [AGENTS.md](AGENTS.md) 规范。

提交代码前请至少运行：

```bash
cd frontend && npm ci && npm run build && cd ..
go test ./...
go vet ./...
```

## 开源许可

本项目基于 [MIT License](LICENSE) 开源。
