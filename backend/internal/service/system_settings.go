package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"dengdeng/internal/config"
	"dengdeng/internal/model"

	"gorm.io/gorm"
)

const systemSettingsKey = "system.settings.v1"
const defaultAgreementUpdatedAt = "2026-07-26"
const legacyDefaultAgreementRevision = "a588f1b8267d9bb7"

// LegalDocument is deliberately plain text with optional lightweight heading
// markers. The SPA never renders administrator-authored HTML, preventing legal
// content from becoming an XSS surface while keeping it readable and editable.
type LegalDocument struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ContentMD string `json:"content_md"`
}

type LoginAgreementSettings struct {
	Enabled   bool            `json:"enabled"`
	Mode      string          `json:"mode"` // modal | checkbox
	UpdatedAt string          `json:"updated_at"`
	Documents []LegalDocument `json:"documents"`
}

func (a LoginAgreementSettings) Revision() string {
	payload, _ := json.Marshal(struct {
		UpdatedAt string          `json:"updated_at"`
		Documents []LegalDocument `json:"documents"`
	}{UpdatedAt: a.UpdatedAt, Documents: a.Documents})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])[:16]
}

// SystemSettings holds safe, runtime-editable product settings. Deployment
// secrets (database, SMTP credentials, JWT keys) deliberately remain in the
// environment and are never represented here.
type SystemSettings struct {
	SiteName             string `json:"site_name"`
	SiteSubtitle         string `json:"site_subtitle"`
	AllowRegister        bool   `json:"allow_register"`
	KeyMultiGroupEnabled bool   `json:"key_multi_group_enabled"`
	// RegistrationEmailSuffixes is an optional tenant-style allow-list. An
	// empty list permits all valid email domains; a non-empty list accepts the
	// listed domains and their subdomains only.
	RegistrationEmailSuffixes []string                  `json:"registration_email_suffixes"`
	InitBalanceMicro          int64                     `json:"init_balance_micro"`
	LoginAgreement            LoginAgreementSettings    `json:"login_agreement"`
	TrustedProxies            []string                  `json:"trusted_proxies"`
	ForwardedClientIPHeaders  []string                  `json:"forwarded_client_ip_headers"`
	SiteCustomization         SiteCustomizationSettings `json:"site_customization"`
	Features                  FeatureSwitchSettings     `json:"features"`
	Security                  SecurityPolicySettings    `json:"security"`
	UserDefaults              UserDefaultSettings       `json:"user_defaults"`
	Notifications             NotificationSettings      `json:"notifications"`
	Email                     EmailRuntimeSettings      `json:"email"`
	AuthProviders             AuthProviderSettings      `json:"auth_providers"`
}

type AdminSystemSettings struct {
	SystemSettings
	SitePublicURL    string          `json:"site_public_url"`
	SMTPConfigured   bool            `json:"smtp_configured"`
	SMTPFromName     string          `json:"smtp_from_name"`
	SMTPFrom         string          `json:"smtp_from"`
	SecretConfigured map[string]bool `json:"secret_configured"`
}

type SystemSettingsService struct {
	db  *gorm.DB
	cfg *config.Config
}

func NewSystemSettingsService(db *gorm.DB, cfg *config.Config) *SystemSettingsService {
	return &SystemSettingsService{db: db, cfg: cfg}
}

// Defaults exposes the same compatibility baseline used when no persisted
// settings row exists. It is primarily useful for isolated handlers/tests
// whose minimal schema predates the settings table.
func (s *SystemSettingsService) Defaults() SystemSettings { return s.defaults() }

