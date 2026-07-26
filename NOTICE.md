# DengDeng AI 第三方通知与致谢

Copyright © 2026 DengDeng AI contributors.

DengDeng AI 依据 GNU Lesser General Public License Version 3 或任何更高版本发布。完整条款见仓库根目录的 [LICENSE](LICENSE)。

## 主要参考项目

本项目在架构设计、协议兼容和产品交互方面参考了以下开源项目。相应项目的代码、文档、名称、图标和商标仍归各自权利人所有，并适用各自仓库中声明的许可证。

| 项目 | 地址 | 参考范围 |
| --- | --- | --- |
| Sub2API | https://github.com/Wei-Shaw/sub2api | 账号池、调度、计费、运营监控、Agent Identity 与网关能力 |
| CLIProxyAPI | https://github.com/router-for-me/CLIProxyAPI | Codex / Claude 协议、OAuth / PAT 与 Responses 兼容行为 |
| New API | https://github.com/QuantumNous/new-api | 第三方中转、模型目录与额度接口兼容思路 |
| One API | https://github.com/songquanpeng/one-api | OpenAI 兼容中转与渠道生态 |
| CCSwitch | https://github.com/farion1231/cc-switch | CLI 服务商切换、快速导入与用量查询配置 |
| Aside Music | https://github.com/Lincb522/Aside-music | 早期页面布局、暖色视觉语言与品牌界面参考 |

## 主要依赖

项目还使用 Go、Gin、GORM、Vue、Vite、Pinia、Tailwind CSS、SQLite、PostgreSQL、AWS SDK for Go 及其他依赖。完整依赖及其版本以 `backend/go.mod`、`backend/go.sum`、`frontend/package.json` 和 `frontend/pnpm-lock.yaml` 为准。

分发本项目的源码、二进制或容器镜像时，请保留本通知、DengDeng AI 的许可证文件以及第三方依赖要求保留的版权和许可证文本。若本通知与任一第三方许可证冲突，以相应许可证原文为准。

上述项目与其作者不因本项目的引用、兼容或致谢而对 DengDeng AI 提供背书、担保、运营支持或商业合作承诺。
