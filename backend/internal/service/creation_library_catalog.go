package service

const (
	creationSourceBuiltin   = "builtin"
	creationSourceOfficial  = "official"
	creationSourceCommunity = "community"
)

type creationSkillSource struct {
	author     string
	sourceType string
	url        string
	license    string
}

func creationSkill(id, name, description, content, scope, category string, tags []string, source creationSkillSource) CreationLibraryEntry {
	return CreationLibraryEntry{
		ID: id, Name: name, Description: description, Content: content,
		Scope: scope, Enabled: true, Version: "1.0.0", Author: source.author,
		Category: category, Tags: tags, SourceType: source.sourceType,
		SourceURL: source.url, License: source.license,
	}
}

func defaultCreationSkills() []CreationLibraryEntry {
	builtin := creationSkillSource{author: "DengDeng AI", sourceType: creationSourceBuiltin}
	openAI := func(path string) creationSkillSource {
		return creationSkillSource{author: "OpenAI", sourceType: creationSourceOfficial, url: "https://github.com/openai/plugins/tree/main/" + path}
	}
	anthropic := func(path string) creationSkillSource {
		return creationSkillSource{author: "Anthropic", sourceType: creationSourceOfficial, url: "https://github.com/anthropics/skills/tree/main/skills/" + path, license: "Apache-2.0"}
	}
	anthropicReference := func(path string) creationSkillSource {
		return creationSkillSource{author: "Anthropic", sourceType: creationSourceOfficial, url: "https://github.com/anthropics/skills/tree/main/skills/" + path}
	}
	vercel := func(path string) creationSkillSource {
		return creationSkillSource{author: "Vercel", sourceType: creationSourceOfficial, url: "https://github.com/vercel-labs/agent-skills/tree/main/skills/" + path}
	}
	superpowers := func(path string) creationSkillSource {
		return creationSkillSource{author: "Superpowers", sourceType: creationSourceCommunity, url: "https://github.com/obra/superpowers/tree/main/skills/" + path, license: "MIT"}
	}
	addy := func(path string) creationSkillSource {
		return creationSkillSource{author: "Addy Osmani", sourceType: creationSourceCommunity, url: "https://github.com/addyosmani/agent-skills/tree/main/skills/" + path, license: "MIT"}
	}
	github := func(path string) creationSkillSource {
		return creationSkillSource{author: "GitHub", sourceType: creationSourceOfficial, url: "https://github.com/github/awesome-copilot/tree/main/skills/" + path, license: "MIT"}
	}
	google := func(path string) creationSkillSource {
		return creationSkillSource{author: "Google", sourceType: creationSourceOfficial, url: "https://github.com/google/skills/tree/main/" + path, license: "Apache-2.0"}
	}
	matt := func(path string) creationSkillSource {
		return creationSkillSource{author: "Matt Pocock", sourceType: creationSourceCommunity, url: "https://github.com/mattpocock/skills/tree/main/" + path, license: "MIT"}
	}
	ecc := func(path string) creationSkillSource {
		return creationSkillSource{author: "Everything Claude Code", sourceType: creationSourceCommunity, url: "https://github.com/affaan-m/ECC/tree/main/" + path, license: "MIT"}
	}
	marketing := func(path string) creationSkillSource {
		return creationSkillSource{author: "Marketing Skills", sourceType: creationSourceCommunity, url: "https://github.com/coreyhaines31/marketingskills/tree/main/" + path, license: "MIT"}
	}
	pmSkills := func(path string) creationSkillSource {
		return creationSkillSource{author: "PM Skills", sourceType: creationSourceCommunity, url: "https://github.com/phuryn/pm-skills/tree/main/" + path, license: "MIT"}
	}
	science := func(path string) creationSkillSource {
		return creationSkillSource{author: "K-Dense AI", sourceType: creationSourceCommunity, url: "https://github.com/K-Dense-AI/scientific-agent-skills/tree/main/" + path, license: "MIT"}
	}

	skills := []CreationLibraryEntry{
		creationSkill("code-reviewer", "代码审查", "定位缺陷、回归与安全边界", "以高级代码审查者身份工作。按严重程度列出可复现问题，给出准确位置、影响和最小修复；把已验证缺陷与个人偏好分开。", CreationScopeChat, "开发", []string{"代码", "审查", "安全"}, builtin),
		creationSkill("debugger", "故障诊断", "从日志和现象定位根因", "先建立时间线和已知事实，再列出相互竞争的根因假设。为每个假设设计最小、单变量验证；修复后给出能覆盖原故障的回归检查。", CreationScopeChat, "开发", []string{"排障", "日志"}, builtin),
		creationSkill("backend-engineer", "后端工程", "接口、数据与并发", "以可观察的服务端行为为准，检查接口契约、数据一致性、并发边界、事务、重试幂等和失败恢复。修改应落在拥有该行为的边界。", CreationScopeChat, "开发", []string{"API", "数据库", "并发"}, builtin),
		creationSkill("frontend-engineer", "前端工程", "交互、响应式与可访问性", "以真实渲染和交互为准实现前端。覆盖加载、空态、错误、键盘与移动端；避免横向溢出、裁切和仅靠颜色表达状态。", CreationScopeChat, "开发", []string{"前端", "响应式", "无障碍"}, builtin),
		creationSkill("api-designer", "API 设计", "请求契约与兼容性", "设计或审查 API 时明确鉴权、输入、输出、错误、分页、幂等和版本边界。给出可直接调用的请求示例，并说明破坏性变更。", CreationScopeChat, "开发", []string{"API", "契约"}, builtin),
		creationSkill("data-analyst", "数据分析", "指标、异常与可复现结论", "先确认指标口径、时间范围和样本边界，再处理缺失与异常值。展示计算方法，区分描述事实、相关性和因果推断。", CreationScopeChat, "分析", []string{"数据", "指标"}, builtin),
		creationSkill("research-synthesizer", "资料研究", "归纳来源与分歧", "围绕明确问题整理资料。优先一手来源，记录发布日期和适用范围，比较冲突说法，并把来源事实与推断分开。", CreationScopeChat, "分析", []string{"研究", "来源"}, builtin),
		creationSkill("technical-writer", "技术写作", "文档、说明与发布记录", "面向实际读者编写技术内容。保留准确名称、前置条件、输入输出、限制和示例；删除重复说明、套话和未经验证的宣传。", CreationScopeChat, "写作", []string{"文档", "说明"}, builtin),
		creationSkill("translator", "专业翻译", "保留术语、语气和结构", "保持原意、语气、格式、专有名词和代码标识。优先目标语言的自然表达，不自行增加事实或删减约束；歧义处给出简短说明。", CreationScopeChat, "写作", []string{"翻译", "本地化"}, builtin),
		creationSkill("product-planner", "产品规划", "需求、边界与验收标准", "把产品目标整理为用户行为、业务规则、状态、异常和可观察验收标准。区分必须项与备选项，不用口号替代具体行为。", CreationScopeChat, "产品", []string{"需求", "验收"}, builtin),
		creationSkill("visual-director", "视觉导演", "构图、光线与材质", "控制主体层级、构图、光线、色彩和材质一致性。先锁定视觉重点，再移除抢占注意力且不服务主题的元素。", CreationScopeImage, "视觉", []string{"构图", "光线"}, builtin),
		creationSkill("layout-designer", "版式设计", "网格、层级与文字可读性", "建立明确网格、对齐、层级和留白；指定文字必须完整可读，主要信息优先，装饰不得遮挡内容。", CreationScopeImage, "视觉", []string{"版式", "文字"}, builtin),
		creationSkill("storyboard", "分镜导演", "镜头与连续性", "按镜头组织画面，明确景别、机位、运动、时长和转场，并保持角色、场景、道具与光线连续。", CreationScopeVideo, "视频", []string{"镜头", "连续性"}, builtin),
		creationSkill("audio-director", "音频导演", "语气、节奏与声音层次", "明确发音、语气、速度、停顿和情绪变化；多层声音区分主体、环境和音乐，不让背景掩盖主要内容。", CreationScopeAudio, "音频", []string{"配音", "声音"}, builtin),

		creationSkill("frontend-app-builder", "前端应用构建", "从结构到真实页面的完整实现", "先锁定目标、信息架构、现有设计约束和关键状态，再建立少量可复用的布局与组件。实现加载、空态、错误、交互和响应式；如具备浏览器工具，必须按真实页面逐项核对并修正。", CreationScopeChat, "前端", []string{"界面", "组件", "验证"}, openAI("plugins/build-web-apps/skills/frontend-app-builder")),
		creationSkill("frontend-testing", "前端测试与调试", "用真实交互复现并定位界面问题", "先写出可重复的操作步骤、预期结果和实际结果，再检查控制台、网络请求、DOM 与样式。每次只改变一个变量；修复后复测原路径、边界状态和移动端。", CreationScopeChat, "测试", []string{"浏览器", "调试", "回归"}, openAI("plugins/build-web-apps/skills/frontend-testing-debugging")),
		creationSkill("data-visualization", "数据可视化", "选择能准确表达数据关系的图形", "先明确受众、问题、指标口径和比较关系，再选择图形编码。坐标、单位、时间范围和不确定性必须清楚；避免 3D、误导比例和仅靠颜色区分。输出图表结构、交互和验收项。", CreationScopeChat, "数据", []string{"图表", "指标", "可视化"}, openAI("plugins/build-web-data-visualization/skills/data-visualization")),
		creationSkill("accessible-visualization", "无障碍可视化", "让图表在键盘和辅助技术下可用", "为图表提供可感知标题、摘要、数据表或等价文本。保证键盘可达、焦点可见、对比度足够，颜色之外还有形状或文字编码；动态更新需有可控通知。", CreationScopeChat, "数据", []string{"无障碍", "图表", "键盘"}, openAI("plugins/build-web-data-visualization/skills/accessibility-and-inclusive-visualization")),
		creationSkill("threat-modeling", "威胁建模", "从资产、边界和攻击路径识别风险", "先界定系统、资产、角色、数据流和信任边界，再按攻击前提、路径、影响和现有控制记录威胁。只保留与当前架构相关的风险，并给出可验证的缓解措施与剩余风险。", CreationScopeChat, "安全", []string{"威胁", "架构", "边界"}, openAI("plugins/codex-security/skills/threat-model")),
		creationSkill("security-diff-review", "安全变更审查", "聚焦代码变更带来的安全影响", "先读取变更及其调用方，标出鉴权、输入处理、凭证、数据访问、命令执行和依赖边界的变化。每项结论要包含可达路径、影响、证据和最小修复，不用扫描器告警替代验证。", CreationScopeChat, "安全", []string{"Diff", "鉴权", "审查"}, openAI("plugins/codex-security/skills/security-diff-scan")),
		creationSkill("skill-authoring", "技能编写", "把流程封装成可复用技能", "先定义技能触发条件、输入、输出、可用工具和完成标准。说明可执行步骤、失败恢复和验证方法；只加入完成任务必需的规则，避免把一次性项目细节写成通用技能。", CreationScopeChat, "Agent", []string{"技能", "工作流", "规范"}, openAI(".agents/skills/plugin-creator")),
		creationSkill("ios-debugging", "iOS 调试", "定位构建、运行和界面故障", "先确认 Xcode、SDK、部署目标、scheme 与可复现设备环境。区分编译、链接、启动、网络、并发和布局问题；保留最小运行证据，修复后覆盖最低系统与当前系统路径。", CreationScopeChat, "移动端", []string{"iOS", "Xcode", "调试"}, openAI("plugins/build-ios-apps/skills/ios-debugger-agent")),

		creationSkill("document-coauthoring", "文档共创", "通过提纲和迭代形成可审阅文档", "先确认读者、用途、篇幅和必须覆盖的事实，再共同确定提纲。逐节起草并检查重复、断言依据、术语一致性和行动项；最后从读者视角做一次完整审阅。", CreationScopeChat, "写作", []string{"文档", "提纲", "协作"}, anthropicReference("doc-coauthoring")),
		creationSkill("mcp-server-builder", "MCP 服务构建", "设计稳定、清晰的工具与资源接口", "先把用户任务映射为少量高价值工具或资源，定义输入 schema、权限、分页、错误和幂等。避免把底层 API 原样暴露；给出可运行示例、负面用例和客户端验证步骤。", CreationScopeChat, "Agent", []string{"MCP", "工具", "协议"}, anthropic("mcp-builder")),
		creationSkill("internal-communications", "内部沟通", "编写状态、公告和决策通知", "先确定受众和他们需要采取的行动。用事实说明当前状态、变化、影响、负责人和时间；风险与未知项单独列出，不使用宣传口号或模糊承诺。", CreationScopeChat, "办公", []string{"公告", "状态", "决策"}, anthropic("internal-comms")),
		creationSkill("brand-guidelines", "品牌规范应用", "按既有品牌规则统一视觉与文案", "先提取已有品牌中的标志使用、色彩、字体、间距、图像和语气规则。只在缺少规则时提出最少假设；所有输出保持一致，并标出无法确认的品牌资产。", CreationScopeAll, "品牌", []string{"品牌", "视觉", "文案"}, anthropic("brand-guidelines")),
		creationSkill("algorithmic-art", "生成艺术", "用可控参数构建算法视觉", "先确定视觉系统、随机种子、画布尺寸、色板和参数范围，再描述可复现的生成规则。让变化来自明确参数而非无约束随机；保留主体层级、边界和导出要求。", CreationScopeImage, "视觉", []string{"生成艺术", "参数", "算法"}, anthropic("algorithmic-art")),
		creationSkill("canvas-design", "画布设计", "为海报和静态画布建立完整构图", "根据目标尺寸和观看距离确定网格、视觉焦点、文字层级、留白和出血。所有关键文字完整可读，图像与文字形成一个构图，不把素材随意堆成卡片。", CreationScopeImage, "视觉", []string{"画布", "海报", "构图"}, anthropic("canvas-design")),
		creationSkill("webapp-testing", "Web 应用验收", "覆盖真实任务路径和异常状态", "把需求转成可执行的用户路径，覆盖登录、权限、表单、加载、空态、错误和响应式。记录步骤、环境、证据和结果；失败时缩小到最短复现路径，修复后做针对性回归。", CreationScopeChat, "测试", []string{"Web", "验收", "回归"}, anthropic("webapp-testing")),
		creationSkill("theme-factory", "主题设计系统", "构建可复用且有边界的视觉主题", "从用途与内容密度出发定义背景、表面、文字、强调色、状态色、字体、间距、圆角和阴影。主题必须覆盖明暗、交互、图表和移动端，不只替换主色。", CreationScopeChat, "前端", []string{"主题", "设计系统", "令牌"}, anthropic("theme-factory")),

		creationSkill("react-performance", "React 性能", "按影响定位 React 与 Next.js 性能问题", "先用数据定位瀑布请求、包体、服务端耗时、重复渲染和客户端取数，再处理最高影响项。保持服务端与客户端边界清楚，避免为了理论优化增加复杂度；给出前后指标。", CreationScopeChat, "前端", []string{"React", "Next.js", "性能"}, vercel("react-best-practices")),
		creationSkill("web-design-audit", "Web 设计审计", "审查可用性、可访问性和响应式", "逐项检查语义结构、键盘、焦点、表单、动效、排版、图片、导航状态、暗色模式、触控和国际化。只报告能指向具体界面或代码的发现，并给出可验证修复。", CreationScopeChat, "前端", []string{"UX", "无障碍", "审计"}, vercel("web-design-guidelines")),
		creationSkill("react-native-engineering", "React Native 工程", "兼顾性能、平台差异和原生体验", "先确认目标平台、导航、状态和数据边界。列表、图片、动画和桥接以性能证据为准；保留 iOS 与 Android 的交互差异、无障碍、键盘和安全区行为。", CreationScopeChat, "移动端", []string{"React Native", "移动端", "性能"}, vercel("react-native-skills")),
		creationSkill("writing-guidelines", "产品文案审校", "让说明直接、准确且可执行", "按读者任务审校标题、结构、语气、术语、示例、数字和代码。优先主动语态与直接动作，删除空泛形容和重复帮助文本；错误说明失败原因与下一步。", CreationScopeChat, "写作", []string{"文案", "文档", "审校"}, vercel("writing-guidelines")),

		creationSkill("brainstorming-workflow", "方案探索", "在实现前澄清目标并比较方案", "先从现有约束和证据确认要解决的问题，再提出少量真正不同的方案。逐项比较用户影响、复杂度、风险和验证成本；选定后输出具体决策，不把开放讨论当成完成。", CreationScopeChat, "规划", []string{"方案", "权衡", "决策"}, superpowers("brainstorming")),
		creationSkill("systematic-debugging", "系统化调试", "用证据逐层缩小故障范围", "先稳定复现并采集最接近故障点的证据。沿数据流和调用链定位首次偏离，逐个验证假设；没有根因证据前不叠加修复，修复后运行原复现与相邻回归。", CreationScopeChat, "开发", []string{"根因", "复现", "调试"}, superpowers("systematic-debugging")),
		creationSkill("test-driven-development", "测试驱动开发", "用失败测试固定当前行为边界", "先写能通过真实逻辑复现缺失行为的最小失败测试，再实现使其通过的最小改动。随后整理代码并运行相关测试；不要测试静态定义或仅验证模拟器本身。", CreationScopeChat, "测试", []string{"TDD", "测试", "回归"}, superpowers("test-driven-development")),
		creationSkill("verification-before-completion", "完成前验证", "以最新证据确认交付状态", "在声称完成前运行能直接证明目标的检查，读取完整结果并确认产物实际存在。失败、跳过或旧结果都不能当作通过；未覆盖部分必须明确标记。", CreationScopeChat, "质量", []string{"验证", "交付", "证据"}, superpowers("verification-before-completion")),
		creationSkill("implementation-planning", "实施计划", "把需求拆成可执行、可验证步骤", "基于当前代码和依赖写计划。每步说明修改边界、行为、验证和失败处理，按依赖排序；避免泛化任务、预设不存在的文件或把普通实现细节变成待确认问题。", CreationScopeChat, "规划", []string{"计划", "依赖", "验收"}, superpowers("writing-plans")),
		creationSkill("code-review-response", "审查意见处理", "验证反馈后逐项修正", "逐条理解审查意见，先核对它是否适用于当前代码和契约。对成立的问题修改拥有行为的源头并验证；对不成立或需权衡的意见用证据说明，不机械接受。", CreationScopeChat, "开发", []string{"Review", "反馈", "修复"}, superpowers("receiving-code-review")),
		creationSkill("branch-finishing", "分支收尾", "在合并前清理并确认变更边界", "确认工作树、变更范围、测试和生成产物，清理本次改动产生的临时文件与孤立代码。总结最终行为和未覆盖风险；只有在明确授权时执行提交、推送、合并或部署。", CreationScopeChat, "工程", []string{"Git", "合并", "发布"}, superpowers("finishing-a-development-branch")),

		creationSkill("performance-optimization", "性能优化", "从测量结果定位高影响瓶颈", "先定义用户可感知指标和基线，再用剖析数据定位 CPU、内存、I/O、网络或渲染瓶颈。优先解决最大贡献项，记录前后结果；不做没有证据的微优化。", CreationScopeChat, "工程", []string{"性能", "Profiling", "基线"}, addy("performance-optimization")),
		creationSkill("observability-instrumentation", "可观测性", "设计可定位问题的日志、指标和追踪", "围绕关键请求和业务状态定义结构化日志、指标、追踪与关联 ID。记录有诊断价值的状态转移和失败，避免敏感数据与正常流程噪声；告警必须对应可行动条件。", CreationScopeChat, "运维", []string{"日志", "指标", "追踪"}, addy("observability-and-instrumentation")),
		creationSkill("security-hardening", "安全加固", "按真实攻击面收紧系统边界", "先枚举入口、身份、数据、依赖和部署边界，再验证实际可达风险。按影响和可利用性排序，修复默认配置、权限、输入、凭证和审计问题；保留兼容性与恢复方案。", CreationScopeChat, "安全", []string{"加固", "权限", "审计"}, addy("security-and-hardening")),
		creationSkill("migration-engineering", "迁移工程", "规划兼容、回滚和数据校验", "识别旧新契约、数据形态、调用方和不可逆步骤。设计分阶段迁移、双读或兼容窗口、校验、监控和回滚；每阶段都有进入与退出条件。", CreationScopeChat, "工程", []string{"迁移", "兼容", "回滚"}, addy("deprecation-and-migration")),
		creationSkill("ci-cd-automation", "CI/CD 自动化", "构建可重复且可回退的交付流水线", "明确构建输入、缓存、测试、制品、环境和发布权限。流水线失败要可定位，重试要安全，部署需健康检查和回滚；不要把长期凭证写入代码或日志。", CreationScopeChat, "运维", []string{"CI", "CD", "发布"}, addy("ci-cd-and-automation")),
		creationSkill("source-driven-development", "源码驱动开发", "以当前实现和一手文档为准", "先读取实际调用方、配置、版本和上游一手文档，再选择 API 与实现方式。遇到冲突时以运行契约和当前版本为准，明确记录推断与尚未验证处。", CreationScopeChat, "开发", []string{"源码", "文档", "版本"}, addy("source-driven-development")),
		creationSkill("code-simplification", "代码简化", "在保持行为的前提下降低复杂度", "先确认要保持的外部行为和测试，再删除重复分支、无用抽象和间接层。优先清晰的数据流与一个主要路径，不以更短代码牺牲可读性、错误处理或性能。", CreationScopeChat, "开发", []string{"重构", "复杂度", "清理"}, addy("code-simplification")),

		creationSkill("codebase-knowledge", "代码库理解", "快速建立结构、边界和调用关系", "从入口、模块、配置、数据模型、外部依赖和测试开始建立代码地图。对关键行为沿调用链追到拥有源，区分已读证据与命名推测；输出可供后续任务复用的简明结论。", CreationScopeChat, "开发", []string{"代码库", "调用链", "架构"}, github("acquire-codebase-knowledge")),
		creationSkill("architecture-blueprint", "架构蓝图", "把系统边界和数据流整理为可审阅方案", "描述组件职责、接口、数据流、信任边界、部署拓扑和关键质量属性。每个设计决定说明约束与取舍，并给出渐进实施和验证方式，避免只画无行为含义的方框。", CreationScopeChat, "架构", []string{"架构", "数据流", "决策"}, github("architecture-blueprint-generator")),
		creationSkill("bug-reproduction", "缺陷复现", "把模糊故障转成稳定复现用例", "记录环境、版本、前置状态、最短步骤、预期、实际结果和证据。尝试缩小输入并识别触发条件；若无法稳定复现，明确成功率和缺失信息，不猜测根因。", CreationScopeChat, "测试", []string{"Bug", "复现", "证据"}, github("bug-reproduction-brief")),
		creationSkill("readme-authoring", "README 编写", "为真实使用者编写项目入口文档", "从当前代码和可运行命令提取项目用途、要求、安装、配置、运行、验证和常见故障。示例必须与仓库一致；敏感配置只说明字段，不写入真实值。", CreationScopeChat, "写作", []string{"README", "安装", "使用"}, github("create-readme")),

		creationSkill("gemini-api-integration", "Gemini API 接入", "构建可验证的 Gemini 模型调用", "先确认模型、端点、鉴权和输入输出契约，再实现文本、多模态、流式响应与结构化输出。处理配额、超时和安全设置，使用最小请求验证实际响应。", CreationScopeChat, "云平台", []string{"Gemini", "API", "多模态"}, google("skills/cloud/gemini-api")),
		creationSkill("realtime-multimodal", "实时多模态", "设计低延迟音视频对话链路", "明确会话生命周期、音视频格式、打断、重连和背压策略。分别测量首包、端到端延迟与丢包，并验证客户端在权限拒绝和网络波动下能够恢复。", CreationScopeChat, "云平台", []string{"实时", "音频", "视频"}, google("skills/cloud/gemini-live-api")),
		creationSkill("rag-system-design", "RAG 系统设计", "构建可检索、可评估的知识链路", "先定义语料边界、切分、元数据、索引和权限，再设计召回、重排、引用与无答案处理。用真实问题集评估命中率、答案依据和延迟，避免只验证单个演示样例。", CreationScopeChat, "Agent", []string{"RAG", "检索", "引用"}, google("skills/cloud/agent-platform-rag-engine-management")),
		creationSkill("agent-evaluation", "Agent 评测", "用稳定样本衡量 Agent 行为", "把目标拆成任务成功率、正确性、工具选择、成本、延迟和安全边界。建立固定样本、评分规则和失败分类；版本变更后对比基线并检查退化样例。", CreationScopeChat, "Agent", []string{"评测", "基线", "Agent"}, google("skills/cloud/agent-platform-eval-flywheel")),
		creationSkill("prompt-lifecycle", "提示词生命周期", "管理提示词版本、评测和发布", "为提示词定义输入变量、输出契约、适用模型和版本。修改必须关联评测样本与结果，发布保留回滚路径；把业务规则放在拥有它的配置中，避免散落复制。", CreationScopeChat, "Agent", []string{"提示词", "版本", "评测"}, google("skills/cloud/agent-platform-prompt-management")),
		creationSkill("cloud-cost-review", "云成本审查", "从真实账单和利用率定位浪费", "统一账期、币种、标签和分摊口径，再按服务、环境和团队分析成本。结合利用率验证闲置、规格和承诺折扣机会，给出节省范围、风险与复核方法。", CreationScopeChat, "云平台", []string{"成本", "账单", "优化"}, google("skills/cloud/google-cloud-waf-cost-optimization")),
		creationSkill("cloud-reliability-review", "云可靠性审查", "检查故障域、恢复和容量边界", "从服务目标、依赖、故障域、容量和数据恢复开始审查。为关键故障给出检测、降级、恢复与演练步骤，并用现有监控或测试证明控制是否生效。", CreationScopeChat, "云平台", []string{"可靠性", "容灾", "SLO"}, google("skills/cloud/google-cloud-waf-reliability")),
		creationSkill("cloud-operations-review", "云运维审查", "整理变更、监控与事故响应", "检查部署、配置、告警、值班、日志、追踪和变更记录。告警必须对应行动，运行手册必须能在限定时间内完成定位与恢复，并通过演练验证。", CreationScopeChat, "运维", []string{"运维", "告警", "事故"}, google("skills/cloud/google-cloud-waf-operational-excellence")),
		creationSkill("bigquery-engineering", "BigQuery 数据工程", "设计可维护的分析查询与数据集", "确认表结构、分区、聚簇、权限和成本边界，再编写可复现查询。避免全表扫描和口径漂移；用查询计划、处理字节数和结果抽样验证。", CreationScopeChat, "数据", []string{"BigQuery", "SQL", "数据仓库"}, google("skills/cloud/bigquery-basics")),
		creationSkill("kubernetes-troubleshooting", "Kubernetes 故障排查", "沿工作负载和集群事件定位问题", "从期望状态、Pod 事件、日志、探针、资源、调度、网络和存储逐层排查。先找到首次异常证据，再修改配置；修复后验证滚动发布、容量和失败恢复。", CreationScopeChat, "运维", []string{"Kubernetes", "容器", "排障"}, google("skills/cloud/gke-workload-troubleshooting")),

		creationSkill("domain-modeling", "领域建模", "把业务规则收敛到清晰模型", "从用户行为、术语、状态和不变量提取实体、值对象与边界。模型名称沿用业务语言，规则由一个所有者维护；用关键场景和反例检查边界是否正确。", CreationScopeChat, "架构", []string{"领域模型", "业务规则", "边界"}, matt("skills/engineering/domain-modeling")),
		creationSkill("architecture-improvement", "架构演进", "渐进改善耦合与责任边界", "先用调用关系和变更成本找出真实阻力，再确定目标边界与迁移顺序。每一步保持系统可运行、可回退并有行为验证，不为追求形式一次性重写。", CreationScopeChat, "架构", []string{"架构", "重构", "演进"}, matt("skills/engineering/improve-codebase-architecture")),
		creationSkill("merge-conflict-resolution", "合并冲突处理", "按双方意图恢复正确行为", "分别读取冲突两侧及其相关调用和测试，确认各自要保留的行为。合并后删除冲突标记和重复逻辑，运行覆盖双方变更的验证，不只选择看起来更新的一侧。", CreationScopeChat, "工程", []string{"Git", "冲突", "合并"}, matt("skills/engineering/resolving-merge-conflicts")),
		creationSkill("specification-writing", "技术规格编写", "把目标写成可实现的行为契约", "说明背景、范围、用户路径、数据、接口、状态、错误、权限和验收标准。把未知项与已决定事项分开，确保每项要求都能用代码、测试或运行证据验证。", CreationScopeChat, "产品", []string{"规格", "契约", "验收"}, matt("skills/engineering/to-spec")),
		creationSkill("ticket-breakdown", "任务拆分", "把规格拆成可交付的依赖步骤", "按拥有行为的模块和依赖关系拆分任务，每项包含改动边界、完成条件和验证。避免按文件机械拆分，也不要让多个任务同时拥有同一状态迁移。", CreationScopeChat, "产品", []string{"任务", "拆分", "依赖"}, matt("skills/engineering/to-tickets")),
		creationSkill("rapid-prototyping", "快速原型", "用最小实现验证关键假设", "先确定唯一要验证的用户行为和失败条件，再实现最短可操作路径。使用真实交互和代表性数据观察结果；确认方向后再决定哪些代码保留到正式实现。", CreationScopeChat, "产品", []string{"原型", "验证", "交互"}, matt("skills/engineering/prototype")),
		creationSkill("engineering-handoff", "工程交接", "保存可继续工作的准确上下文", "记录当前目标、已验证状态、未完成项、改动文件、关键命令和阻塞条件。只保留接手者需要继续工作的事实，敏感值不进入交接内容。", CreationScopeChat, "工程", []string{"交接", "上下文", "记录"}, matt("skills/productivity/handoff")),
		creationSkill("technical-teaching", "技术讲解", "按读者基础建立可检验理解", "先确认读者已有知识和目标，用一个核心模型组织说明，再配合最小示例与反例。通过问题或练习检查理解，不用术语堆叠替代推理。", CreationScopeChat, "写作", []string{"教学", "示例", "解释"}, matt("skills/productivity/teach")),

		creationSkill("agent-debugging", "Agent 调试", "追踪提示、工具与状态之间的偏差", "保存输入、上下文、工具参数、工具结果和最终输出的关联记录。定位首次错误决策，区分模型、提示、工具契约和环境故障；用固定样例复测。", CreationScopeChat, "Agent", []string{"Agent", "调试", "追踪"}, ecc(".agents/skills/agent-introspection-debugging")),
		creationSkill("benchmark-design", "基准测试设计", "构建可重复且可比较的性能测试", "明确负载、环境、数据集、预热、并发和统计方法。报告分布而非单次最快值，控制缓存与噪声；变更前后使用同一条件比较。", CreationScopeChat, "测试", []string{"Benchmark", "性能", "统计"}, ecc(".agents/skills/benchmark-methodology")),
		creationSkill("evaluation-harness", "评测体系", "自动执行样例、评分与回归比较", "定义稳定输入集、期望特征、评分器和容差，再记录模型、提示与配置版本。失败样例可单独重放，聚合结果能定位退化类别而不只给总分。", CreationScopeChat, "Agent", []string{"评测", "回归", "样例"}, ecc(".agents/skills/eval-harness")),
		creationSkill("machine-learning-workflow", "机器学习工程", "管理数据、训练、评估与发布链路", "固定数据版本、特征、切分、指标和随机性，防止训练与验证泄漏。记录实验和制品，发布前比较基线、鲁棒性、成本与推理延迟。", CreationScopeChat, "数据", []string{"机器学习", "训练", "实验"}, ecc(".agents/skills/mle-workflow")),
		creationSkill("docker-engineering", "Docker 工程", "构建小型、可复现且安全的镜像", "固定基础镜像与依赖，分离构建和运行阶段，使用非特权用户并减少上下文。验证镜像大小、启动、健康检查、信号处理和运行时配置。", CreationScopeChat, "运维", []string{"Docker", "镜像", "容器"}, ecc(".kiro/skills/docker-patterns")),
		creationSkill("postgresql-engineering", "PostgreSQL 工程", "设计索引、事务与可观测查询", "从访问模式和一致性要求设计表、约束、索引和事务。用执行计划和真实数据量验证查询，迁移必须可回滚并考虑锁与线上负载。", CreationScopeChat, "数据", []string{"PostgreSQL", "SQL", "事务"}, ecc(".kiro/skills/postgres-patterns")),
		creationSkill("go-engineering", "Go 工程", "编写清晰、并发安全的 Go 服务", "遵循现有包边界和接口，显式处理错误、上下文取消与资源释放。并发状态只有一个所有者；使用竞态检测和针对行为的测试验证。", CreationScopeChat, "开发", []string{"Go", "并发", "服务"}, ecc(".kiro/skills/golang-patterns")),
		creationSkill("python-engineering", "Python 工程", "构建可维护、可测试的 Python 代码", "明确运行版本、类型边界、依赖和入口，保持 I/O 与业务逻辑分离。正确处理异常、上下文管理和异步取消，用格式化、类型检查和行为测试验证。", CreationScopeChat, "开发", []string{"Python", "类型", "测试"}, ecc(".kiro/skills/python-patterns")),

		creationSkill("seo-audit", "SEO 审计", "检查可抓取性、内容和页面信号", "从索引状态、站点结构、状态码、canonical、站点地图、结构化数据和性能开始。再核对搜索意图与页面内容；按影响和验证成本排序问题，不承诺排名结果。", CreationScopeChat, "营销", []string{"SEO", "搜索", "审计"}, marketing("skills/seo-audit")),
		creationSkill("content-strategy", "内容策略", "围绕受众问题规划可持续内容", "明确受众、使用场景、搜索或分发渠道和业务目标，再建立主题集群与内容节奏。每项内容有负责人、衡量指标和更新条件，避免只列泛化选题。", CreationScopeChat, "营销", []string{"内容", "渠道", "规划"}, marketing("skills/content-strategy")),
		creationSkill("copy-editing", "文案编辑", "提升准确性、结构和可读性", "先核对事实、受众和行动目标，再修正结构、术语、语法和冗余。保留作者语气，不加入未经证实的卖点；按钮、错误和说明使用一致动作词。", CreationScopeChat, "写作", []string{"文案", "编辑", "校对"}, marketing("skills/copy-editing")),
		creationSkill("pricing-page-strategy", "定价页策略", "清楚表达方案、计费和限制", "基于实际产品规则整理方案差异、计费单位、额度、超额、退款和适用对象。突出用户决策需要的信息，避免虚构折扣、稀缺性或收益承诺。", CreationScopeChat, "营销", []string{"定价", "套餐", "转化"}, marketing("skills/pricing")),
		creationSkill("onboarding-optimization", "新用户引导优化", "缩短从注册到首次成功的路径", "定义首次成功事件和当前漏斗，找出阻塞步骤、错误和等待。每次只验证一个改动，并观察完成率、耗时和后续留存，而不是增加更多提示文本。", CreationScopeChat, "产品", []string{"引导", "漏斗", "激活"}, marketing("skills/onboarding")),
		creationSkill("marketing-attribution", "营销归因", "建立可解释的渠道与转化口径", "统一事件、时间窗、去重和身份关联规则，说明直接、首次、多触点等模型差异。检查数据缺口和隐私边界，结论标明归因模型而非宣称因果。", CreationScopeChat, "营销", []string{"归因", "渠道", "转化"}, marketing("skills/attribution")),
		creationSkill("product-launch", "产品发布", "协调范围、渠道和发布后的验证", "明确受众、发布日期、功能范围、依赖、支持和回滚。发布内容只陈述真实变化；上线后跟踪采用、故障与反馈，并为每项异常指定处理人。", CreationScopeChat, "营销", []string{"发布", "上线", "反馈"}, marketing("skills/launch")),
		creationSkill("customer-research", "客户研究", "从访谈和行为证据理解需求", "先定义研究问题和样本，再使用中立问题获取具体经历、触发、替代方案与结果。分离原话、观察和推断，按重复模式整理结论并说明样本限制。", CreationScopeChat, "产品", []string{"用户研究", "访谈", "需求"}, marketing("skills/customer-research")),

		creationSkill("prd-writing", "PRD 编写", "把产品决策写成可验收要求", "写明用户问题、目标、范围、流程、规则、状态、权限、指标和验收。引用现有证据，标注未决问题与负责人；避免把界面草图当成完整业务规则。", CreationScopeChat, "产品", []string{"PRD", "需求", "验收"}, pmSkills("pm-execution/skills/create-prd")),
		creationSkill("feature-prioritization", "功能优先级", "按目标、证据和成本排序需求", "统一比较维度，包括用户影响、战略关联、证据强度、成本、风险和依赖。展示评分与关键假设，必要时单独处理硬性合规或故障项。", CreationScopeChat, "产品", []string{"优先级", "路线图", "决策"}, pmSkills("pm-product-discovery/skills/prioritize-features")),
		creationSkill("product-premortem", "产品预演", "在执行前识别可能的失败路径", "假设项目已经失败，从需求、技术、数据、运营、依赖和采用逐项寻找具体原因。为高影响且可预防的风险设置负责人、早期信号和缓解动作。", CreationScopeChat, "产品", []string{"风险", "预演", "项目"}, pmSkills("pm-execution/skills/pre-mortem")),
		creationSkill("stakeholder-management", "干系人管理", "明确影响、决策权和沟通节奏", "列出受影响角色、目标、顾虑、决策权和所需信息。针对关键决策安排负责人、材料、时间与升级路径，避免用频繁汇报代替问题解决。", CreationScopeChat, "产品", []string{"干系人", "沟通", "决策"}, pmSkills("pm-execution/skills/stakeholder-map")),
		creationSkill("release-notes", "发布说明", "以用户可理解的方式记录版本变化", "从最终代码和已验证行为提取新增、修复、变化与必要操作。使用准确名称和版本，不写开发过程、未上线内容或空泛宣传；破坏性变化给出迁移步骤。", CreationScopeChat, "写作", []string{"发布日志", "版本", "变更"}, pmSkills("pm-execution/skills/release-notes")),
		creationSkill("ab-test-analysis", "A/B 测试分析", "判断实验结果、效应与不确定性", "核对假设、随机化、样本量、运行周期和指标定义，再计算效应与置信区间。检查分流、污染、多重比较和提前停止，区分统计显著与业务价值。", CreationScopeChat, "数据", []string{"A/B 测试", "实验", "统计"}, pmSkills("pm-data-analytics/skills/ab-test-analysis")),
		creationSkill("cohort-analysis", "留存分群分析", "按共同起点比较用户行为变化", "定义分群事件、时间粒度、留存行为和观察窗，统一处理时区与未成熟周期。展示样本量、留存曲线和关键分层，避免把结构差异直接解释为因果。", CreationScopeChat, "数据", []string{"留存", "分群", "行为"}, pmSkills("pm-data-analytics/skills/cohort-analysis")),
		creationSkill("product-strategy", "产品战略", "连接市场选择、优势与可执行取舍", "基于用户问题、替代方案、能力、约束和证据定义目标市场与价值主张。明确不做什么、成功指标和关键假设，并把战略转换为可排序的行动。", CreationScopeChat, "产品", []string{"战略", "市场", "取舍"}, pmSkills("pm-product-strategy/skills/product-strategy")),

		creationSkill("literature-review", "文献综述", "系统检索并综合研究证据", "先定义研究问题、检索库、关键词、时间范围和纳入标准。记录筛选过程，按方法与结论比较研究，识别一致性、分歧、偏差和证据空白。", CreationScopeChat, "科研", []string{"文献", "检索", "综述"}, science("skills/literature-review")),
		creationSkill("citation-management", "引文管理", "核对来源并保持引用一致", "保存作者、标题、年份、标识符和访问来源，优先核对原始文献。按指定格式生成引用，检查正文与参考文献一一对应，不编造缺失字段。", CreationScopeChat, "科研", []string{"引用", "参考文献", "来源"}, science("skills/citation-management")),
		creationSkill("experimental-design", "实验设计", "设计可检验、可复现的研究方案", "明确假设、变量、对照、随机化、样本量、排除标准和分析计划。识别混杂与测量误差，预先规定停止与重复规则，并保留复现实验所需材料。", CreationScopeChat, "科研", []string{"实验", "假设", "复现"}, science("skills/experimental-design")),
		creationSkill("scientific-peer-review", "学术同行评审", "审查方法、证据和结论边界", "分别评估研究问题、方法、数据、统计、可复现性与结论。指出具体证据位置和影响，区分致命缺陷、可修正问题与表达建议，不用领域偏好替代评审标准。", CreationScopeChat, "科研", []string{"评审", "方法", "证据"}, science("skills/peer-review")),
		creationSkill("exploratory-data-analysis", "探索性数据分析", "检查数据质量、分布和关系", "先确认字段含义、采集过程和样本范围，再检查缺失、异常、重复、分布与分组差异。可视化和统计量保留单位与样本量，把探索发现标记为待验证假设。", CreationScopeChat, "数据", []string{"EDA", "数据质量", "分布"}, science("skills/exploratory-data-analysis")),
		creationSkill("hypothesis-generation", "科学假设生成", "从证据缺口提出可证伪假设", "基于已知机制、观察和文献空白提出少量竞争假设。每项说明预测、可观测变量、反证条件和最小实验，避免把相关描述包装成机制结论。", CreationScopeChat, "科研", []string{"假设", "机制", "实验"}, science("skills/hypothesis-generation")),
	}
	applyCreationSkillEnglish(skills)
	return skills
}
