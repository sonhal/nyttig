// Command history for the ":" line: Up goes back through what was run,
// Down comes forward again to what was being typed.

const MAX_ENTRIES = 50;

export class History {
	private entries: string[] = [];
	/** Index into entries while browsing, or -1 at the line being typed. */
	private at = -1;
	/** What was typed before the first Up, to come back to. */
	private draft = '';

	/** Remembers a command that was run; a repeat of the last one is skipped. */
	push(text: string): void {
		const t = text.trim();
		this.reset();
		if (!t || this.entries[this.entries.length - 1] === t) return;
		this.entries.push(t);
		if (this.entries.length > MAX_ENTRIES) this.entries.shift();
	}

	/** Forgets the browsing position (a new line was opened, or typed into). */
	reset(): void {
		this.at = -1;
		this.draft = '';
	}

	/** The previous command, or null when there is none to go back to. */
	prev(current: string): string | null {
		if (this.entries.length === 0) return null;
		if (this.at === -1) {
			this.draft = current;
			this.at = this.entries.length - 1;
		} else if (this.at > 0) {
			this.at--;
		}
		return this.entries[this.at] ?? null;
	}

	/** The next command, the typed line after the last one, or null when not browsing. */
	next(): string | null {
		if (this.at === -1) return null;
		if (this.at < this.entries.length - 1) {
			this.at++;
			return this.entries[this.at] ?? null;
		}
		const d = this.draft;
		this.reset();
		return d;
	}
}
