# 轻羽图床 Feather-ImgBed

[![Release](https://img.shields.io/github/v/release/Liebesfreud/Feather-ImgBed?display_name=tag&sort=semver)](https://github.com/Liebesfreud/Feather-ImgBed/releases)
[![CI](https://github.com/Liebesfreud/Feather-ImgBed/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Liebesfreud/Feather-ImgBed/actions/workflows/ci.yml)
[![GHCR](https://img.shields.io/badge/GHCR-v0.1.6-2496ED?logo=docker&logoColor=white)](https://github.com/Liebesfreud/Feather-ImgBed/pkgs/container/feather-imgbed)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

轻量、现代、可自托管的多存储图床。提供图片上传、管理、分享和随机图 API，数据与存储凭据完全由你掌控。

当前稳定版本：**v0.1.6**

## 功能特性

- 支持单图、批量和安全图片 URL 导入，兼容 JPEG、PNG、GIF 和 WebP。
- 支持本地、S3 兼容对象存储、WebDAV 和 Telegram 存储。
- 提供图片搜索、日期/收藏/标签/相册/存储筛选、双向游标分页和详情查看。
- 支持批量复制、批量收藏、批量标签、软删除、回收站恢复与可重试的永久清理。
- 上传时生成缩略图；可选生成非破坏式 WebP 和文字水印派生版本。
- 默认保留原始上传内容；可选清理静态图片的 EXIF、GPS 等隐私元数据并应用 JPEG 方向，GIF 始终保留动画原文件。
- 支持收藏、标签与相册整理，相册删除不会删除图片。
- 随机图 API 默认关闭；启用后可限定到指定相册、标签或两者交集，避免私人图片意外公开。
- 支持 API Token，方便 PicGo、自定义脚本和其他客户端接入。
- 使用 SHA-256 检测重复文件，支持随机、日期目录和原文件名等命名规则。
- 管理员密码使用 bcrypt 哈希；存储凭据使用 AES-256-GCM 加密。
- 内置 30 天管理员登录会话、CSRF、上传与登录限流、请求 ID、健康检查和优雅关闭。
- 使用 SQLite WAL 持久化，自动检查并执行数据库迁移。
- 支持运行时 SQLite 一致性快照、age 口令加密、备份完整校验、定时保留和恢复前验证。
- 提供管理员离线密码重置、远程对象抽样校验、跨存储迁移、目录导入和完整数据导出命令。
- 内置只读诊断和缩略图回填维护命令。
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
  ghcr.io/liebesfreud/feather-imgbed:0.1.6
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
    image: ghcr.io/liebesfreud/feather-imgbed:0.1.6
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
| `FEATHER_BACKUP_INTERVAL` | 空 | 自动备份间隔，例如 `24h`；留空关闭 |
| `FEATHER_BACKUP_RETENTION` | `7` | 自动备份保留份数，支持 1–365 |
| `FEATHER_BACKUP_DIR` | `{数据目录}/backups` | 自动备份目录，建议挂载到独立磁盘或数据卷 |
| `FEATHER_BACKUP_PASSPHRASE_FILE` | 空 | 自动备份加密口令文件；启用自动备份时必填 |
| `FEATHER_BACKUP_VERIFY_REMOTE` | `0` | 每次备份后每个远程存储抽样校验的对象数，`0` 表示关闭 |

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

# 创建、校验和恢复加密备份；恢复时必须先停服务
feather-imgbed backup create -data ./data -passphrase-file ./backup.passphrase -output ./feather-backup.tar.gz.age
feather-imgbed backup verify -passphrase-file ./backup.passphrase ./feather-backup.tar.gz.age
feather-imgbed backup restore -data ./data -passphrase-file ./backup.passphrase ./feather-backup.tar.gz.age

# 忘记管理员密码时离线重置，并注销现有会话
feather-imgbed auth reset-password -data ./data -password-file ./new-password.txt

# 校验对象、预演并执行跨存储迁移
feather-imgbed storage verify -data ./data -sample 20
feather-imgbed storage migrate -data ./data -from old-s3 -to new-s3 -dry-run
feather-imgbed storage migrate -data ./data -from old-s3 -to new-s3

# 从目录批量导入；导出完整图片对象与组织元数据
feather-imgbed data import-dir -data ./data -storage local /path/to/photos
feather-imgbed data export -data ./data -output /safe/feather-export

# 为缺少缩略图的旧记录回填，并把旧格式缩略图升级为 WebP；
# 缺少 file_id 的旧 Telegram 记录会跳过并写入报告
feather-imgbed thumbnails rebuild -data ./data
```

服务参数包括 `-listen`、`-data`、`-log-level`、`-master-key-file` 和对应的自动备份参数。诊断命令支持 `-json` 和 `-network`；未传 `-network` 时不会连接远程存储。Go 标准命令行解析要求选项写在目录或归档等位置参数之前。

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
完整的机器可读契约见 [OpenAPI 3.1 文档](docs/openapi.yaml)。

API Token 默认只授予 `images:upload`。可按用途组合 `images:read`、`images:manage`、
`images:delete` 和 `settings:admin`；旧版本创建的 Token 在迁移后保留原有完整权限。

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
| `GET` | `/statistics` | 查询图片数量、存储占用和累计流量 |

图片列表支持 `limit`、`cursor`、`storage_id`、`search`、`from`、`to`、`order`、`favorite`、`tag_id` 和 `album_id` 参数。上传接口使用 `multipart/form-data`，可重复提交 `file` 字段实现批量上传；部分文件失败时返回 HTTP `207`。

URL 导入只接受直接图片地址，仍会执行与本地上传相同的大小、MIME、像素、哈希和存储检查。服务会拒绝私网、环回、链路本地、多播等地址，每次重定向都会重新解析验证并固定到已验证 IP。

图片详情的 `url` 始终是原图地址；可用派生版本在 `variants` 数组中返回，`kind` 当前包括 `thumbnail`、`webp` 和 `watermarked`。

随机图默认关闭。需要使用时，在“图片管理 → 随机图”独立页面中显式启用，并可限定一个相册、一个标签或两者交集。关闭时接口返回 `404 RANDOM_IMAGE_DISABLED`。

启用后可以直接用作图片地址：

```html
<img src="https://img.example.com/api/v1/random" alt="随机图片">
```

获取 JSON 数据：

```bash
curl 'https://img.example.com/api/v1/random?format=json'
```

## 数据安全、备份与恢复

备份会通过 SQLite `VACUUM INTO` 创建一致性数据库快照，因此可以在服务运行时生成；本地对象会在写入归档时复核大小和 SHA-256。为降低大量上传、删除与备份并发导致本次备份失败的概率，手工备份仍建议选择业务空闲时段。恢复和密码重置必须先停止服务。

```bash
feather-imgbed backup create -data /data \
  -passphrase-file /run/secrets/feather-backup \
  -output /safe/feather-backup.tar.gz.age

feather-imgbed backup verify \
  -passphrase-file /run/secrets/feather-backup \
  /safe/feather-backup.tar.gz.age

# 先停止正在使用 /data 的 Feather-ImgBed 实例
feather-imgbed backup restore -data /data \
  -passphrase-file /run/secrets/feather-backup \
  /safe/feather-backup.tar.gz.age
```

口令至少 12 个字符，建议由密码管理器生成，并将口令文件以 `0600` 权限保存在备份之外。未指定口令文件时仍可创建兼容旧版本的普通 `tar.gz`，但归档通常包含 `master.key`，不应明文存放在不可信位置。命令不会覆盖已有备份文件。

归档包含清单、逐文件大小和 SHA-256。`backup verify` 会解密归档、检查路径与大小限制、验证全部摘要、执行 SQLite `quick_check`，并确认归档内本地存储对象存在。恢复会在同卷临时目录完成同样的验证，全部通过后才切换数据目录；验证失败时原目录保持不变。`backups/` 与临时目录不会递归进入归档。

数据目录包含：

- `feather.db`：SQLite 数据库。
- `images/`：本地存储的图片。
- `master.key`：远程存储凭据的加密主密钥。

远程 S3、WebDAV、Telegram 图片不在普通备份归档中；归档只包含它们的元数据和加密配置。位于数据目录外的绝对路径本地存储也不会自动纳入归档，命令会输出必须单独备份的明确警告。主密钥丢失后，已保存的远程存储凭据无法恢复。若使用 `FEATHER_MASTER_KEY` 环境变量而不是密钥文件，归档不会复制该环境变量，执行 `backup verify` 或 `backup restore` 时必须再次提供同一变量。

启用自动备份时必须配置口令文件。服务启动后会立即创建并验证一份加密备份，之后在每次备份完成后重新计时，不会重叠执行：

```yaml
services:
  feather-imgbed:
    environment:
      FEATHER_BACKUP_INTERVAL: "24h"
      FEATHER_BACKUP_RETENTION: "14"
      FEATHER_BACKUP_DIR: /backup
      FEATHER_BACKUP_PASSPHRASE_FILE: /run/secrets/feather-backup
      FEATHER_BACKUP_VERIFY_REMOTE: "10"
    volumes:
      - feather-data:/data
      - feather-backup:/backup
    secrets:
      - feather-backup

volumes:
  feather-data:
  feather-backup:

secrets:
  feather-backup:
    file: ./backup.passphrase
```

`FEATHER_BACKUP_VERIFY_REMOTE` 只做有界抽样：原图校验 SHA-256，派生图校验大小。也可以随时手工执行 `storage verify`；默认检查远程存储，使用 `-include-local` 可同时检查本地存储，显式传 `-storage local` 时会直接检查该本地存储。

## 数据迁移与导出

跨存储迁移会复制原图及全部派生图，全部写入目标后才在事务中切换数据库引用，最后删除源对象。建议先停止上传/删除操作并执行预演：

```bash
feather-imgbed storage migrate -data /data -from old-storage -to new-storage -dry-run
feather-imgbed storage migrate -data /data -from old-storage -to new-storage
```

默认包含回收站图片；可使用 `-include-trash=false` 排除，或用 `-limit` 分批执行。迁移不会自动修改默认存储，请在设置页确认默认存储后再停用旧存储。逐项 JSON 报告会保留复制、数据库更新或旧对象清理失败的信息。

目录导入会递归识别图片，并复用网页上传的 MIME、大小、像素、去重、缩略图和派生处理流程，不会删除源文件：

```bash
feather-imgbed data import-dir -data /data -storage local /mnt/photos
```

完整导出会从所有本地和远程存储读取实际原图与派生图，同时生成包含图片、标签、相册、关联关系和 SHA-256 的 `metadata.json`：

```bash
feather-imgbed data export -data /data -output /safe/feather-export
```

默认不导出回收站，可加 `-include-trash`。导出目录不包含远程存储凭据；当前版本可对导出根目录运行 `data import-dir` 重新导入原图，但不会自动恢复原 ID、标签和相册关系，`metadata.json` 用于留档或后续迁移工具处理。

升级前应先完成备份，然后拉取新的固定版本镜像并重新创建容器。程序启动时会自动执行兼容的数据库迁移；数据库版本高于当前程序支持版本时，服务会拒绝启动。

## 从源码开发

需要 Go 1.25+ 和 Node.js 22+。仓库提供 `Makefile` 封装常用流程，首次运行会自动安装前端依赖：

```bash
git clone https://github.com/Liebesfreud/Feather-ImgBed.git
cd Feather-ImgBed
make dev        # 一键启动前后端开发服务
```

启动后访问 <http://127.0.0.1:5173>。前端修改会自动热更新，API 请求会代理到 `:8080`；按 `Ctrl+C` 可同时停止前后端。

其它常用目标：

```bash
make run             # 构建前端 + 启动单体服务（:8080）
make build           # 构建前端 + 编译二进制 feather-imgbed
make test            # 构建前端 + 运行 Go 测试（含竞态检测）
make vet             # 构建前端 + 运行 go vet
make test-frontend   # 仅运行前端单元测试
make check           # 提交前综合检查（前端构建 + Go 测试 + vet）
make clean           # 清理前端产物与二进制
```

> 前端产物 `internal/app/web/dist/` 由 `npm run build` 生成并被 `go:embed` 嵌入，未入库。直接运行 `go build`/`go test` 前需先构建前端，`make` 目标会自动处理这一步。

手动等价流程：

```bash
cd frontend && npm ci && npm run build && cd ..
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

`v0.1.6` 正式镜像地址：

```text
ghcr.io/liebesfreud/feather-imgbed:0.1.6
```

完整的分支保护、镜像标签和 Release 流程见 [分支与发布自动化](docs/automation.md)。

图片格式扩展的技术边界与后续验证矩阵见 [HEIC/HEIF 技术评估](docs/heic-evaluation.md)。

## 参与贡献

欢迎提交 [Issue](https://github.com/Liebesfreud/Feather-ImgBed/issues) 和 Pull Request。提交信息请使用中文，并遵循仓库中的 [AGENTS.md](AGENTS.md) 规范。

提交代码前请至少运行：

```bash
make check
```

等价于 `cd frontend && npm ci && npm run build && cd ..` 后运行 `go test ./...` 与 `go vet ./...`。

## 开源许可

本项目基于 [MIT License](LICENSE) 开源。
