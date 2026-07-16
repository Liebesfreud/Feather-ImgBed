# 轻羽图床 Feather-ImgBed

[![Release](https://img.shields.io/github/v/release/Liebesfreud/Feather-ImgBed?display_name=tag&sort=semver)](https://github.com/Liebesfreud/Feather-ImgBed/releases)
[![CI](https://github.com/Liebesfreud/Feather-ImgBed/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Liebesfreud/Feather-ImgBed/actions/workflows/ci.yml)
[![GHCR](https://img.shields.io/badge/GHCR-v0.0.1-2496ED?logo=docker&logoColor=white)](https://github.com/Liebesfreud/Feather-ImgBed/pkgs/container/feather-imgbed)
[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

轻量、现代、可自托管的多存储图床。提供图片上传、管理、分享和随机图 API，数据与存储凭据完全由你掌控。

当前稳定版本：**v0.0.1**

## 功能特性

- 支持单图与批量上传，兼容 JPEG、PNG、GIF 和 WebP。
- 支持本地、S3 兼容对象存储、WebDAV 和 Telegram 存储。
- 提供图片搜索、日期与存储筛选、游标分页、详情查看和可靠删除。
- 提供公开随机图 API，可直接返回图片或 JSON 数据。
- 支持 API Token，方便 PicGo、自定义脚本和其他客户端接入。
- 使用 SHA-256 检测重复文件，支持随机、日期目录和原文件名等命名规则。
- 管理员密码使用 bcrypt 哈希；存储凭据使用 AES-256-GCM 加密。
- 内置会话、CSRF、上传与登录限流、请求 ID、健康检查和优雅关闭。
- 使用 SQLite WAL 持久化，自动检查并执行数据库迁移。
- 单个 Go 二进制内嵌前端资源，Docker 容器以非 root 用户运行。

> 当前版本暂未启用 AVIF 解码。Telegram 存储需要自行提供稳定的公开代理地址。

## 快速开始

推荐使用正式版 GHCR 镜像。以下示例将所有数据库、主密钥和本地图片持久化到 `feather-data` 数据卷：

```bash
docker run -d \
  --name feather-imgbed \
  --restart unless-stopped \
  -p 8080:8080 \
  -v feather-data:/data \
  -e FEATHER_SECURE_COOKIE=false \
  ghcr.io/liebesfreud/feather-imgbed:0.0.1
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
    image: ghcr.io/liebesfreud/feather-imgbed:0.0.1
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

命令行参数包括 `-listen`、`-data`、`-log-level` 和 `-master-key-file`。

### 存储配置

- 本地：`data_dir`、`public_url`
- S3：`endpoint`、`region`、`bucket`、`access_key`、`secret_key`、`public_url`、`path_style`
- WebDAV：`url`、`username`、`password`、`target_dir`、`public_url`
- Telegram：`bot_token`、`chat_id`、`public_url`

更新存储时，敏感字段留空表示保持原值。停用或删除默认存储前，必须先更换系统默认存储；仍有关联图片的存储不能删除。

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
| `GET` | `/random` | 随机图片或 JSON 数据 |
| `GET`、`DELETE` | `/images/{id}` | 图片详情、删除图片 |
| `GET` | `/storages` | 查询脱敏后的存储配置 |
| `PUT`、`DELETE` | `/storages/{id}` | 保存、停用或删除存储 |
| `POST` | `/storages/test?storage_id={id}` | 测试存储连接 |
| `GET`、`PUT` | `/settings` | 查询、更新系统设置 |
| `GET` | `/system` | 查询版本和系统状态 |

图片列表支持 `limit`、`cursor`、`storage_id`、`search`、`from` 和 `to` 参数。上传接口使用 `multipart/form-data`，可重复提交 `file` 字段实现批量上传；部分文件失败时返回 HTTP `207`。

随机图可以直接用作图片地址：

```html
<img src="https://img.example.com/api/v1/random" alt="随机图片">
```

获取 JSON 数据：

```bash
curl 'https://img.example.com/api/v1/random?format=json'
```

## 备份与升级

建议停服务后整体备份 `/data`。该目录包含：

- `feather.db`：SQLite 数据库。
- `images/`：本地存储的图片。
- `master.key`：远程存储凭据的加密主密钥。

主密钥丢失后，已保存的远程存储凭据无法恢复。升级前应先完成备份，然后拉取新的固定版本镜像并重新创建容器。程序启动时会自动执行兼容的数据库迁移；数据库版本高于当前程序支持版本时，服务会拒绝启动。

## 从源码开发

需要 Go 1.23+ 和 Node.js 22+：

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

- `main`：测试与日常开发分支。
- `product`：生产分支，提交后自动构建 GHCR 候选镜像。
- 正式 Release 使用 `vX.Y.Z` 标签；标签提交必须存在于 `product` 分支。

`v0.0.1` 正式镜像地址：

```text
ghcr.io/liebesfreud/feather-imgbed:0.0.1
```

完整的分支保护、镜像标签和 Release 流程见 [分支与发布自动化](docs/automation.md)。

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
