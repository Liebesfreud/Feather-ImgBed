# 分支与发布自动化

## 分支职责

仓库只维护 `main` 分支。提交及面向 `main` 的 Pull Request 会运行前端构建、Go 格式检查、`go vet`、测试、应用构建和 Docker 构建验证，但不会发布镜像。

## 依赖更新

Dependabot 每月检查 Go、npm、GitHub Actions 和 Docker 依赖，并将不同生态的更新合并到一个 Pull Request，统一执行持续集成检查。TypeScript 主版本更新暂时忽略，待 `vue-tsc` 明确兼容后再手动升级。

## 镜像标签

镜像地址为：

```text
ghcr.io/<仓库所有者>/<仓库名>
```

正式 GitHub Release 发布后发布：

- `X.Y.Z`：完整版本。
- `X.Y`：同一小版本系列的最新版本。
- `latest`：最新正式版本。

正式 Release 必须满足以下条件，否则工作流会拒绝发布：

1. Release 不是草稿，且已执行“Publish release”。
2. Release 不是预发布版本。
3. 标签格式严格为 `vX.Y.Z`，例如 `v1.2.0`。
4. 标签所指向的提交存在于 `main` 分支历史中。

普通推送、Pull Request、单独推送 Git 标签和预发布 Release 都不会构建或推送 GHCR 镜像。

## 推荐的 GitHub 仓库设置

在 GitHub Rulesets 或 Branch protection rules 中保护 `main`：

- 禁止直接推送，要求通过 Pull Request 合并。
- 要求 `代码检查` 和 `Docker 构建` 状态检查通过。
- 要求分支在合并前保持最新，并至少需要一次审批。
- 禁止强制推送和删除分支。

仓库 Actions 默认权限建议设为只读；发布工作流仅在任务级别申请写入 Packages 和构建证明所需的权限。首次发布后，还需在 GitHub Packages 中确认镜像可见性符合预期。

## 正式发布步骤

1. 确认 `main` 的待发布内容已通过持续集成检查。
2. 在 `main` 对应提交上创建 `vX.Y.Z` 标签。
3. 使用该标签创建并发布 GitHub Release。
4. 等待“发布容器镜像”工作流成功，使用 `latest` 或固定的 `X.Y.Z` 标签部署。
