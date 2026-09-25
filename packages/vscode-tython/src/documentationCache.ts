// Editor cache only; docstrings still come from the Python provider. A native
// hover never waits for a cache miss. Failures can be retried; empty docs cache.
export class DocumentationCache<T> {
    private readonly entries = new Map<string, { result?: T; pending?: Promise<T | undefined> }>();
    constructor(private readonly limit = 128) {}

    get(key: string, load: () => Promise<T | undefined>, wait: boolean): Promise<T | undefined> {
        let entry = this.entries.get(key);
        if (!entry) {
            if (this.entries.size >= this.limit) this.entries.delete(this.entries.keys().next().value!);
            entry = {};
            this.entries.set(key, entry);
            const current = entry;
            entry.pending = load().then(result => {
                current.result = result;
                current.pending = undefined;
                // undefined means cancelled/unavailable; an empty result is
                // represented by the caller as a successful cacheable value.
                if (result === undefined && this.entries.get(key) === current) this.entries.delete(key);
                return result;
            }, () => {
                if (this.entries.get(key) === current) this.entries.delete(key);
                return undefined;
            });
        }
        return wait && entry.pending ? entry.pending : Promise.resolve(entry.result);
    }

    clear(): void { this.entries.clear(); }
}
