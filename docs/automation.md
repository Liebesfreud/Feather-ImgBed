# 分支与发布自动化

## 分支职责

- `main`：测试分支。提交及面向 `main` 的 Pull Request 会运行前端构建、Go 格式检查、`go vet`、测试、应用构建和 Docker 构建验证。
- `product`：生产分支。面向 `product` 的 Pull Request 会运行相同的持续集成检查；合并后自动构建并推送 GHCR 候选镜像。

建议将 `main` 设置为默认分支，日常变更先合并到 `main`，验证完成后再通过 Pull Request 将 `main` 合并到 `product`。

## 依赖更新

Dependabot 每月检查 Go、npm、GitHub Actions 和 Docker 依赖，并将不同生态的更新合并到一个 Pull Request，统一执行持续集成检查。TypeScript 主版本更新暂时忽略，待 `vue-tsc` 明确兼容后再手动升级。

## 镜像标签

镜像地址为：

```text
ghcr.io/<仓库所有者>/<仓库名>
```

`product` 每次更新后发布：

- `product`：最新生产候选镜像，不代表正式 Release。
- `sha-<短提交哈希>`：不可变的提交定位标签，便于回滚和审计。

正式 GitHub Release 发布后发布：

- `X.Y.Z`：完整版本。
- `X.Y`：同一小版本系列的最新版本。
- `latest`：最新正式版本。

正式 Release 必须满足以下条件，否则工作流会拒绝发布：

1. Release 不是草稿，且已执行“Publish release”。
2. 标签格式严格为 `vX.Y.Z`，例如 `v1.2.0`。
3. 标签所指向的提交存在于 `product` 分支历史中。

## 推荐的 GitHub 仓库设置

在 GitHub Rulesets 或 Branch protection rules 中保护 `main` 和 `product`：

- 禁止直接推送，要求通过 Pull Request 合并。
- 要求 `代码检查` 和 `Docker 构建` 状态检查通过。
- 要求分支在合并前保持最新，并至少需要一次审批。
- 禁止强制推送和删除分支。
- 对 `product` 限制可合并人员，生产变更优先使用 merge commit，保留从 `main` 晋级的记录。

仓库 Actions 默认权限建议设为只读；发布工作流仅在任务级别申请写入 Packages 和构建证明所需的权限。首次发布后，还需在 GitHub Packages 中确认镜像可见性符合预期。

## 正式发布步骤

1. 确认 `main` 的待发布内容已通过 Pull Request 合并到 `product`。
2. 在 `product` 对应提交上创建 `vX.Y.Z` 标签。
3. 使用该标签创建并发布 GitHub Release。
4. 等待“发布容器镜像”工作流成功，使用 `latest` 或固定的 `X.Y.Z` 标签部署。
