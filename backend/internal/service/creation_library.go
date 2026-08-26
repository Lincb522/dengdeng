package service

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"dengdeng/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	CreationScopeAll          = "all"
	CreationScopeChat         = "chat"
	CreationScopeImage        = "image"
	CreationScopeVideo        = "video"
	CreationScopeAudio        = "audio"
	creationCatalogVersion    = 5
	creationLibraryMaxEntries = 256
)

type CreationCapabilitySettings struct {
	Prompts bool `json:"prompts"`
	Rules   bool `json:"rules"`
	Skills  bool `json:"skills"`
	Chat    bool `json:"chat"`
	Image   bool `json:"image"`
	Video   bool `json:"video"`
	Audio   bool `json:"audio"`
}

type CreationLibraryEntry struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	NameEN         string   `json:"name_en,omitempty"`
	Description    string   `json:"description"`
	DescriptionEN  string   `json:"description_en,omitempty"`
	Content        string   `json:"content"`
	ContentEN      string   `json:"content_en,omitempty"`
	Scope          string   `json:"scope"`
	Enabled        bool     `json:"enabled"`
	AutoApply      bool     `json:"auto_apply"`
	Version        string   `json:"version,omitempty"`
	Author         string   `json:"author,omitempty"`
	Category       string   `json:"category,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	SourceType     string   `json:"source_type,omitempty"`
	SourceURL      string   `json:"source_url,omitempty"`
	InstallCommand string   `json:"install_command,omitempty"`
	License        string   `json:"license,omitempty"`
}

type CreationLibrarySettings struct {
	Enabled        bool                       `json:"enabled"`
	CatalogVersion int                        `json:"catalog_version"`
	Capabilities   CreationCapabilitySettings `json:"capabilities"`
	Prompts        []CreationLibraryEntry     `json:"prompts"`
	Rules          []CreationLibraryEntry     `json:"rules"`
	Skills         []CreationLibraryEntry     `json:"skills"`
}

type UserCreationLibrary struct {
	CreationLibrarySettings
	SelectedRuleIDs  []string `json:"selected_rule_ids"`
	SelectedSkillIDs []string `json:"selected_skill_ids"`
}

type UserCreationSelection struct {
	RuleIDs  []string `json:"rule_ids"`
	SkillIDs []string `json:"skill_ids"`
}

func defaultCreationLibrarySettings() CreationLibrarySettings {
	return CreationLibrarySettings{
		Enabled:        true,
		CatalogVersion: creationCatalogVersion,
		Capabilities: CreationCapabilitySettings{
			Prompts: true, Rules: true, Skills: true,
			Chat: true, Image: true, Video: true, Audio: true,
		},
		Prompts: []CreationLibraryEntry{
			{ID: "code-review", Name: "代码审查", Description: "检查缺陷、边界和回归风险", Content: "审查下面的代码。先列出可复现的问题，再给出最小修改方案；区分已验证的问题和需要补充证据的判断。", Scope: CreationScopeChat, Enabled: true, Category: "开发", Tags: []string{"代码", "审查"}},
			{ID: "root-cause", Name: "故障排查", Description: "从现象定位根因", Content: "根据现象、日志和最近变更定位根因。按已知事实、待验证假设、验证步骤和修复方案输出。", Scope: CreationScopeChat, Enabled: true, Category: "开发", Tags: []string{"排障", "日志"}},
			{ID: "structured-summary", Name: "结构化整理", Description: "整理长文本和记录", Content: "整理下面的内容，保留关键数据、决定、负责人和未解决事项。不要补写原文没有的信息。", Scope: CreationScopeChat, Enabled: true, Category: "办公", Tags: []string{"总结", "整理"}},
			{ID: "product-image", Name: "商品主图", Description: "干净的商品视觉", Content: "生成一张商品主图：主体完整，结构与材质准确，光线自然，背景简洁，不添加未要求的文字或标志。", Scope: CreationScopeImage, Enabled: true, Category: "图像", Tags: []string{"商品", "摄影"}},
			{ID: "ui-concept", Name: "界面视觉稿", Description: "清晰的信息层级", Content: "生成界面视觉稿：真实产品结构，信息层级清楚，控件尺寸合理，文字完整可读，避免装饰性堆叠。", Scope: CreationScopeImage, Enabled: true, Category: "图像", Tags: []string{"UI", "产品"}},
		},
		Rules: []CreationLibraryEntry{
			{ID: "codex-preserve-objective", Name: "保持目标与约束", Description: "遵循明确目标、范围和输出格式", Content: "保留用户明确的目标、范围、约束、来源和输出格式。不要把请求替换成相近但不同的任务；只有缺少会实质改变结果的信息时才提问。", Scope: CreationScopeAll, Enabled: true, AutoApply: true, Author: "Codex", Category: "Codex 规则", Tags: []string{"目标", "范围"}},
			{ID: "codex-ground-first", Name: "先查证再判断", Description: "事实、假设与未知信息分开", Content: "先依据当前输入、可用资料和可验证结果再下结论。明确区分已验证事实、待验证假设和未知信息；证据冲突时保留冲突并说明取舍。", Scope: CreationScopeChat, Enabled: true, AutoApply: true, Author: "Codex", Category: "Codex 规则", Tags: []string{"证据", "核验"}},
			{ID: "codex-complete-path", Name: "完成可验证路径", Description: "交付可检查的结果", Content: "在范围明确后执行最窄的完整路径。需要产物时给出可检查的文件、补丁或命令；没有验证的结果标记为未验证，不把中间状态描述成完成。", Scope: CreationScopeChat, Enabled: true, Author: "Codex", Category: "Codex 规则", Tags: []string{"交付", "验证"}},
			{ID: "codex-secrets", Name: "保护凭证与隐私", Description: "不输出无关密钥和个人数据", Content: "不要在回答、日志、示例或生成内容中泄露密钥、令牌、Cookie、密码和无关个人数据。只处理完成当前任务所需的最小信息。", Scope: CreationScopeAll, Enabled: true, AutoApply: true, Author: "Codex", Category: "Codex 规则", Tags: []string{"安全", "隐私"}},
			{ID: "codex-engineering", Name: "依据真实契约实现", Description: "以调用方和运行行为为准", Content: "实现应依据现有调用方、数据契约和运行行为。修复拥有问题的源头及直接依赖；不要无证据增加兼容层、兜底路径或无关重构。", Scope: CreationScopeChat, Enabled: true, Author: "Codex", Category: "Codex 规则", Tags: []string{"工程", "契约"}},
			{ID: "codex-direct-response", Name: "直接且克制地回答", Description: "删除重复、套话和过程旁白", Content: "先给结果或下一步。使用用户当前语言，保留准确标识；不重复问题，不叙述常规过程，不添加无助于行动、判断或恢复的文案。", Scope: CreationScopeChat, Enabled: true, Author: "Codex", Category: "Codex 规则", Tags: []string{"表达", "本地化"}},
			{ID: "codex-frontend", Name: "产品界面规则", Description: "可用、响应式和可访问", Content: "界面以任务为中心，使用真实数据和直接标签。所有支持的视口都不得出现意外横向溢出、关键控件裁切或无法到达的内容；验证加载、空态、错误、键盘操作及移动端布局。", Scope: CreationScopeChat, Enabled: true, Author: "Codex", Category: "Codex 规则", Tags: []string{"前端", "移动端"}},
			{ID: "request-language", Name: "使用请求语言", Description: "跟随用户当前语言", Content: "使用用户当前请求的主要语言回答，专有名词和代码标识保持原样。", Scope: CreationScopeChat, Enabled: true, Author: "DengDeng AI", Category: "通用"},
			{ID: "image-text", Name: "文字完整可读", Description: "保护图中指定文案", Content: "图中需要出现的文字必须完整、可读且拼写准确；未要求时不要自行添加文字。", Scope: CreationScopeImage, Enabled: true, Author: "DengDeng AI", Category: "图像"},
		},
		Skills: defaultCreationSkills(),
	}
}

func (s *SystemSettingsService) DefaultCreationLibrary() CreationLibrarySettings {
	return defaultCreationLibrarySettings()
}

func (c CreationCapabilitySettings) TypeEnabled(kind string) bool {
	switch kind {
	case "prompt":
		return c.Prompts
	case "rule":
		return c.Rules
	case "skill":
		return c.Skills
	default:
		return false
	}
}

func (c CreationCapabilitySettings) ScopeEnabled(scope string) bool {
	switch scope {
	case CreationScopeAll:
		return c.Chat || c.Image || c.Video || c.Audio
	case CreationScopeChat:
		return c.Chat
	case CreationScopeImage:
		return c.Image
	case CreationScopeVideo:
		return c.Video
	case CreationScopeAudio:
		return c.Audio
	default:
		return false
	}
}

func creationCapabilitiesAreZero(c CreationCapabilitySettings) bool {
	return !c.Prompts && !c.Rules && !c.Skills && !c.Chat && !c.Image && !c.Video && !c.Audio
}

func upgradeCreationLibrarySettings(library *CreationLibrarySettings, fromVersion int) {
	if library == nil || fromVersion >= creationCatalogVersion {
		return
	}
	defaults := defaultCreationLibrarySettings()
	if creationCapabilitiesAreZero(library.Capabilities) {
		library.Capabilities = defaults.Capabilities
	}
	merge := func(current *[]CreationLibraryEntry, additions []CreationLibraryEntry) {
		byID := make(map[string]int, len(*current))
		for index := range *current {
			byID[(*current)[index].ID] = index
		}
		for _, addition := range additions {
			index, exists := byID[addition.ID]
			if !exists {
				*current = append(*current, addition)
				continue
			}
			entry := &(*current)[index]
			matchesDefaultText := entry.Name == addition.Name && entry.Description == addition.Description && entry.Content == addition.Content
			if entry.Version == "" {
				entry.Version = addition.Version
			}
			if entry.Author == "" {
				entry.Author = addition.Author
			}
			if matchesDefaultText && entry.NameEN == "" {
				entry.NameEN = addition.NameEN
			}
			if matchesDefaultText && entry.DescriptionEN == "" {
				entry.DescriptionEN = addition.DescriptionEN
			}
			if matchesDefaultText && entry.ContentEN == "" {
				entry.ContentEN = addition.ContentEN
			}
			if entry.Category == "" {
				entry.Category = addition.Category
			}
			if len(entry.Tags) == 0 {
				entry.Tags = append([]string(nil), addition.Tags...)
			}
			if entry.SourceType == "" {
				entry.SourceType = addition.SourceType
			}
			if entry.SourceURL == "" {
				entry.SourceURL = addition.SourceURL
			}
			if entry.InstallCommand == "" {
				entry.InstallCommand = addition.InstallCommand
			}
			if entry.License == "" {
				entry.License = addition.License
			}
		}
	}
	merge(&library.Prompts, defaults.Prompts)
	merge(&library.Rules, defaults.Rules)
	merge(&library.Skills, defaults.Skills)
	library.CatalogVersion = creationCatalogVersion
}

func normalizeCreationLibrarySettings(library *CreationLibrarySettings) error {
	if library == nil {
		return errors.New("creation library is required")
	}
	if library.CatalogVersion < creationCatalogVersion {
		upgradeCreationLibrarySettings(library, library.CatalogVersion)
	}
	library.CatalogVersion = creationCatalogVersion
	total := len(library.Prompts) + len(library.Rules) + len(library.Skills)
	if total > creationLibraryMaxEntries {
		return fmt.Errorf("creation library allows at most %d entries", creationLibraryMaxEntries)
	}

	normalizeEntries := func(kind string, entries []CreationLibraryEntry) ([]CreationLibraryEntry, error) {
		seen := make(map[string]struct{}, len(entries))
		normalized := make([]CreationLibraryEntry, 0, len(entries))
		for index, entry := range entries {
			entry.ID = normalizeDocumentID(entry.ID)
			if entry.ID == "" {
				entry.ID = fmt.Sprintf("%s-%d", kind, index+1)
			}
			entry.Name = strings.TrimSpace(entry.Name)
			entry.NameEN = strings.TrimSpace(entry.NameEN)
			entry.Description = strings.TrimSpace(entry.Description)
			entry.DescriptionEN = strings.TrimSpace(entry.DescriptionEN)
			entry.Content = strings.TrimSpace(entry.Content)
			entry.ContentEN = strings.TrimSpace(entry.ContentEN)
			entry.Scope = strings.ToLower(strings.TrimSpace(entry.Scope))
			entry.Version = strings.TrimSpace(entry.Version)
			entry.Author = strings.TrimSpace(entry.Author)
			entry.Category = strings.TrimSpace(entry.Category)
			entry.SourceType = strings.ToLower(strings.TrimSpace(entry.SourceType))
			entry.SourceURL = strings.TrimSpace(entry.SourceURL)
			entry.InstallCommand = strings.TrimSpace(entry.InstallCommand)
			entry.License = strings.TrimSpace(entry.License)
			if entry.Scope == "" {
				entry.Scope = CreationScopeAll
			}
			switch entry.Scope {
			case CreationScopeAll, CreationScopeChat, CreationScopeImage, CreationScopeVideo, CreationScopeAudio:
			default:
				return nil, fmt.Errorf("%s %q has an invalid scope", kind, entry.ID)
			}
			if entry.Name == "" || len([]rune(entry.Name)) > 64 {
				return nil, fmt.Errorf("%s %q needs a name of at most 64 characters", kind, entry.ID)
			}
			if len([]rune(entry.NameEN)) > 96 {
				return nil, fmt.Errorf("%s %q English name is too long", kind, entry.ID)
			}
			if len([]rune(entry.Description)) > 160 {
				return nil, fmt.Errorf("%s %q description is too long", kind, entry.ID)
			}
			if len([]rune(entry.DescriptionEN)) > 240 {
				return nil, fmt.Errorf("%s %q English description is too long", kind, entry.ID)
			}
			if entry.Content == "" || len([]rune(entry.Content)) > 4_000 {
				return nil, fmt.Errorf("%s %q content must be between 1 and 4000 characters", kind, entry.ID)
			}
			if len([]rune(entry.ContentEN)) > 4_000 {
				return nil, fmt.Errorf("%s %q English content is too long", kind, entry.ID)
			}
			if len([]rune(entry.Version)) > 24 || len([]rune(entry.Author)) > 64 || len([]rune(entry.Category)) > 40 || len([]rune(entry.License)) > 40 {
				return nil, fmt.Errorf("%s %q metadata is too long", kind, entry.ID)
			}
			switch entry.SourceType {
			case "", "builtin", "official", "community", "custom":
			default:
				return nil, fmt.Errorf("%s %q has an invalid source type", kind, entry.ID)
			}
			if len(entry.SourceURL) > 500 {
				return nil, fmt.Errorf("%s %q source URL is too long", kind, entry.ID)
			}
			if len([]rune(entry.InstallCommand)) > 2_000 {
				return nil, fmt.Errorf("%s %q install command is too long", kind, entry.ID)
			}
			if kind != "skill" {
				entry.InstallCommand = ""
			}
			if entry.SourceURL != "" {
				parsed, err := url.ParseRequestURI(entry.SourceURL)
				if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
					return nil, fmt.Errorf("%s %q has an invalid source URL", kind, entry.ID)
				}
			}
			if len(entry.Tags) > 8 {
				return nil, fmt.Errorf("%s %q allows at most 8 tags", kind, entry.ID)
			}
			tags := make([]string, 0, len(entry.Tags))
			tagSet := make(map[string]struct{}, len(entry.Tags))
			for _, rawTag := range entry.Tags {
				tag := strings.TrimSpace(rawTag)
				if tag == "" {
					continue
				}
				if len([]rune(tag)) > 24 {
					return nil, fmt.Errorf("%s %q tag is too long", kind, entry.ID)
				}
				key := strings.ToLower(tag)
				if _, exists := tagSet[key]; exists {
					continue
				}
				tagSet[key] = struct{}{}
				tags = append(tags, tag)
			}
			entry.Tags = tags
			if _, exists := seen[entry.ID]; exists {
				return nil, fmt.Errorf("%s IDs must be unique", kind)
			}
			seen[entry.ID] = struct{}{}
			if kind == "prompt" || kind == "skill" {
				entry.AutoApply = false
			}
			normalized = append(normalized, entry)
		}
		return normalized, nil
	}

	var err error
	if library.Prompts, err = normalizeEntries("prompt", library.Prompts); err != nil {
		return err
	}
	if library.Rules, err = normalizeEntries("rule", library.Rules); err != nil {
		return err
	}
	if library.Skills, err = normalizeEntries("skill", library.Skills); err != nil {
		return err
	}
	return nil
}

func (s *SystemSettingsService) UpdateCreationLibrary(next CreationLibrarySettings) (CreationLibrarySettings, error) {
	settings, err := s.Get()
	if err != nil {
		return CreationLibrarySettings{}, err
	}
	settings.CreationLibrary = next
	updated, err := s.Update(settings)
	if err != nil {
		return CreationLibrarySettings{}, err
	}
	return updated.CreationLibrary, nil
}

func PublicCreationLibrary(library CreationLibrarySettings) CreationLibrarySettings {
	filter := func(kind string, entries []CreationLibraryEntry) []CreationLibraryEntry {
		if !library.Capabilities.TypeEnabled(kind) {
			return []CreationLibraryEntry{}
		}
		out := make([]CreationLibraryEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.Enabled && library.Capabilities.ScopeEnabled(entry.Scope) {
				out = append(out, entry)
			}
		}
		return out
	}
	return CreationLibrarySettings{
		Enabled:        library.Enabled,
		CatalogVersion: library.CatalogVersion,
		Capabilities:   library.Capabilities,
		Prompts:        filter("prompt", library.Prompts),
		Rules:          filter("rule", library.Rules),
		Skills:         filter("skill", library.Skills),
	}
}

func sanitizeCreationSelection(library CreationLibrarySettings, selection UserCreationSelection) (UserCreationSelection, error) {
	public := PublicCreationLibrary(library)
	validate := func(kind string, entries []CreationLibraryEntry, requested []string) ([]string, error) {
		available := make(map[string]struct{}, len(entries))
		for _, entry := range entries {
			if !entry.AutoApply {
				available[entry.ID] = struct{}{}
			}
		}
		seen := make(map[string]struct{}, len(requested))
		result := make([]string, 0, len(requested))
		for _, rawID := range requested {
			id := normalizeDocumentID(rawID)
			if id == "" {
				continue
			}
			if _, ok := available[id]; !ok {
				return nil, fmt.Errorf("%s %q is unavailable", kind, id)
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			result = append(result, id)
		}
		if len(result) > 24 {
			return nil, fmt.Errorf("at most 24 %ss can be selected", kind)
		}
		sort.Strings(result)
		return result, nil
	}
	rules, err := validate("rule", public.Rules, selection.RuleIDs)
	if err != nil {
		return UserCreationSelection{}, err
	}
	skills, err := validate("skill", public.Skills, selection.SkillIDs)
	if err != nil {
		return UserCreationSelection{}, err
	}
	return UserCreationSelection{RuleIDs: rules, SkillIDs: skills}, nil
}

func visibleCreationSelection(library CreationLibrarySettings, selection UserCreationSelection) UserCreationSelection {
	public := PublicCreationLibrary(library)
	filter := func(entries []CreationLibraryEntry, requested []string) []string {
		available := make(map[string]struct{}, len(entries))
		for _, entry := range entries {
			if !entry.AutoApply {
				available[entry.ID] = struct{}{}
			}
		}
		result := make([]string, 0, len(requested))
		for _, id := range requested {
			if _, ok := available[id]; ok {
				result = append(result, id)
			}
		}
		return result
	}
	return UserCreationSelection{
		RuleIDs:  filter(public.Rules, selection.RuleIDs),
		SkillIDs: filter(public.Skills, selection.SkillIDs),
	}
}

func (s *SystemSettingsService) UserCreationLibrary(userID int64) (UserCreationLibrary, error) {
	settings, err := s.Get()
	if err != nil {
		return UserCreationLibrary{}, err
	}
	selection, err := s.UserCreationSelection(userID)
	if err != nil {
		return UserCreationLibrary{}, err
	}
	selection = visibleCreationSelection(settings.CreationLibrary, selection)
	return UserCreationLibrary{
		CreationLibrarySettings: PublicCreationLibrary(settings.CreationLibrary),
		SelectedRuleIDs:         selection.RuleIDs,
		SelectedSkillIDs:        selection.SkillIDs,
	}, nil
}

func (s *SystemSettingsService) UserCreationSelection(userID int64) (UserCreationSelection, error) {
	if userID <= 0 || s.db == nil || !s.db.Migrator().HasTable(&model.UserCreationSelection{}) {
		return UserCreationSelection{RuleIDs: []string{}, SkillIDs: []string{}}, nil
	}
	var stored model.UserCreationSelection
	if err := s.db.First(&stored, "user_id = ?", userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return UserCreationSelection{RuleIDs: []string{}, SkillIDs: []string{}}, nil
		}
		return UserCreationSelection{}, err
	}
	return UserCreationSelection{
		RuleIDs:  append([]string(nil), stored.RuleIDs...),
		SkillIDs: append([]string(nil), stored.SkillIDs...),
	}, nil
}

func (s *SystemSettingsService) UpdateUserCreationSelection(userID int64, requested UserCreationSelection) (UserCreationSelection, error) {
	if userID <= 0 {
		return UserCreationSelection{}, errors.New("user is required")
	}
	settings, err := s.Get()
	if err != nil {
		return UserCreationSelection{}, err
	}
	selection, err := sanitizeCreationSelection(settings.CreationLibrary, requested)
	if err != nil {
		return UserCreationSelection{}, err
	}
	stored := model.UserCreationSelection{UserID: userID, RuleIDs: selection.RuleIDs, SkillIDs: selection.SkillIDs}
	if err := s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"rule_ids", "skill_ids", "updated_at"}),
	}).Create(&stored).Error; err != nil {
		return UserCreationSelection{}, err
	}
	return selection, nil
}
