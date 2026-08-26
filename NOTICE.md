# 第三方通知

Copyright © 2026 DengDeng AI contributors.

DengDeng AI 按 GNU Lesser General Public License Version 3 或任何更高版本发布，完整条款见 [LICENSE](LICENSE)。本通知不改变第三方作品各自适用的许可证、版权和商标规则。

## 嵌入源码

图像工作台包含来自 [basketikun/infinite-canvas](https://github.com/basketikun/infinite-canvas) 的源码，固定上游提交为 `3e72f33fb9e7341ac04f27e841a2a45e47d98ffa`。该上游按 MIT License 发布，原许可证保存在 [frontend/workbench/LICENSE.upstream](frontend/workbench/LICENSE.upstream)，来源记录见 [frontend/workbench/UPSTREAM.md](frontend/workbench/UPSTREAM.md)。

分发修改后的工作台时必须同时保留 DengDeng AI 的许可证、本通知和上述 MIT 许可证。

## 能力库来源

内置提示词、规则和 Skill 目录为每个条目保存来源、作者和已知许可证。主要来源包括：

| 来源 | 地址 |
| --- | --- |
| OpenAI Plugins / Skills | https://github.com/openai/plugins |
| Anthropic Skills | https://github.com/anthropics/skills |
| Vercel Agent Skills | https://github.com/vercel-labs/agent-skills |
| Superpowers | https://github.com/obra/superpowers |
| Addy Osmani Agent Skills | https://github.com/addyosmani/agent-skills |
| GitHub Awesome Copilot | https://github.com/github/awesome-copilot |
| Google Skills | https://github.com/google/skills |
| Matt Pocock Skills | https://github.com/mattpocock/skills |
| Everything Claude Code | https://github.com/affaan-m/ECC |
| Marketing Skills | https://github.com/coreyhaines31/marketingskills |
| PM Skills | https://github.com/phuryn/pm-skills |
| Scientific Agent Skills | https://github.com/K-Dense-AI/scientific-agent-skills |

条目中的来源和许可证字段优先于本表。来源未声明许可证时，不应根据“公开可见”推定可以复制、修改或再分发其完整内容。

图像工作台的远程提示词目录由 [yukkcat/image-prompts](https://github.com/yukkcat/image-prompts) 聚合，运行时从其 `dist/sources` 读取。各提示词、预览图和来源项目仍归原作者所有；页面中的来源链接用于核对具体条目的作者与许可。

## 协议与产品参考

以下项目用于核对协议行为、数据格式、运维流程或界面交互。除“嵌入源码”明确列出的部分外，本表不表示仓库直接包含其源码。

| 项目 | 地址 | 参考范围 |
| --- | --- | --- |
| Sub2API | https://github.com/Wei-Shaw/sub2api | 账号池、调度、计费、运行监控与 Agent Identity 行为 |
| CLIProxyAPI | https://github.com/router-for-me/CLIProxyAPI | Codex、Claude、OAuth/PAT 和 Responses 兼容行为 |
| New API | https://github.com/QuantumNous/new-api | 第三方中转、模型发现和额度接口格式 |
| One API | https://github.com/songquanpeng/one-api | OpenAI 兼容渠道与额度接口格式 |
| CCSwitch | https://github.com/farion1231/cc-switch | CLI 服务商切换、快速导入和用量提取配置 |
| Aside Music | https://github.com/Lincb522/Aside-music | 早期品牌与界面语言参考 |

这些项目及其作者不因被引用而对 DengDeng AI 提供背书、担保、运营支持或商业合作承诺。

## 品牌资源

OpenAI、Anthropic、Gemini、xAI、Kimi、DeepSeek、智谱 GLM 及其他服务名称、Logo 和商标属于各自权利人。仓库中的平台标识只用于说明兼容目标或账号来源，不表示官方合作。

Kimi、DeepSeek 和 GLM 的资源来源记录在 [frontend/src/assets/provider-logos/sources.json](frontend/src/assets/provider-logos/sources.json) 与 `manifest.json`。组合路由图标和 DengDeng AI 品牌资源为项目自有资源。

## 依赖

主要依赖包括 Go、Gin、GORM、SQLite、PostgreSQL、AWS SDK for Go、Vue、React、Vite、Pinia、Tailwind CSS、Ant Design、Fabric.js、Stripe.js 和 Airwallex Components SDK。完整名称、版本和间接依赖以以下锁定文件为准：

- [backend/go.mod](backend/go.mod)
- [backend/go.sum](backend/go.sum)
- [frontend/package.json](frontend/package.json)
- [frontend/pnpm-lock.yaml](frontend/pnpm-lock.yaml)
- [frontend/workbench/package.json](frontend/workbench/package.json)

二进制、容器或二次发行需要遵守所有适用依赖的许可证、版权通知和源代码提供义务。若本通知与第三方许可证原文冲突，以第三方许可证原文为准。