func defaultLegalDocuments() []LegalDocument {
	return []LegalDocument{
		{
			ID: "terms", Title: "用户协议", ContentMD: `生效日期：2026年7月26日

特别提示

欢迎使用 DengDeng AI（中文名“蹬蹬ai”，以下简称“本服务”）。在注册、登录、充值、创建 API 密钥或调用接口前，请完整阅读本协议、隐私政策、可接受使用政策、服务特定条款及免责声明。你点击同意、完成注册或实际使用本服务，即表示你已理解并接受上述文件。若你不同意其中任何内容，请停止注册和使用。

一、协议主体与适用范围

本协议是服务运营方与你之间关于访问和使用 DengDeng AI 网站、控制台、API 网关、模型转发、密钥管理、用量统计、账户余额、充值、兑换码及相关功能的约定。你通过 SDK、CLI、第三方客户端或自行开发的程序调用本服务，同样受本协议约束。

二、账户注册与身份信息

你应使用本人有权控制的邮箱或获准使用的登录方式注册，并保证所提交信息真实、准确、有效。你不得冒用他人身份、批量注册、交易账户、绕过注册限制或利用自动化手段干扰正常注册流程。未成年人应在监护人阅读并同意本协议后使用；不适合未成年人的功能不得由未成年人使用。

三、账户、密码与 API 密钥安全

账户密码、验证码、会话、API 密钥和上游凭据均属于敏感信息。你应妥善保管并限制访问范围，不得在公开仓库、聊天记录、截图或不可信客户端中泄露。通过你的账户或密钥发起的请求，原则上视为你的操作。发现泄露、异常登录或未经授权的调用时，应立即停用或轮换密钥、修改密码并联系支持人员。

四、服务内容与可用性

本服务提供统一 API 接入、协议兼容、上游账号调度、用量统计、计费、运营监控及相关管理功能。具体开放的模型、上下文长度、输出上限、思考强度、缓存能力、生图能力、并发限制和可用地区，以控制台当时展示及实际上游能力为准。我们可以基于安全、合规、成本、上游变化或维护需要调整、暂停或下线部分功能。

五、余额、充值与计费

账户余额是本服务内部用于抵扣调用费用的记账单位，不是银行存款、电子货币或可自由转让的有价凭证。请求费用根据模型价格、输入与输出 Token、缓存读写、思考档位、图像张数或质量、分组倍率、用户倍率及其他已公示规则计算。最终扣费以服务端成功记录的用量和账单为准。显示价格、倍率或预估费用仅用于帮助理解，不能替代最终账单。

充值到账可能受支付渠道回调、网络延迟或人工核验影响。发生重复扣款、已支付未到账或金额异常时，你应提供订单号和必要凭证供核查。已经实际消耗的模型调用无法撤回，通常不支持退款；未消耗余额的处理依照适用法律、支付渠道规则和运营方公布的退款规则执行。

六、额度、并发与公平使用

你应遵守账户、密钥、分组和上游平台的额度、频率、并发及地区限制。不得通过多账户、多密钥、代理轮换、伪造客户端身份或其他方式规避限流、风控、计费和访问控制。系统可以对异常请求实施排队、限流、冷却、重试、切换账号、暂停密钥或拒绝服务。

七、上游服务与第三方客户端

本服务可能连接 OpenAI、Anthropic、Google、xAI 或其他第三方模型与中转服务。第三方服务的订阅、配额、内容政策、地区限制、账号风控和可用性由相应提供方独立决定。你应自行确认拥有合法、有效的访问授权，并遵守对应平台条款。使用 Chatbox、Claude Code、Codex CLI、CCSwitch 或其他第三方客户端时，还应遵守该客户端的许可证和使用规则。

八、用户行为规范

你不得利用本服务实施违法犯罪、侵犯知识产权或隐私、诈骗、骚扰、传播恶意软件、窃取凭据、攻击系统、规避安全措施、批量滥用资源或其他损害第三方与平台安全的行为。更完整的限制见可接受使用政策。你应对输入内容、生成结果及其后续使用承担责任，并在必要时进行人工审核。

九、知识产权

DengDeng AI 自有代码、界面、标识、文档及其他成果的权利，依适用法律和开源许可证确定。上游模型、第三方软件、商标及生成内容的权利归属，适用相应权利人的规则。使用本服务不代表你取得任何第三方商标、模型、软件或数据的所有权。

十、暂停、限制与终止

如账户存在安全风险、欠费、异常流量、违反协议、收到有效投诉或监管要求，我们可以采取提醒、限制功能、暂停密钥、冻结账户、终止服务和保留必要记录等措施。对明显的紧急安全风险，可先采取保护措施后再通知。你可以停止使用服务；注销、数据导出或余额处理请通过公布的支持渠道提出。

十一、协议变更与通知

我们可以因功能、法律、支付、上游政策或安全要求更新协议，并在网站、登录页或控制台公布新的更新日期。涉及用户重要权益的重大变化将以合理方式提示。更新生效后继续使用服务，视为接受更新内容；若不同意，应停止使用并处理账户余额与数据。

十二、争议与联系

本协议的订立、履行和解释适用运营方所在地可适用的法律。发生争议时，双方应先通过支持渠道友好协商；协商不成的，依有管辖权的法院或法律规定的其他程序处理。联系和投诉方式以网站当前公布的联系方式为准。`,
		},
		{
			ID: "privacy", Title: "隐私政策", ContentMD: `生效日期：2026年7月26日

本政策说明 DengDeng AI 在提供账户、API 网关、模型调用、计费和运营功能时如何处理信息。我们遵循必要、正当、透明和安全的原则，仅在实现明确目的所需范围内处理数据。

一、我们处理的信息

账户信息：邮箱、昵称、登录来源、账户状态、角色、余额和安全设置。

认证与安全信息：密码摘要、验证码状态、会话标识、登录时间、设备与浏览器信息、IP 地址、请求地区、风控记录和审计日志。密码不会以明文保存。

密钥与凭据信息：平台 API 密钥仅保存不可逆摘要；上游 API Key、OAuth 凭据或 Agent Identity 私钥等需要恢复使用的字段会加密保存。创建后展示给你的明文平台密钥可能仅出现一次。

调用与计费信息：请求时间、端点、模型、分组、上游账号标识、Token 用量、缓存用量、图像数量、状态码、首字耗时、总耗时、费用、错误摘要和必要的调度记录。除非功能明确需要或为排查故障而临时启用，我们不以运营分析为目的长期保存完整提示词与完整模型输出。

交易与支持信息：充值订单号、支付状态、渠道流水标识、兑换记录、推广关系、分成记录，以及你主动提交的工单、截图和联系方式。支付卡号、银行卡账户等完整支付凭据通常由持牌支付机构处理，本服务只接收完成交易所需的结果信息。

二、处理目的

我们使用上述信息完成注册登录、身份验证、请求转发、账户调度、用量计费、余额扣减、支付入账、额度查询、故障排查、反滥用、安全审计、客户支持、服务改进和履行法律义务。未经新的合法依据，我们不会将信息用于与这些目的不相容的用途。

三、本地存储与浏览器数据

网站可能在浏览器中保存登录状态、主题偏好、表格设置和你主动选择保存在本机的 API 密钥。保存在本机的密钥不会因为刷新页面自动从服务端恢复。你可以通过退出登录、清除本机密钥或浏览器设置删除这些数据。请勿在公共或不受信任设备上启用本机保存。

四、请求内容与模型提供方

为完成模型调用，你提交的提示词、附件、工具定义和上下文会被发送到所选上游服务或中转服务。上游提供方会依其隐私政策和服务条款独立处理这些内容。请勿提交身份证件、支付信息、商业秘密、医疗记录或其他非必要敏感数据；确需处理时，应先取得合法授权并采用脱敏、加密和最小化措施。

五、信息共享

我们不会以出售个人信息为目的向第三方提供数据。为完成服务，信息可能在必要范围内提供给模型提供商、云服务商、邮件服务商、对象存储、支付渠道、网络与安全服务商。我们也可能在获得你的明确同意、履行法律义务、响应有效执法要求或保护用户和平台安全时披露必要信息。

六、数据保存期限

账户和账务数据会在账户存续、处理争议及履行财税或法律义务所需期限内保存。用量、审计、告警、备份和安全日志按系统配置的保留策略清理。加密备份可能在轮换周期结束后删除。超过目的所需期限的信息将被删除、匿名化或依法封存。

七、安全措施

我们采用访问控制、密码哈希、敏感字段加密、密钥摘要、HTTPS、审计日志、备份和权限隔离等措施降低风险。但任何系统均无法保证绝对安全。若发生可能影响用户权益的安全事件，我们会采取处置措施，并在法律要求时通知相关用户和主管机关。

八、你的权利

在适用法律允许的范围内，你可以请求查询、更正、导出或删除与账户有关的信息，撤回基于同意的处理，或对不准确的账单和记录提出异议。部分数据因安全、账务、反欺诈、争议处理或法定义务无法立即删除。提出请求时，我们可能需要验证身份。

九、未成年人

本服务主要面向具备完全民事行为能力的开发者和组织用户。未成年人应在监护人指导下使用，不应提交敏感个人信息或进行充值。发现未经适当授权收集的未成年人信息后，我们会依法处理。

十、政策更新与联系

我们会因功能、法律或数据处理方式变化更新本政策，并标注新的生效日期。对重要变化会提供合理提示。隐私咨询、数据请求或安全问题请通过网站公布的支持渠道联系。`,
		},
		{
			ID: "usage-policy", Title: "可接受使用政策", ContentMD: `生效日期：2026年7月26日

本政策用于保护用户、上游服务、第三方和 DengDeng AI 的正常运行。你应确保自己以及通过你的账户、密钥或系统接入本服务的人员遵守以下规则。

一、合法合规使用

不得利用本服务实施或协助违法犯罪、诈骗、洗钱、非法交易、侵权、骚扰、歧视、威胁、侵犯隐私或传播法律禁止的内容。不得在缺少合法依据时收集、识别、推断、交易或公开他人的敏感个人信息。

二、系统与网络安全

不得传播恶意软件、钓鱼内容或窃取凭据；不得扫描、攻击、破坏、压测或未经授权访问任何系统；不得干扰服务稳定性、伪造请求来源、注入恶意工具参数或利用漏洞扩大权限。经书面授权的安全测试应限定在明确范围内，并避免影响真实用户和生产数据。

三、账户、订阅与访问限制

不得买卖、出租、共享或批量控制无权使用的账户和订阅。不得绕过模型提供商的地区、年龄、订阅、并发、配额、客户端、内容或风控限制。不得通过伪造设备、客户端身份、请求头、网络位置或组织关系获得本不具备的权限。

四、公平使用与自动化

不得通过批量注册、多账户轮换、多密钥拆分、代理池或其他方式规避计费、限流、冷却和并发保护。自动化调用应设置合理的超时、重试、速率和并发上限，不得制造无意义请求、重试风暴或资源耗尽。未经许可，不得抓取控制台、用户数据、密钥、模型输出或运行指标。

五、内容与知识产权

你应确保有权提交提示词、代码、文档、图片、音视频和数据集，并有权使用和传播生成结果。不得要求模型大规模复现受保护作品、绕过技术保护措施或生成明显用于侵权的内容。发现输出可能侵犯权利时，应停止使用并进行人工核查。

六、高风险用途

将模型用于医疗、法律、金融、信贷、就业、教育录取、关键基础设施、执法、武器或其他可能严重影响个人权益的场景时，必须符合适用法律和上游政策，并建立专业审核、人工决策、可解释记录和申诉机制。不得把未经验证的模型输出作为唯一决策依据。

七、未明确禁止但具有风险的行为

即使某一行为未在本政策逐项列出，只要其明显可能损害平台、上游、用户或公众安全，我们仍可要求停止、降低频率或补充验证。对研究、教育、新闻等具有公共利益的用途，也应遵循必要性、比例性和最小伤害原则。

八、处置与申诉

发现违规、异常调用或紧急安全风险时，我们可以记录证据、限制请求、暂停密钥、冻结账户、取消未使用权益、终止服务或依法配合调查。处置将尽量与风险程度相匹配。你认为判断有误时，可通过支持渠道提交账户、时间、请求编号和说明申请复核。`,
		},
		{
			ID: "service-specific-terms", Title: "服务特定条款", ContentMD: `生效日期：2026年7月26日

本条款针对模型中转、跨协议兼容、账号池调度、图像生成、OAuth 与第三方客户端等功能作补充说明。如与用户协议的一般条款不一致，以对具体功能规定更明确且不违反适用法律的内容为准。

一、模型目录与名称

控制台展示的模型名称可能是对外别名，并不保证与上游产品名称、版本或发布日期完全一致。模型上下文、最大输出、知识截止时间、推理能力和多模态能力来自配置或上游信息，可能因账号类型、地区、灰度发布和上游调整而变化。生产系统应以实际响应和官方资料进行验证。

二、跨协议兼容

本服务支持部分 OpenAI、Anthropic、Gemini 和 xAI 兼容端点，并在明确支持的组合中转换消息、工具调用、流式事件和用量字段。兼容层无法保证所有 SDK、客户端、实验参数、响应扩展或错误格式完全一致。跨协议转换可能丢失上游独有字段，工具调用和结构化输出应在接入前进行完整测试。

三、流式响应、缓存与 Token

首字耗时、总耗时、输入输出 Token、缓存创建和缓存读取等数据，优先采用上游返回值并按兼容规则归一化。不同上游对系统提示、工具定义、图像、推理 Token 和缓存的统计口径可能不同。客户端估算、控制台记录与上游账单之间可能出现合理差异，结算以服务端实际记录和已公示规则为准。

四、思考强度与快速模式

客户端显式提交的思考强度通常优先于密钥默认值。不同模型支持的档位不同，不支持的参数可能被归一化、忽略或拒绝。思考强度、快速模式和服务等级可能影响费用、延迟和输出长度，具体倍率以调用时生效的配置为准。

五、上游账号与调度

账号池会根据平台、分组、模型、优先级、并发、健康状态、额度和冷却时间选择上游。发生可重试错误时，系统可能切换账号或同平台分组。切换不能保证请求一定成功，也不能消除上游封禁、订阅失效、地区限制、会话关联和内容策略风险。你提供的上游账号必须来源合法并获准用于相应调用。

六、OAuth、PAT 与 Agent Identity

OAuth、个人访问令牌和 Agent Identity 是不同凭证类型，不能在未明确支持时互换。导入凭证前应确认来源、权限范围和有效期。浏览器登录回调、刷新令牌、运行时身份和签名任务可能依赖上游接口，任何上游变更都可能导致授权失效。不得导入盗取、共享或未经授权的凭据。

七、API Key 类型的第三方中转

当上游账号指向第三方中转站时，余额、模型和额度查询依赖第三方公开接口或管理员配置。查询结果可能延迟、不完整或与最终账单不同。本服务不会替第三方承诺余额真实性、价格、服务质量或数据处理方式。你应独立评估第三方的信誉、协议和隐私风险。

八、图像与多媒体

图像生成和编辑可能按张数、尺寸、质量、模型、请求次数或上游实际消耗计费。异步任务可能因超时、上游排队、内容审核或对象存储失败而终止。生成内容可能包含事实错误、瑕疵、相似元素或权利风险；发布、商用或用于人物识别前必须人工审核并取得必要授权。

九、第三方客户端与快速配置

快速配置仅根据当前站点地址、密钥、平台和模型生成示例。你应在写入 Claude Code、Codex CLI、Gemini CLI、Chatbox、CCSwitch、IDE 插件或其他工具前备份原配置并核对路径。配置中包含密钥，不得提交到公开仓库。第三方客户端更新可能导致配置格式失效，本服务不对其兼容性作永久承诺。

十、生产接入责任

生产系统必须自行设置连接与读取超时、有界重试、指数退避、幂等、熔断、日志脱敏、余额告警和降级方案。不得假设 HTTP 200 一定包含有效内容，也不得对 401、402、413、429、5xx 或网络中断进行无限重试。重要业务应准备替代提供方并保留人工处置能力。`,
		},
		{
			ID: "disclaimer", Title: "免责声明", ContentMD: `生效日期：2026年7月26日

请在使用 DengDeng AI 前阅读本免责声明。除适用法律不得排除或限制的责任外，使用本服务产生的技术、业务、账户和合规风险由使用者结合自身场景独立评估。

一、按现状提供

本服务按“现状”和“可用”基础提供。我们会合理维护系统，但不承诺服务永久可用、无中断、无延迟、无漏洞、无错误或完全满足特定目的，也不承诺任何模型、价格、倍率、分组、支付渠道、上游账号或第三方客户端长期保持不变。

二、上游平台风险

模型能力、订阅权益、账号状态、地区限制、内容审核、速率限制和服务条款由相应上游提供方决定。上游可能在没有提前通知本服务的情况下修改接口、降低额度、拒绝请求、暂停账号或终止服务。本服务无法保证上游账号不会被限制、风控或封禁，也不保证 OAuth、PAT、API Key 或 Agent Identity 始终有效。

三、模型输出风险

模型输出可能不准确、不完整、过时、具有偏见、包含虚构信息或与输入无关。代码可能存在漏洞，图片可能存在瑕疵或权利争议。你必须在使用、执行、发布或据此决策前进行人工验证。任何模型输出均不构成法律、医疗、金融、投资、税务、心理、就业或其他专业意见。

四、兼容性与网络风险

协议转换、流式传输、代理、CDN、浏览器、SDK 和第三方客户端可能造成字段差异、乱码、空响应、截断、超时、重复请求或统计偏差。你应自行建立超时、重试上限、幂等、校验、备份和降级机制。因本地网络、代理、DNS、证书、反向代理或客户端配置导致的问题，不视为本服务对可用性的承诺。

五、费用与数据风险

自动化程序、长上下文、工具定义、缓存创建、思考 Token、重试和高质量图像可能产生超出预期的费用。你应设置密钥额度、并发限制和余额告警并持续检查账单。请勿提交无备份的数据或不必要的敏感信息。因配置错误、密钥泄露、误操作、客户端重试或未及时停用密钥造成的费用和数据损失，应由账户控制者承担。

六、第三方服务

本服务中出现的第三方项目、商标、链接、客户端和支付服务仅用于兼容、说明或致谢，不代表其对 DengDeng AI 的认可、担保或合作承诺。访问第三方服务前应独立阅读其条款和隐私政策。第三方的内容、交易、停运、安全事件和争议由相应主体负责。

七、责任限制

在适用法律允许的最大范围内，运营方不对因无法使用服务、上游中断、模型错误、账号限制、数据丢失、利润损失、商誉损失、业务中断或第三方索赔造成的间接、附带、特殊、惩罚性或后果性损失承担责任。若法律要求承担责任，责任范围和金额应依实际过错、可预见性、因果关系及强制性法律规定确定。

八、使用者责任

你应确保使用本服务的方式合法、获得必要授权，并对输入、输出、部署环境、终端用户、账单和安全措施负责。因你违反法律、第三方权利、上游条款或本协议造成投诉、调查、损失或索赔的，应依法承担相应责任。

九、免责声明的边界

本免责声明不排除因故意或重大过失造成损害时依法不得排除的责任，不限制消费者依法享有且不能放弃的权利，也不影响用户协议中关于退款、争议处理和数据保护的具体约定。`,
		},
		{
			ID: "open-source", Title: "开源软件说明", ContentMD: `生效日期：2026年7月26日

一、项目许可证

DengDeng AI 的开源代码依据 GNU Lesser General Public License Version 3，或任何更高版本发布，简称 LGPL-3.0-or-later。完整许可证文本以代码仓库根目录 LICENSE 文件为准。许可证授予的是著作权范围内的复制、修改和分发许可，不是对站点服务、商标、域名、上游账号、付费额度、用户数据或第三方内容的授权。

二、获取源代码

你可以通过项目公开仓库获取相应版本的源代码、构建文件和修改记录。分发修改版本、二进制或组合程序时，应保留版权与许可证通知，并按照 LGPL-3.0-or-later 及其中引用的 GNU GPL 条款履行提供对应源码、允许修改与重新链接等义务。具体合规方式应以许可证原文为准。

三、商标与站点服务

“DengDeng AI”“蹬蹬ai”、图标、域名和站点视觉标识不因代码开源而自动授予商标使用权。公开部署修改版本时，不应造成由原项目运营方提供、认证或担保的误解。站点充值、余额、上游资源和客户支持属于具体运营服务，不包含在开源许可证授权中。

四、第三方组件

项目依赖 Go、Gin、GORM、Vue、Vite、Pinia、Tailwind CSS、SQLite、PostgreSQL、AWS SDK 等第三方软件，并参考或兼容 Sub2API、CLIProxyAPI、New API、One API、CCSwitch 及其他社区项目。各项目的代码、名称和商标分别适用其原始许可证与权利声明。DengDeng AI 的许可证不会替代或缩减第三方许可证要求。

五、参考与致谢

Sub2API 为账号池、调度、计费、Agent Identity 和运营能力提供了重要参考；CLIProxyAPI 为 Codex、Claude 与兼容协议行为提供了实现参考；New API 与 One API 为第三方中转额度和接口兼容提供了参考；Aside Music 为早期站点布局与暖色视觉风格提供了设计参考。感谢上述项目的作者、维护者和贡献者。引用和兼容不代表这些项目对 DengDeng AI 提供背书。

六、无担保

开源代码同样按许可证规定以无担保方式提供。使用、修改或分发代码前，请自行评估安全、合规、数据保护和上游平台条款。如许可证说明与 LICENSE 原文存在差异，以 LICENSE 原文为准。`,
		},
	}
}

