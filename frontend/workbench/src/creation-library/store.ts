import { create } from "zustand";
import { persist } from "zustand/middleware";

type CreationLibraryStore = {
    enabledRuleIds: string[];
    enabledSkillIds: string[];
    toggleRule: (id: string) => void;
    toggleSkill: (id: string) => void;
    clearEnabled: () => void;
};

function toggle(items: string[], id: string) {
    return items.includes(id) ? items.filter((item) => item !== id) : [...items, id];
}

export const useCreationLibraryStore = create<CreationLibraryStore>()(
    persist(
        (set) => ({
            enabledRuleIds: [],
            enabledSkillIds: [],
            toggleRule: (id) => set((state) => ({ enabledRuleIds: toggle(state.enabledRuleIds, id) })),
            toggleSkill: (id) => set((state) => ({ enabledSkillIds: toggle(state.enabledSkillIds, id) })),
            clearEnabled: () => set({ enabledRuleIds: [], enabledSkillIds: [] }),
        }),
        { name: "dengdeng:creation-library:v1" },
    ),
);
