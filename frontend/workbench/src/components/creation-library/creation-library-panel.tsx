import { useMemo, useState } from "react";
import { Button, Empty, Input, Segmented, Switch, Tabs, Tag } from "antd";
import { Search } from "lucide-react";
import { useTranslation } from "react-i18next";

import { builtInPrompts, builtInRules, builtInSkills, localized, type CreationMode } from "@/creation-library/catalog";
import { useCreationLibraryStore } from "@/creation-library/store";

type LibraryTab = "prompts" | "rules" | "skills";
type ModeFilter = "all" | CreationMode;

export function CreationLibraryPanel({ onSelectPrompt }: { onSelectPrompt?: (prompt: string) => void }) {
    const { i18n, t } = useTranslation();
    const [tab, setTab] = useState<LibraryTab>("prompts");
    const [mode, setMode] = useState<ModeFilter>("all");
    const [keyword, setKeyword] = useState("");
    const enabledRuleIds = useCreationLibraryStore((state) => state.enabledRuleIds);
    const enabledSkillIds = useCreationLibraryStore((state) => state.enabledSkillIds);
    const toggleRule = useCreationLibraryStore((state) => state.toggleRule);
    const toggleSkill = useCreationLibraryStore((state) => state.toggleSkill);
    const language = i18n.resolvedLanguage || "zh-CN";
    const query = keyword.trim().toLowerCase();

    const prompts = useMemo(
        () => builtInPrompts.filter((item) => matches(item.modes, mode) && includesQuery([localized(item.title, language), localized(item.description, language), localized(item.prompt, language), ...item.tags.map((tag) => localized(tag, language))], query)),
        [language, mode, query],
    );
    const rules = useMemo(
        () => builtInRules.filter((item) => matches(item.modes, mode) && includesQuery([localized(item.title, language), localized(item.description, language)], query)),
        [language, mode, query],
    );
    const skills = useMemo(
        () => builtInSkills.filter((item) => matches(item.modes, mode) && includesQuery([localized(item.title, language), localized(item.description, language)], query)),
        [language, mode, query],
    );

    const modeOptions = (["all", "image", "video", "text", "audio"] as ModeFilter[]).map((value) => ({ label: t(`creationLibrary.modes.${value}`), value }));

    return (
        <div className="flex min-h-0 flex-col">
            <div className="flex flex-col gap-3 border-b border-stone-200 pb-4 dark:border-stone-800 sm:flex-row sm:items-center">
                <Input prefix={<Search className="size-4 text-stone-400" />} value={keyword} onChange={(event) => setKeyword(event.target.value)} placeholder={t("creationLibrary.search")} allowClear />
                <Segmented className="shrink-0 overflow-x-auto" value={mode} options={modeOptions} onChange={(value) => setMode(value as ModeFilter)} />
            </div>
            <Tabs
                activeKey={tab}
                onChange={(value) => setTab(value as LibraryTab)}
                items={[
                    { key: "prompts", label: t("creationLibrary.tabs.prompts", { count: prompts.length }), children: <PromptList items={prompts} language={language} onSelect={onSelectPrompt} /> },
                    { key: "rules", label: t("creationLibrary.tabs.rules", { count: enabledRuleIds.length }), children: <ToggleList items={rules} language={language} enabledIds={enabledRuleIds} onToggle={toggleRule} type="rule" /> },
                    { key: "skills", label: t("creationLibrary.tabs.skills", { count: enabledSkillIds.length }), children: <ToggleList items={skills} language={language} enabledIds={enabledSkillIds} onToggle={toggleSkill} type="skill" /> },
                ]}
            />
        </div>
    );
}

function PromptList({ items, language, onSelect }: { items: typeof builtInPrompts; language: string; onSelect?: (prompt: string) => void }) {
    const { t } = useTranslation();
    if (!items.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("creationLibrary.empty")} className="py-12" />;
    return (
        <div className="grid gap-3 md:grid-cols-2">
            {items.map((item) => (
                <article key={item.id} className="min-w-0 rounded-xl border border-stone-200 bg-white p-4 dark:border-stone-800 dark:bg-stone-950">
                    <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                            <h3 className="text-sm font-semibold text-stone-950 dark:text-stone-100">{localized(item.title, language)}</h3>
                            <p className="mt-1 text-xs leading-5 text-stone-500 dark:text-stone-400">{localized(item.description, language)}</p>
                        </div>
                        {onSelect ? <Button size="small" type="primary" onClick={() => onSelect(localized(item.prompt, language))}>{t("creationLibrary.use")}</Button> : null}
                    </div>
                    <p className="mt-3 line-clamp-3 whitespace-pre-wrap text-xs leading-5 text-stone-600 dark:text-stone-300">{localized(item.prompt, language)}</p>
                    <div className="mt-3 flex flex-wrap gap-1.5">{item.tags.map((tag) => <Tag key={tag.zh} className="m-0">{localized(tag, language)}</Tag>)}</div>
                </article>
            ))}
        </div>
    );
}

type ToggleItem = (typeof builtInRules)[number] | (typeof builtInSkills)[number];

function ToggleList({ items, language, enabledIds, onToggle, type }: { items: ToggleItem[]; language: string; enabledIds: string[]; onToggle: (id: string) => void; type: "rule" | "skill" }) {
    const { t } = useTranslation();
    if (!items.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t("creationLibrary.empty")} className="py-12" />;
    return (
        <div className="divide-y divide-stone-200 rounded-xl border border-stone-200 bg-white dark:divide-stone-800 dark:border-stone-800 dark:bg-stone-950">
            {items.map((item) => {
                const enabled = enabledIds.includes(item.id);
                return (
                    <label key={item.id} className="flex cursor-pointer items-start gap-4 px-4 py-3.5">
                        <div className="min-w-0 flex-1">
                            <div className="flex flex-wrap items-center gap-2">
                                <span className="text-sm font-semibold text-stone-950 dark:text-stone-100">{localized(item.title, language)}</span>
                                <Tag className="m-0">{t(`creationLibrary.types.${type}`)}</Tag>
                            </div>
                            <p className="mt-1 text-xs leading-5 text-stone-500 dark:text-stone-400">{localized(item.description, language)}</p>
                        </div>
                        <Switch checked={enabled} onChange={() => onToggle(item.id)} aria-label={localized(item.title, language)} />
                    </label>
                );
            })}
        </div>
    );
}

function matches(modes: CreationMode[], selected: ModeFilter) {
    return selected === "all" || modes.includes(selected);
}

function includesQuery(values: string[], query: string) {
    return !query || values.join(" ").toLowerCase().includes(query);
}