func upgradeLegacyDefaultAgreement(agreement *LoginAgreementSettings) {
	if agreement == nil || agreement.Revision() != legacyDefaultAgreementRevision {
		return
	}
	agreement.UpdatedAt = defaultAgreementUpdatedAt
	agreement.Documents = defaultLegalDocuments()
}

func (s *SystemSettingsService) defaults() SystemSettings {
	name := "DengDeng AI · 蹬蹬ai"
	allowRegister := true
	initBalance := int64(0)
	trustedProxies := []string{}
	forwardedHeaders := []string{"X-Forwarded-For", "X-Real-IP"}
	if s.cfg != nil {
		if strings.TrimSpace(s.cfg.Site.Name) != "" {
			name = strings.TrimSpace(s.cfg.Site.Name)
		}
		allowRegister = s.cfg.Site.AllowRegister
		initBalance = s.cfg.Site.InitBalanceMicro
		trustedProxies = append([]string(nil), s.cfg.Server.TrustedProxies...)
		if len(s.cfg.Server.ForwardedClientIPHeaders) > 0 {
			forwardedHeaders = append([]string(nil), s.cfg.Server.ForwardedClientIPHeaders...)
		}
	}
	siteCustomization, features, security, userDefaults, notifications, email, authProviders := defaultExtendedSystemSettings()
	if s.cfg != nil {
		if value := strings.TrimSpace(s.cfg.SMTP.Host); value != "" {
			email.Host = value
		}
		if s.cfg.SMTP.Port > 0 {
			email.Port = s.cfg.SMTP.Port
		}
		if value := strings.TrimSpace(s.cfg.SMTP.User); value != "" {
			email.Username = value
		}
		if value := strings.TrimSpace(s.cfg.SMTP.FromName); value != "" {
			email.FromName = value
		}
		if value := strings.TrimSpace(s.cfg.SMTP.From); value != "" {
			email.From = value
		}
		if s.cfg.SMTP.Host != "" {
			email.UseTLS = s.cfg.SMTP.Secure
		}
	}
	userDefaults.BalanceMicro = initBalance
	return SystemSettings{
		SiteName:                 name,
		SiteSubtitle:             "统一管理模型接入与用量",
		AllowRegister:            allowRegister,
		KeyMultiGroupEnabled:     true,
		InitBalanceMicro:         initBalance,
		TrustedProxies:           trustedProxies,
		ForwardedClientIPHeaders: forwardedHeaders,
		SiteCustomization:        siteCustomization,
		Features:                 features,
		Security:                 security,
		UserDefaults:             userDefaults,
		Notifications:            notifications,
		Email:                    email,
		AuthProviders:            authProviders,
		LoginAgreement: LoginAgreementSettings{
			Enabled: true, Mode: "modal", UpdatedAt: defaultAgreementUpdatedAt, Documents: defaultLegalDocuments(),
		},
	}
}

