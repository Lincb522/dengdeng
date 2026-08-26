import { builtInRules, builtInSkills, type CreationMode } from "./catalog";
import { useCreationLibraryStore } from "./store";
import type { AiConfig } from "@/stores/use-config-store";

export function applyCreationGuidance(config: AiConfig, mode: CreationMode): AiConfig {
    const { enabledRuleIds, enabledSkillIds } = useCreationLibraryStore.getState();
    const rules = builtInRules.filter((item) => item.modes.includes(mode) && enabledRuleIds.includes(item.id));
    const skills = builtInSkills.filter((item) => item.modes.includes(mode) && enabledSkillIds.includes(item.id));
    const guidance = [...rules.map((item) => item.instruction), ...skills.map((item) => item.instruction)];
    if (!guidance.length) return config;
    return {
        ...config,
        systemPrompt: [config.systemPrompt.trim(), ...guidance].filter(Boolean).join("\n\n"),
    };
}
