export type CreationMode = "image" | "video" | "text" | "audio";

export type LocalizedText = {
    zh: string;
    en: string;
};

export type BuiltInPrompt = {
    id: string;
    title: LocalizedText;
    description: LocalizedText;
    prompt: LocalizedText;
    modes: CreationMode[];
    tags: LocalizedText[];
};

export type BuiltInRule = {
    id: string;
    title: LocalizedText;
    description: LocalizedText;
    instruction: string;
    modes: CreationMode[];
};

export type BuiltInSkill = {
    id: string;
    title: LocalizedText;
    description: LocalizedText;
    instruction: string;
    modes: CreationMode[];
};

const text = (zh: string, en: string): LocalizedText => ({ zh, en });

export const builtInPrompts: BuiltInPrompt[] = [
    {
        id: "portrait-editorial",
        title: text("杂志人像", "Editorial portrait"),
        description: text("人物、光线与镜头语言", "Subject, lighting, and camera language"),
        prompt: text("一张杂志编辑风格的人像摄影。主体：{{主体}}。场景：{{场景}}。光线：柔和侧光，保留自然肤质。构图：半身，视线明确，背景克制。镜头：85mm 人像镜头，浅景深。", "An editorial portrait photograph. Subject: {{subject}}. Setting: {{setting}}. Lighting: soft side light with natural skin texture. Composition: half body, clear eye line, restrained background. Lens: 85mm portrait lens, shallow depth of field."),
        modes: ["image"],
        tags: [text("人像", "Portrait"), text("摄影", "Photography")],
    },
    {
        id: "product-studio",
        title: text("商品主图", "Product hero"),
        description: text("电商与品牌商品展示", "E-commerce and brand product imagery"),
        prompt: text("为{{商品}}制作一张商品主图。主体完整、比例准确，材质与边缘清楚。背景：{{背景}}。光线：大面积柔光配合轮廓光。构图：居中，留出安全边距，不添加无关道具、文字或水印。", "Create a product hero image for {{product}}. Keep the object complete, proportionally accurate, with clear materials and edges. Background: {{background}}. Lighting: broad soft light with rim light. Centered composition with safe margins; no unrelated props, text, or watermark."),
        modes: ["image"],
        tags: [text("商品", "Product"), text("电商", "E-commerce")],
    },
    {
        id: "poster-layout",
        title: text("活动海报", "Event poster"),
        description: text("带明确文案层级的竖版海报", "Portrait poster with clear copy hierarchy"),
        prompt: text("设计一张竖版活动海报。主题：{{主题}}。主标题：{{主标题}}。副标题：{{副标题}}。时间与地点：{{信息}}。建立清楚的标题、正文和信息层级，文字必须完整可读，保留合理留白，避免装饰压住文字。", "Design a portrait event poster. Theme: {{theme}}. Headline: {{headline}}. Subheading: {{subheading}}. Time and place: {{details}}. Establish clear headline, body, and information hierarchy. Keep all text complete and legible with adequate whitespace."),
        modes: ["image"],
        tags: [text("海报", "Poster"), text("排版", "Typography")],
    },
    {
        id: "ui-dashboard",
        title: text("界面视觉稿", "Interface mockup"),
        description: text("可落地的产品界面图", "Implementation-ready product interface"),
        prompt: text("设计{{产品类型}}的{{页面名称}}界面。核心任务：{{核心任务}}。信息层级清楚，使用真实业务数据与直接标签；控件状态完整，间距统一，文字可读。避免营销式大标题、无意义装饰卡片和过多说明文案。", "Design the {{page name}} interface for a {{product type}}. Primary task: {{primary task}}. Use clear information hierarchy, realistic business data, direct labels, complete control states, consistent spacing, and legible text. Avoid marketing headlines, decorative cards, and redundant helper copy."),
        modes: ["image"],
        tags: [text("UI", "UI"), text("产品", "Product")],
    },
    {
        id: "storyboard",
        title: text("分镜脚本", "Storyboard"),
        description: text("镜头、动作与节奏", "Shots, action, and pacing"),
        prompt: text("为{{主题}}编写一组分镜。总时长：{{时长}}。按镜头列出景别、机位运动、主体动作、环境声音与转场；保持人物、服装、场景和时间连续，节奏由开场、发展到收束。", "Create a storyboard for {{subject}}. Total duration: {{duration}}. For each shot, specify framing, camera movement, subject action, ambient sound, and transition. Preserve continuity in character, wardrobe, setting, and time, with a clear opening, development, and resolution."),
        modes: ["video", "text"],
        tags: [text("视频", "Video"), text("分镜", "Storyboard")],
    },
    {
        id: "prompt-structure",
        title: text("结构化描述", "Structured brief"),
        description: text("把想法整理成可执行说明", "Turn an idea into an actionable brief"),
        prompt: text("目标：{{目标}}\n主体：{{主体}}\n场景：{{场景}}\n风格：{{风格}}\n构图或镜头：{{构图}}\n光线与色彩：{{光线色彩}}\n必须保留：{{必须保留}}\n不要出现：{{不要出现}}", "Goal: {{goal}}\nSubject: {{subject}}\nSetting: {{setting}}\nStyle: {{style}}\nComposition or shot: {{composition}}\nLighting and color: {{lighting and color}}\nMust preserve: {{must preserve}}\nExclude: {{exclude}}"),
        modes: ["image", "video", "text"],
        tags: [text("通用", "General"), text("结构", "Structure")],
    },
];