func normalizeDocumentID(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *SystemSettingsService) normalize(next SystemSettings) (SystemSettings, error) {
	next.SiteName = strings.TrimSpace(next.SiteName)
	next.SiteSubtitle = strings.TrimSpace(next.SiteSubtitle)
	// InitBalanceMicro is the legacy registration-balance field. Keep the two
	// representations in lockstep so settings saved by an older frontend cannot
	// leave the newer registration path reading a zero-value UserDefaults field.
	if next.UserDefaults.BalanceMicro == 0 && next.InitBalanceMicro > 0 {
		next.UserDefaults.BalanceMicro = next.InitBalanceMicro
	} else {
		next.InitBalanceMicro = next.UserDefaults.BalanceMicro
	}
	if next.SiteName == "" || len([]rune(next.SiteName)) > 120 {
		return SystemSettings{}, errors.New("site name must be between 1 and 120 characters")
	}
	if len([]rune(next.SiteSubtitle)) > 240 {
		return SystemSettings{}, errors.New("site subtitle must be at most 240 characters")
	}
	if next.InitBalanceMicro < 0 || next.InitBalanceMicro > 1_000_000_000_000 {
		return SystemSettings{}, errors.New("initial balance is out of range")
	}
	seenSuffixes := make(map[string]struct{}, len(next.RegistrationEmailSuffixes))
	suffixes := make([]string, 0, len(next.RegistrationEmailSuffixes))
	for _, raw := range next.RegistrationEmailSuffixes {
		suffix := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "@")
		if suffix == "" {
			continue
		}
		if len(suffix) > 253 || strings.ContainsAny(suffix, "@ /\\\t\r\n") || !strings.Contains(suffix, ".") {
			return SystemSettings{}, errors.New("registration email suffixes must be domain names")
		}
		if _, duplicate := seenSuffixes[suffix]; duplicate {
			continue
		}
		seenSuffixes[suffix] = struct{}{}
		suffixes = append(suffixes, suffix)
	}
	if len(suffixes) > 64 {
		return SystemSettings{}, errors.New("at most 64 registration email suffixes are allowed")
	}
	next.RegistrationEmailSuffixes = suffixes

	proxies := make([]string, 0, len(next.TrustedProxies))
	proxySeen := make(map[string]struct{}, len(next.TrustedProxies))
	for _, raw := range next.TrustedProxies {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if len(value) > 128 {
			return SystemSettings{}, errors.New("trusted proxy is too long")
		}
		if ip := net.ParseIP(value); ip == nil {
			if _, _, err := net.ParseCIDR(value); err != nil {
				return SystemSettings{}, errors.New("trusted proxies must be IP addresses or CIDR ranges")
			}
		}
		if _, ok := proxySeen[value]; !ok {
			proxySeen[value] = struct{}{}
			proxies = append(proxies, value)
		}
	}
	if len(proxies) > 64 {
		return SystemSettings{}, errors.New("at most 64 trusted proxies are allowed")
	}
	next.TrustedProxies = proxies

	headers := make([]string, 0, len(next.ForwardedClientIPHeaders))
	headerSeen := make(map[string]struct{}, len(next.ForwardedClientIPHeaders))
	for _, raw := range next.ForwardedClientIPHeaders {
		name := http.CanonicalHeaderKey(strings.TrimSpace(raw))
		if name == "" || strings.ContainsAny(name, " :\t\r\n") {
			return SystemSettings{}, errors.New("forwarded client IP header is invalid")
		}
		if _, ok := headerSeen[name]; !ok {
			headerSeen[name] = struct{}{}
			headers = append(headers, name)
		}
	}
	if len(headers) == 0 || len(headers) > 8 {
		return SystemSettings{}, errors.New("between 1 and 8 forwarded client IP headers are required")
	}
	next.ForwardedClientIPHeaders = headers
	next.Security.ForwardedIPHeaders = append([]string(nil), headers...)
	if err := normalizeExtendedSystemSettings(&next); err != nil {
		return SystemSettings{}, err
	}

	a := &next.LoginAgreement
	if a.Mode != "checkbox" {
		a.Mode = "modal"
	}
	a.UpdatedAt = strings.TrimSpace(a.UpdatedAt)
	if a.UpdatedAt == "" {
		a.UpdatedAt = defaultAgreementUpdatedAt
	}
	if len(a.UpdatedAt) > 32 {
		return SystemSettings{}, errors.New("agreement update date is too long")
	}
	seen := make(map[string]struct{}, len(a.Documents))
	docs := make([]LegalDocument, 0, len(a.Documents))
	for i, doc := range a.Documents {
		doc.ID = normalizeDocumentID(doc.ID)
		if doc.ID == "" {
			doc.ID = fmt.Sprintf("document-%d", i+1)
		}
		doc.Title = strings.TrimSpace(doc.Title)
		doc.ContentMD = strings.TrimSpace(doc.ContentMD)
		if doc.Title == "" || len([]rune(doc.Title)) > 64 {
			return SystemSettings{}, errors.New("each agreement document needs a title of at most 64 characters")
		}
		if len([]rune(doc.ContentMD)) > 16_000 {
			return SystemSettings{}, errors.New("agreement document is too long")
		}
		if _, duplicate := seen[doc.ID]; duplicate {
			return SystemSettings{}, errors.New("agreement document IDs must be unique")
		}
		seen[doc.ID] = struct{}{}
		docs = append(docs, doc)
	}
	if a.Enabled && len(docs) == 0 {
		return SystemSettings{}, errors.New("enable at least one agreement document")
	}
	a.Documents = docs
	return next, nil
}

