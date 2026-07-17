# Feather-ImgBed 详细改进计划

> 制定日期：2026-07-17
> 参考项目：
> [OU-Image Hosting](https://github.com/cshaizhihao/ou-image-hosting)、
> [FuImage](https://github.com/tomtiom383-afk/fu-image)

## 1. 目标与原则

本计划以 Feather-ImgBed 当前的 Go、SQLite、Vue 3 和多存储架构为基础，目标是在保持“轻量、单二进制、自托管、多存储”定位的前提下，逐步补齐图片管理、性能优化、运维和图库整理能力。

实施时遵循以下原则：

1. 先建立数据库升级与测试基础，再增加需要修改数据模型的功能。
2. 原图始终保留，缩略图、WebP 和水印等处理结果作为派生版本存储。
3. 所有存储继续通过统一存储接口访问，不为单一存储编写业务特例。
4. 批量操作必须有数量限制、逐项结果和可靠的失败恢复机制。
5. 公共上传、多用户和权限系统属于产品定位升级，不与个人图床功能混在早期版本中。
6. 保持 `CGO_ENABLED=0` 和多架构镜像能力；确需 CGO 的图片格式支持应作为可选增强方案。

## 2. 总体路线

| 阶段 | 版本建议 | 主要内容 | 单人开发估算 |
| --- | --- | --- | ---: |
| 0 | 技术准备 | 数据库迁移框架、测试基础、上传逻辑解耦 | 2–3 天 |
| 1 | v0.0.2 | 批量操作、回收站、排序和完整筛选 | 4–6 天 |
| 2 | v0.0.3 | 缩略图、真实上传进度、复制偏好 | 4–7 天 |
| 3 | v0.1.0 | 备份、恢复、诊断与维护命令 | 4–6 天 |
| 4 | v0.2.0 | 收藏、标签、相册 | 6–10 天 |
| 5 | v0.3.0 | URL 上传、图片派生处理、格式扩展 | 5–8 天 |
| 6 | 可选大版本 | 公共上传、多用户、权限、配额、审计 | 12 天以上 |

除可选公共平台能力外，整体预计需要 25–40 个开发日。实际应按阶段独立发布，不建议一次性开发全部功能。

---

## 3. 阶段 0：工程基础

### 3.1 改造数据库迁移

#### 当前问题

当前数据库逻辑主要用于从空数据库创建 v1。后续新增回收站、派生版本、标签和相册后，必须保证已有用户可以逐版本升级。

#### 实现方式

将迁移改为有序列表：

```go
type migration struct {
	Version int
	Up      func(context.Context, *sql.Tx) error
}

var migrations = []migration{
	{Version: 1, Up: migrateV1},
	{Version: 2, Up: migrateV2},
	{Version: 3, Up: migrateV3},
}
```

启动时执行：

1. 读取 `PRAGMA user_version`。
2. 拒绝打开高于当前程序支持版本的数据库。
3. 按版本顺序执行所有尚未应用的迁移。
4. 每个迁移使用独立事务。
5. 迁移成功后在同一事务中更新 `user_version`。
6. 迁移失败时回滚并拒绝启动。
7. 新安装也顺序执行所有迁移，不单独维护“最新建表 SQL”。

#### 涉及文件

- `internal/app/db.go`
- 新增 `internal/app/migrations.go`
- 新增数据库迁移测试

#### 验收标准

- 空数据库可以直接升级到最新版本。
- 真实 v1 数据库升级后，用户、图片、存储、Token 和会话均不丢失。
- 迁移中途失败时，数据库版本号和结构不前进。
- 新版本数据库仍会被旧程序安全拒绝。

### 3.2 拆分图片模块

#### 当前问题

上传、查询、随机图和删除集中在一个文件中，继续加入缩略图、回收站和标签会降低可维护性。

#### 实现方式

拆分为：

```text
internal/app/
├── image_upload.go
├── image_query.go
├── image_delete.go
├── image_processing.go
├── image_variants.go
└── image_organization.go
```

将 multipart 专用的上传逻辑重构成通用图片接收入口：

```go
func (a *App) ingestImage(
	ctx context.Context,
	source io.Reader,
	filename string,
	expectedSize int64,
	storageID string,
) (Image, error)
```

本地文件上传、URL 上传和未来的数据导入统一经过：

1. 大小限制。
2. 临时文件。
3. SHA-256。
4. MIME 检测。
5. 图片解码与尺寸检查。
6. 去重。
7. 对象存储。
8. 派生版本处理。
9. 数据库写入与失败回滚。

### 3.3 提升存储后端的可测试性

#### 实现方式

为 `App` 增加可注入的存储工厂：

```go
type backendFactory func(StorageRecord) (storageBackend, error)
```

生产环境继续调用现有的本地、S3、WebDAV 和 Telegram 实现；测试环境可以注入内存存储或故障存储。

#### 需要覆盖的失败场景

- 原图上传失败。
- 缩略图上传失败。
- 对象写入成功但数据库写入失败。
- 永久删除中部分对象失败。
- 客户端取消请求后，回滚仍使用独立上下文。

### 3.4 建立前端测试

增加：

- Vitest
- Vue Test Utils
- jsdom
- Playwright 烟雾测试

前端脚本补充：

```json
{
	"scripts": {
		"test": "vitest run",
		"test:e2e": "playwright test"
	}
}
```

CI 中增加单元测试和核心管理流程 E2E。

---

## 4. 阶段 1：完成图片管理闭环

### 4.1 真正可用的批量管理

#### 前端实现

图库进入批量模式后显示固定操作栏，包含：

- 已选择数量。
- 选择当前已加载图片。
- 清空选择。
- 批量复制 URL。
- 批量复制 Markdown。
- 批量复制 HTML。
- 批量复制 BBCode。
- 批量移入回收站。
- 后续扩展批量收藏、标签和加入相册。

复制操作完全在浏览器端完成，不调用后端。

#### API

```http
POST /api/v1/images/bulk
Content-Type: application/json

{
  "action": "trash",
  "ids": ["id1", "id2"]
}
```

约束：

- 单次最多 100 个 ID。
- 对 ID 去重。
- 拒绝空数组和未知 action。
- 批量移入回收站使用一个 SQLite 事务。
- 返回请求数量、实际影响数量和未找到数量。

### 4.2 回收站与软删除

#### 数据库 v2

```sql
ALTER TABLE images ADD COLUMN deleted_at TEXT;
ALTER TABLE images ADD COLUMN purge_error TEXT;

CREATE INDEX idx_images_deleted
ON images(deleted_at, created_at DESC);
```

#### API

```http
DELETE /api/v1/images/{id}
GET    /api/v1/trash
POST   /api/v1/trash/{id}/restore
DELETE /api/v1/trash/{id}
POST   /api/v1/trash/purge
```

语义：

- `DELETE /images/{id}` 改为移入回收站，不立即删除存储对象。
- `GET /trash` 使用独立游标分页。
- `POST /trash/{id}/restore` 清空 `deleted_at` 和旧的清理错误。
- `DELETE /trash/{id}` 删除派生对象、原图和数据库记录。
- `POST /trash/purge` 支持批量永久删除或清空回收站。

#### 永久删除流程

1. 查询图片与所有派生版本。
2. 加载对应存储配置。
3. 逐个删除派生对象。
4. 删除原图。
5. 所有对象删除成功后删除数据库记录。
6. 失败时保留记录，在 `purge_error` 中记录安全截断后的错误。
7. 批量清理返回逐项结果，部分失败时使用 HTTP 207。

#### 查询调整

以下查询统一增加 `deleted_at IS NULL`：

- 普通图片列表。
- 图片详情。
- 随机图。
- 重复文件查找。

#### 已知限制

S3、WebDAV 和 Telegram 使用公开地址时，图片进入回收站后旧直链仍可能访问，直到永久删除。要立即撤销直链，需要未来引入私有存储和代理交付。

#### 前端

新增 `TrashView.vue` 和 `/trash` 路由：

- 分页查看已删除图片。
- 单项和批量恢复。
- 单项永久删除。
- 清空回收站。
- 显示删除时间和清理失败原因。
- 危险操作二次确认。

### 4.3 完成排序和日期筛选

#### API

```http
GET /api/v1/images
    ?from=...
    &to=...
    &order=desc
    &cursor=...
```

只允许：

```text
order=asc
order=desc
```

游标包含排序方向：

```text
base64(order + "\0" + created_at + "\0" + id)
```

升序使用 `>` 比较，降序使用 `<` 比较。切换排序后旧游标必须失效。

#### 前端

- 增加结束日期输入。
- 将静态“最新上传”改为真实排序选择器。
- 筛选条件同步到路由 query。
- 刷新后恢复筛选。
- 搜索或筛选变化时清空旧游标和选中状态。
- 结束日期按用户所在时区转换为当天 `23:59:59.999`。

#### 验收标准

- 相同时间戳下分页仍不重复、不漏图。
- 升序和降序均可连续加载。
- 切换排序不会错误复用旧游标。
- 起止日期边界正确。

---

## 5. 阶段 2：缩略图与上传体验

### 5.1 建立通用图片派生版本

不要为缩略图、WebP 和水印分别向 `images` 表增加大量字段，统一建立：

```sql
CREATE TABLE image_variants (
	id TEXT PRIMARY KEY,
	image_id TEXT NOT NULL
		REFERENCES images(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	object_key TEXT NOT NULL,
	public_url TEXT NOT NULL,
	mime_type TEXT NOT NULL,
	size INTEGER NOT NULL,
	width INTEGER NOT NULL,
	height INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(image_id, kind)
);

CREATE INDEX idx_image_variants_image
ON image_variants(image_id);
```

首个 `kind` 为 `thumbnail`，未来可以增加：

- `webp`
- `watermarked`
- `preview`
- `avif`

图片 API 增加 `thumbnail_url`，但保留 `url` 作为原图地址。

### 5.2 上传时生成缩略图

#### 处理流程

1. 上传内容流式写入临时文件，同时计算 SHA-256。
2. 检测真实 MIME。
3. 使用 `DecodeConfig` 获取宽高。
4. 检查最大像素数量，避免解压炸弹。
5. 上传原图。
6. 从临时文件生成最长边 480px 的缩略图。
7. 将缩略图上传到同一存储。
8. 在同一数据库事务中插入原图和派生版本记录。
9. 数据库写入失败时，使用独立上下文回滚原图与缩略图。

#### 对象路径

```text
variants/{image-id}/thumbnail.jpg
variants/{image-id}/thumbnail.png
```

#### 编码策略

- JPEG 原图生成质量 82 的 JPEG 缩略图。
- 含透明通道的 PNG/WebP 生成 PNG。
- GIF 取首帧生成静态预览，原始动画不修改。
- 使用现有 `golang.org/x/image` 缩放。
- 不引入 CGO，继续支持当前跨架构构建。

#### 失败策略

- 原图上传成功但缩略图生成失败时，允许原图入库。
- 写入结构化警告日志。
- `thumbnail_url` 为空时前端回退到原图。
- 缩略图失败不能把整个上传队列标记为失败。

#### 旧图片回填

- 第一版只保证新上传图片生成缩略图。
- 增加 `thumbnails rebuild` 维护命令。
- 本地、S3、WebDAV 在扩展 `storageBackend.Open()` 后支持回填。
- 旧 Telegram 记录缺少完整的重新下载元数据，先跳过并生成报告。

### 5.3 真实上传进度与有限并发

#### 当前问题

当前上传串行执行，并使用计时器模拟进度。

#### 实现方式

- 新增基于 `XMLHttpRequest` 的 `uploadFile()`。
- 使用 `xhr.upload.onprogress` 更新真实百分比。
- 默认最多同时上传 3 个任务。
- 每个任务保存取消句柄。
- 支持单项取消和失败重试。
- 更换上传存储只影响尚未开始的任务。

队列状态：

```text
waiting → uploading → success
                    ↘ error → retrying
```

继续使用单文件请求，以保留单项进度、取消和重试能力。

### 5.4 链接格式与复制偏好

新增：

```text
frontend/src/linkFormats.ts
```

```ts
type LinkFormat = 'url' | 'markdown' | 'html' | 'bbcode'
```

BBCode 格式：

```text
[img]https://example.com/image.jpg[/img]
```

浏览器本地偏好：

```text
feather-copy-format
feather-auto-copy
```

设置页增加：

- 默认复制格式。
- 上传完成后自动复制。
- 批量复制的分隔方式。

自动复制规则：

- 单图完成后复制该图。
- 批量上传全部结束后一次性复制所有成功项。
- 浏览器拒绝异步剪贴板权限时，只提示用户点击“全部复制”，不把上传判为失败。

---

## 6. 阶段 3：备份、恢复与诊断

### 6.1 引入子命令

保持原启动方式兼容：

```text
feather-imgbed
feather-imgbed serve
feather-imgbed doctor
feather-imgbed backup create
feather-imgbed backup restore
feather-imgbed thumbnails rebuild
```

无子命令时等价于 `serve`。

### 6.2 离线备份

第一版优先实现离线备份，避免在线上传期间数据库和图片文件不一致。

归档使用 `tar.gz`，包含：

```text
manifest.json
feather.db
master.key
images/
FEATHER_DATA_DIR 内的其他本地存储目录
```

`manifest.json` 包含：

- 应用版本。
- 数据库版本。
- 创建时间。
- 文件数量。
- 每个文件的相对路径、大小和 SHA-256。
- 是否包含主密钥。

安全要求：

- 禁止绝对路径和 `..`。
- 排除 `backups/` 目录。
- 限制归档文件数量。
- 限制单文件和解压总大小。
- 恢复前验证全部 SHA-256。
- 解压到同卷临时目录。
- 全部验证通过后再切换数据目录。
- 恢复失败时保留原目录。

范围说明：

- 远程 S3、WebDAV、Telegram 图片不进入本地备份。
- 备份包含远程图片元数据和加密后的存储凭据。
- 数据目录外的绝对路径本地存储需要发出警告并单独备份。

### 6.3 Doctor 诊断

默认只读检查：

- 数据目录存在且可写。
- `master.key` 存在、长度正确、权限合理。
- SQLite `PRAGMA quick_check`。
- 数据库版本。
- WAL 状态。
- 本地存储目录权限。
- 存储配置能否解密。
- 图片记录与本地文件是否对应。
- 剩余磁盘空间。
- HTTPS Cookie 与站点 URL 配置是否匹配。
- 使用 `--network` 时测试远程存储连接。

输出支持普通文本和 `--json`。

退出码：

- `0`：正常。
- `1`：存在警告。
- `2`：存在阻止运行的问题。

---

## 7. 阶段 4：图库整理

### 7.1 收藏

单管理员阶段直接扩展图片表：

```sql
ALTER TABLE images
ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0;
```

API：

```http
PATCH /api/v1/images/{id}

{
  "favorite": true
}
```

批量接口：

```http
POST /api/v1/images/bulk

{
  "action": "favorite",
  "value": true,
  "ids": ["id1", "id2"]
}
```

前端：

- 图片卡片收藏按钮。
- 图片详情收藏按钮。
- “仅看收藏”筛选。
- 批量收藏和取消收藏。

### 7.2 标签

数据库：

```sql
CREATE TABLE tags (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL COLLATE NOCASE UNIQUE,
	color TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE image_tags (
	image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
	tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	PRIMARY KEY(image_id, tag_id)
);
```

API：

```http
GET    /api/v1/tags
POST   /api/v1/tags
PUT    /api/v1/tags/{id}
DELETE /api/v1/tags/{id}

PUT  /api/v1/images/{id}/tags
POST /api/v1/images/bulk/tags
```

前端：

- 标签管理弹窗。
- 图片详情增删标签。
- 批量添加和移除标签。
- 按标签筛选。
- 删除标签时只解除关系，不删除图片。

### 7.3 相册

数据库：

```sql
CREATE TABLE albums (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	cover_image_id TEXT REFERENCES images(id) ON DELETE SET NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE album_images (
	album_id TEXT NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
	image_id TEXT NOT NULL REFERENCES images(id) ON DELETE CASCADE,
	position INTEGER NOT NULL DEFAULT 0,
	added_at TEXT NOT NULL,
	PRIMARY KEY(album_id, image_id)
);
```

API：

```http
GET    /api/v1/albums
POST   /api/v1/albums
GET    /api/v1/albums/{id}
PUT    /api/v1/albums/{id}
DELETE /api/v1/albums/{id}

POST   /api/v1/albums/{id}/images
DELETE /api/v1/albums/{id}/images/{image_id}
```

前端增加 `/albums`：

- 相册卡片、封面、数量和描述。
- 从图库批量加入相册。
- 相册详情移除图片。
- 设置相册封面。
- 删除相册不删除图片。
- 从相册打开图片后保留返回来源。

---

## 8. 阶段 5：导入与图片处理

### 8.1 URL 上传

API：

```http
POST /api/v1/images/import-url

{
  "url": "https://example.com/image.jpg",
  "storage_id": "local",
  "filename": "optional.jpg"
}
```

必须同时实现 SSRF 防护：

1. 只允许 HTTP 和 HTTPS。
2. 禁止 URL 用户名和密码。
3. DNS 解析出的所有地址都不能是 loopback、私网、link-local、multicast 或 unspecified。
4. 自定义 `DialContext` 固定到已验证的 IP，防止 DNS rebinding。
5. 每次重定向都重新验证目标。
6. 最多允许 3 次重定向。
7. 设置连接和整体下载超时。
8. 流式读取，最多读取 `max_file_size + 1`。
9. 不信任响应的 `Content-Type` 和扩展名，仍走 Feather 的内容检测。
10. 使用独立限流键。

前端上传页增加“本地文件 / 图片 URL”切换，只接受直接图片地址，不解析网页。

### 8.2 非破坏式 WebP 与水印

所有处理结果写入 `image_variants`，不覆盖原图。

设置示例：

```json
{
  "processing": {
    "generate_webp": false,
    "webp_quality": 82,
    "watermark_enabled": false,
    "watermark_text": "",
    "watermark_position": "bottom-right"
  }
}
```

规则：

- 原图始终保留。
- WebP 使用 `kind=webp`。
- 水印使用 `kind=watermarked`。
- 图片详情允许选择复制原图或派生版本。
- GIF 默认不转换、不加水印，避免丢失动画。
- 处理失败不影响原图上传，只记录处理状态。
- 是否把派生版本作为默认外链必须由用户明确配置。

### 8.3 HEIC/HEIF

先进行技术验证：

- amd64 和 arm64 构建。
- 是否需要 CGO/libheif。
- Docker 镜像体积。
- 解码内存消耗。
- EXIF 方向。
- 色彩空间。
- 大尺寸 iPhone 照片。
- API 客户端与浏览器上传的一致支持。

如果必须引入 CGO，建议发布可选增强镜像；默认镜像继续保持纯 Go 和单二进制。

第一阶段可以：

1. 保存 HEIC/HEIF 原文件。
2. 生成 JPEG 预览派生版本。
3. 图库使用预览图展示。
4. 复制链接时允许选择原文件或 JPEG 版本。

---

## 9. 阶段 6：可选公共平台能力

只有确定 Feather 要从个人图床转为多人公共图床后再实施。

### 9.1 多用户与权限

数据库增加：

- `users.role`：`owner`、`admin`、`user`。
- `images.owner_id`。
- `api_tokens.user_id`。
- Token scopes。
- 用户存储配额。
- 每日上传计数。

所有图片查询必须在后端附加所有权范围，不能只通过前端隐藏菜单实现隔离。

### 9.2 公共上传

设置：

- 是否允许游客上传。
- 是否开放注册。
- 单 IP 上传速率。
- 每日数量额度。
- 每日流量额度。
- 游客图片默认公开或隐藏。
- 公共上传使用的存储。
- 最大保留时间。

游客上传记录必须有独立归属，不能混入管理员个人上传历史。

### 9.3 验证码、封禁和审计

数据库：

```sql
CREATE TABLE audit_events (...);
CREATE TABLE ip_bans (...);
CREATE TABLE daily_usage (...);
```

审计事件至少包括：

- 上传。
- 登录失败。
- IP 封禁和解封。
- 设置修改。
- Token 创建与撤销。
- 永久删除。
- 备份与恢复。

验证码优先集成可配置的 Turnstile 等服务，不把简单算术题作为主要安全机制。

### 9.4 分享与私有交付

密码分享只有在对象不再直接公开时才真正有效，因此需要先完成：

- 私有 S3/WebDAV 对象。
- Feather 代理下载或短期签名 URL。
- 分享 Token 只保存摘要。
- 分享过期时间。
- 分享密码哈希。
- 访问次数。
- 撤销状态。
- 独立限流。
- 日志中隐藏分享 Token。

---

## 10. 全局测试与质量门槛

每个版本合并前至少执行：

```bash
cd frontend
npm ci
npm run build
npm test

cd ..
test -z "$(gofmt -l $(find . -type f -name '*.go'))"
go vet ./...
go test -race -count=1 ./...
go build -trimpath ./...
docker build .
```

E2E 必测场景：

1. v1 数据升级到新版本。
2. 上传、缩略图和重复检测。
3. 批量移入回收站、恢复和永久删除。
4. 删除远程存储失败后的重试。
5. 排序分页无重复、无遗漏。
6. 备份后在全新数据目录恢复。
7. 标签和相册删除不误删原图。
8. URL 上传拦截私网、危险重定向和超大响应。
9. 所有新增写接口继续验证会话、CSRF 和 API Token。
10. amd64 和 arm64 Docker 镜像均能构建和启动。

## 11. 建议的实施顺序

### 第一批：必须优先完成

1. 数据库迁移框架。
2. 图片模块解耦。
3. 批量操作。
4. 回收站。
5. 排序和完整日期筛选。

这一批解决当前界面已经展示但尚未形成完整操作闭环的问题，并为后续所有数据库功能建立可靠升级路径。

### 第二批：直接改善实际使用体验

1. 缩略图与派生版本表。
2. 真实上传进度。
3. 有限并发和取消上传。
4. BBCode。
5. 默认复制格式和自动复制。

### 第三批：提高自托管可靠性

1. 离线备份。
2. 安全恢复。
3. Doctor 诊断。
4. 缩略图回填工具。

### 第四批：增强图库管理

1. 收藏。
2. 标签。
3. 相册。

### 第五批：按需求扩展

1. URL 上传。
2. 非破坏式 WebP。
3. 非破坏式水印。
4. HEIC/HEIF 技术验证与可选支持。

### 暂不进入主线

- 公共上传。
- 用户注册。
- 完整 RBAC。
- 验证码与 IP 封禁。
- 密码分享。
- 访问分析和运营统计。

这些功能应在产品定位明确后作为独立大版本设计，避免 Feather 在早期失去轻量优势。