export const builtInRules: BuiltInRule[] = [
    {
        id: "preserve-intent",
        title: text("保持原始意图", "Preserve intent"),
        description: text("不擅自增加主体、文案或风格", "Do not invent subjects, copy, or styles"),
        instruction: "Follow the user's explicit intent and constraints. Do not add subjects, text, logos, props, or stylistic elements that were not requested.",
        modes: ["image", "video", "text", "audio"],
    },
    {
        id: "reference-consistency",
        title: text("参考图一致性", "Reference consistency"),
        description: text("保留人物、商品和关键视觉特征", "Preserve identity, products, and key visual traits"),
        instruction: "When reference media is provided, preserve identity, proportions, materials, colors, and other explicitly visible defining features unless the user asks to change them.",
        modes: ["image", "video"],
    },
    {
        id: "legible-text",
        title: text("文字完整可读", "Legible text"),
        description: text("有文字时保持内容、层级与边距", "Preserve copy, hierarchy, and margins"),
        instruction: "When the request includes visible text, reproduce the supplied wording exactly, keep every line legible, and maintain clear hierarchy and safe margins.",
        modes: ["image", "video"],
    },
    {
        id: "visual-coherence",
        title: text("视觉一致", "Visual coherence"),
        description: text("统一构图、光线、透视和材质", "Align composition, lighting, perspective, and materials"),
        instruction: "Keep composition, perspective, lighting direction, shadow behavior, scale, and material response internally consistent.",
        modes: ["image", "video"],
    },
    {
        id: "concise-output",
        title: text("直接输出", "Direct output"),
        description: text("文本结果不重复问题和过程说明", "Avoid repeating the request or narrating the process"),
        instruction: "Answer directly. Do not restate the request, narrate routine reasoning, or add generic introductions and conclusions.",
        modes: ["text"],
    },
];

export const builtInSkills: BuiltInSkill[] = [
    {
        id: "art-director",
        title: text("视觉导演", "Art director"),
        description: text("补齐构图、镜头、光线和色彩关系", "Resolve composition, camera, lighting, and color"),
        instruction: "Act as a visual art director. Convert the request into one coherent visual decision: define the focal subject, composition, camera position, depth, lighting direction, color relationship, and background restraint without changing the requested content.",
        modes: ["image", "video"],
    },
    {
        id: "product-photographer",
        title: text("商品摄影", "Product photography"),
        description: text("突出形体、材质与可售卖性", "Clarify form, materials, and sellability"),
        instruction: "Use product-photography discipline: accurate proportions, clean silhouettes, controlled reflections, material-specific highlights, readable branding only when supplied, and an uncluttered commercial composition.",
        modes: ["image", "video"],
    },
    {
        id: "character-continuity",
        title: text("角色连续性", "Character continuity"),
        description: text("跨画面保持人物特征和服装", "Preserve character features and wardrobe across shots"),
        instruction: "Maintain character continuity across outputs: facial structure, age, hairstyle, body proportions, wardrobe, accessories, handedness, and distinguishing marks must remain stable unless explicitly changed.",
        modes: ["image", "video"],
    },
    {
        id: "layout-designer",
        title: text("版式设计", "Layout design"),
        description: text("建立网格、层级、对齐和留白", "Establish grid, hierarchy, alignment, and whitespace"),
        instruction: "Use a deliberate layout grid with strong hierarchy, consistent alignment, sufficient whitespace, readable typography, and clear separation between primary and secondary information.",
        modes: ["image"],
    },
    {
        id: "storyboard-director",
        title: text("分镜导演", "Storyboard director"),
        description: text("组织镜头推进和动作连续性", "Organize shot progression and action continuity"),
        instruction: "Plan the result as a director: define shot purpose, framing, camera movement, subject movement, temporal continuity, transition, and the visual beat that advances the sequence.",
        modes: ["video", "text"],
    },
    {
        id: "prompt-editor",
        title: text("提示词整理", "Prompt editor"),
        description: text("将零散要求整理成完整执行说明", "Turn scattered requirements into an executable brief"),
        instruction: "Resolve scattered user requirements into a complete executable brief while preserving every explicit constraint. Remove repetition, surface conflicts, and keep concrete nouns, quantities, and required wording unchanged.",
        modes: ["image", "video", "text", "audio"],
    },
];

export function localized(value: LocalizedText, language: string) {
    return language.startsWith("zh") ? value.zh : value.en;
}
