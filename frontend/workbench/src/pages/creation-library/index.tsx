import { CreationLibraryPanel } from "@/components/creation-library/creation-library-panel";

export default function CreationLibraryPage() {
    return (
        <main className="h-full overflow-y-auto bg-background px-4 py-6 sm:px-6">
            <div className="mx-auto max-w-5xl">
                <CreationLibraryPanel />
            </div>
        </main>
    );
}
