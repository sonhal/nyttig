// View tracking, K9s-style like the TUI: rows that are on screen count as
// viewed. IDs are collected as they scroll past and sent in batches about
// every 3 seconds; whatever is pending when the page goes away is sent with
// a beacon, which the browser delivers even while the page unloads.

export const FLUSH_INTERVAL_MS = 3000;

export type Sender = (ids: string[]) => Promise<void>;
export type BeaconSender = (ids: string[]) => void;

export class ViewTracker {
	private pending = new Set<string>();
	/** IDs already sent (or queued), so they are never sent twice. */
	private sent = new Set<string>();
	private timer: ReturnType<typeof setInterval> | null = null;

	constructor(
		private send: Sender,
		private beacon: BeaconSender,
		/** Called with the IDs of each batch once it is handed to send. */
		private onFlush: (ids: ReadonlySet<string>) => void = () => {}
	) {}

	/** Records IDs that are on screen now. */
	see(ids: Iterable<string>): void {
		for (const id of ids) {
			if (!this.sent.has(id)) this.pending.add(id);
		}
	}

	get pendingCount(): number {
		return this.pending.size;
	}

	private take(): string[] {
		const ids = [...this.pending];
		this.pending.clear();
		for (const id of ids) this.sent.add(id);
		return ids;
	}

	/** Sends the pending batch. On failure the IDs are queued again. */
	async flush(): Promise<void> {
		if (this.pending.size === 0) return;
		const ids = this.take();
		this.onFlush(new Set(ids));
		try {
			await this.send(ids);
		} catch {
			for (const id of ids) {
				this.sent.delete(id);
				this.pending.add(id);
			}
		}
	}

	/** Sends the pending batch with a beacon (for pagehide). */
	flushBeacon(): void {
		if (this.pending.size === 0) return;
		const ids = this.take();
		this.onFlush(new Set(ids));
		this.beacon(ids);
	}

	start(interval = FLUSH_INTERVAL_MS): void {
		this.stop();
		this.timer = setInterval(() => void this.flush(), interval);
	}

	stop(): void {
		if (this.timer !== null) clearInterval(this.timer);
		this.timer = null;
	}
}