func (s SystemSettings) AllowsRegistrationEmail(email string) bool {
	if len(s.RegistrationEmailSuffixes) == 0 {
		return true
	}
	parts := strings.Split(strings.ToLower(strings.TrimSpace(email)), "@")
	if len(parts) != 2 || parts[1] == "" {
		return false
	}
	domain := parts[1]
	for _, suffix := range s.RegistrationEmailSuffixes {
		if domain == suffix || strings.HasSuffix(domain, "."+suffix) {
			return true
		}
	}
	return false
}

func (s *SystemSettingsService) Get() (SystemSettings, error) {
	defaults := s.defaults()
	var record model.Setting
	err := s.db.First(&record, "key = ?", systemSettingsKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return defaults, nil
	}
	if err != nil {
		return SystemSettings{}, err
	}
	next := defaults
	if err := json.Unmarshal([]byte(record.Value), &next); err != nil {
		return SystemSettings{}, fmt.Errorf("decode system settings: %w", err)
	}
	upgradeLegacyDefaultAgreement(&next.LoginAgreement)
	return s.normalize(next)
}

func (s *SystemSettingsService) Update(next SystemSettings) (SystemSettings, error) {
	return s.UpdateAll(next, SystemSecretUpdate{})
}

// UpdateAll persists public settings and encrypted secrets in one transaction,
// so a failed secret write can never leave the visible settings half-applied.
func (s *SystemSettingsService) UpdateAll(next SystemSettings, secrets SystemSecretUpdate) (SystemSettings, error) {
	next, err := s.normalize(next)
	if err != nil {
		return SystemSettings{}, err
	}
	secretWillBeConfigured := func(secretName string) bool {
		configured := strings.TrimSpace(secrets.Values[secretName]) != ""
		for _, name := range secrets.Clear {
			if name == secretName {
				return false
			}
		}
		if !configured {
			current, secretErr := s.Secret(secretName)
			configured = secretErr == nil && strings.TrimSpace(current) != ""
		}
		return configured
	}
	if next.Security.TurnstileEnabled {
		if !secretWillBeConfigured(SecretTurnstile) {
			return SystemSettings{}, errors.New("Turnstile secret is required when Turnstile is enabled")
		}
	}
	providers := []struct {
		name, secret string
		config       OAuthProviderSettings
	}{
		{"LinuxDO", SecretLinuxDOOAuth, next.AuthProviders.LinuxDO}, {"DingTalk", SecretDingTalkOAuth, next.AuthProviders.DingTalk},
		{"WeChat", SecretWeChatOAuth, next.AuthProviders.WeChat}, {"OIDC", SecretOIDCOAuth, next.AuthProviders.OIDC},
		{"GitHub", SecretGitHubOAuth, next.AuthProviders.GitHub}, {"Google", SecretGoogleOAuth, next.AuthProviders.Google},
	}
	for _, provider := range providers {
		if !provider.config.Enabled {
			continue
		}
		if provider.config.ClientID == "" || !secretWillBeConfigured(provider.secret) {
			return SystemSettings{}, fmt.Errorf("%s OAuth client ID and secret are required", provider.name)
		}
		if provider.config.RedirectURL == "" && (s.cfg == nil || strings.TrimSpace(s.cfg.Site.PublicURL) == "") {
			return SystemSettings{}, fmt.Errorf("%s OAuth redirect URL is required when the public site URL is empty", provider.name)
		}
		if provider.config.AuthorizeURL == "" || provider.config.TokenURL == "" || provider.config.UserInfoURL == "" {
			if provider.config.DiscoveryURL == "" && provider.config.IssuerURL == "" {
				return SystemSettings{}, fmt.Errorf("%s OAuth endpoints or discovery URL are required", provider.name)
			}
		}
	}
	{
		uniqueIDs := map[int64]struct{}{}
		for _, item := range next.UserDefaults.DefaultSubscriptions {
			uniqueIDs[item.GroupID] = struct{}{}
		}
		for _, source := range next.UserDefaults.AuthSourceDefaults {
			for _, item := range source.DefaultSubscriptions {
				uniqueIDs[item.GroupID] = struct{}{}
			}
		}
		ids := make([]int64, 0, len(uniqueIDs))
		for id := range uniqueIDs {
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			var count int64
			if err := s.db.Model(&model.Group{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
				return SystemSettings{}, err
			}
			if count != int64(len(ids)) {
				return SystemSettings{}, errors.New("one or more default subscription groups do not exist")
			}
		}
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return SystemSettings{}, err
	}
	if len(raw) > 256_000 {
		return SystemSettings{}, errors.New("system settings are too large")
	}
	record := model.Setting{Key: systemSettingsKey, Value: string(raw)}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		if err := s.updateSecretsTx(tx, secrets); err != nil {
			return err
		}
		if !next.KeyMultiGroupEnabled {
			return collapseAPIKeyGroupBindings(tx)
		}
		return nil
	}); err != nil {
		return SystemSettings{}, err
	}
	return next, nil
}

