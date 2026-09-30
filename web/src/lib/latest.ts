// Debounced requests where only the latest one counts, for the rule
// editor's live preview: typing schedules a request after a pause, a new
// keystroke cancels the pending timer and aborts the request in flight, and
// a late answer to an older query is dropped.

export type Outcome<Q, R> = { ok: true; query: Q; value: R } | { ok: false; query: Q; error: unknown };

export class LatestRequest<Q, R> {
	private timer: ReturnType<typeof setTimeout> | undefined;
	private ctrl: AbortController | null = null;
	private seq = 0;

	constructor(
		private readonly fn: (query: Q, signal: AbortSignal) => Promise<R>,
		private readonly delayMs: number,
		private readonly onresult: (o: Outcome<Q, R>) => void
	) {}

	/** Runs fn(query) after the delay, replacing anything scheduled or running. */
	schedule(query: Q): void {
		this.cancel();
		const seq = this.seq;
		this.timer = setTimeout(() => {
			this.timer = undefined;
			const ctrl = new AbortController();
			this.ctrl = ctrl;
			this.fn(query, ctrl.signal).then(
				(value) => {
					if (seq === this.seq) this.onresult({ ok: true, query, value });
				},
				(error: unknown) => {
					if (seq === this.seq && !ctrl.signal.aborted) this.onresult({ ok: false, query, error });
				}
			);
		}, this.delayMs);
	}

	/** Drops the pending request, if any; no result is delivered for it. */
	cancel(): void {
		this.seq++;
		clearTimeout(this.timer);
		this.timer = undefined;
		this.ctrl?.abort();
		this.ctrl = null;
	}
}
