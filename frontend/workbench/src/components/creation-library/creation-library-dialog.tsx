import { Modal } from "antd";
import { useTranslation } from "react-i18next";

import { CreationLibraryPanel } from "./creation-library-panel";

export function CreationLibraryDialog({ open, onOpenChange, onSelectPrompt }: { open: boolean; onOpenChange: (open: boolean) => void; onSelectPrompt: (prompt: string) => void }) {
    const { t } = useTranslation();
    return (
        <Modal title={t("creationLibrary.title")} open={open} onCancel={() => onOpenChange(false)} footer={null} width={920} centered styles={{ body: { maxHeight: "72dvh", overflowY: "auto" } }}>
            <CreationLibraryPanel onSelectPrompt={(prompt) => { onSelectPrompt(prompt); onOpenChange(false); }} />
        </Modal>
    );
}