// collapseAPIKeyGroupBindings makes disabling the multi-group feature take
// effect immediately. The legacy group_id column is the stable primary group,
// so it is preserved while every extra many-to-many binding is removed.
func collapseAPIKeyGroupBindings(tx *gorm.DB) error {
	var keys []model.APIKey
	if err := tx.Select("id", "group_id").Find(&keys).Error; err != nil {
		return err
	}
	for _, key := range keys {
		if key.GroupID == 0 {
			continue
		}
		if err := tx.Where("api_key_id = ?", key.ID).Delete(&model.APIKeyGroup{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.APIKeyGroup{APIKeyID: key.ID, GroupID: key.GroupID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *SystemSettingsService) AdminView() (AdminSystemSettings, error) {
	settings, err := s.Get()
	if err != nil {
		return AdminSystemSettings{}, err
	}
	view := AdminSystemSettings{SystemSettings: settings, SecretConfigured: s.SecretConfigured()}
	view.SMTPConfigured = settings.Email.Host != "" && settings.Email.Port > 0 && settings.Email.Username != "" && view.SecretConfigured[SecretSMTPPassword]
	view.SMTPFromName = settings.Email.FromName
	view.SMTPFrom = settings.Email.From
	if s.cfg != nil {
		view.SitePublicURL = s.cfg.Site.PublicURL
	}
	return view, nil
}
