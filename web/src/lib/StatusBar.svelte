<!-- Connection state, unviewed count and fetch times, like the TUI's status bar. -->
<script lang="ts">
	import { shortDuration } from './format';
	import type { FetchTimes } from './meta';
	import type { ConnStatus } from './stream.svelte';

	interface Props {
		status: ConnStatus;
		unviewed: number;
		times: FetchTimes;
		now: number;
		/** A transient message, e.g. after a refresh. */
		note: string;
		shown: number;
		/** Follow mode: "on" at the top of a newest-first list, "off" when scrolled away, "na" oldest first. */
		follow?: 'on' | 'off' | 'na';
		/** Items pushed since scrolling away. */
		pending?: number;
		/** Jump to the newest and follow (F). */
		onfollow?: () => void;
		onhelp?: () => void;
	}

	let { status, unviewed, times, now, note, shown, follow = 'na', pending = 0, onfollow, onhelp }: Props = $props();

	const last = $derived(times.last === null ? '--' : shortDuration(now - times.last) + ' ago');
	const next = $derived(
		times.next === null ? '--' : times.next <= now ? 'now' : shortDuration(times.next - now)
	);
</script>

<div class="bar" role="status" aria-live="polite" data-testid="status">
	<span class="conn {status}" data-status={status}>● {status}</span>
	<span class="sep">·</span>
	<span>unviewed <b data-testid="unviewed">{unviewed}</b></span>
	<span class="sep wide">·</span>
	<span class="wide">last fetch {last}</span>
	<span class="sep">·</span>
	<span><span class="wide">{'next '}</span>{next}</span>
	{#if follow === 'on'}
		<span class="sep">·</span>
		<span class="follow on" data-testid="follow" data-follow="on">follow</span>
	{:else if follow === 'off'}
		<span class="sep">·</span>
		<button type="button" class="follow off" data-testid="follow" data-follow="off" data-pending={pending} onclick={onfollow} title="jump to the newest and follow (F)"
			>{pending > 0 ? `↑ ${pending} new` : '↑ follow'}<span class="wide">{' (F)'}</span></button
		>
	{/if}
	{#if note}
		<span class="sep">·</span>
		<span class="note">{note}</span>
	{/if}
	<span class="right wide">{shown} shown</span>
	<button type="button" class="help wide" onclick={onhelp} title="key help (?)" data-testid="help-hint">? help</button>
</div>

<style>
	.bar {
		display: flex;
		align-items: center;
		gap: 1ch;
		height: var(--bar-h);
		padding: 0 1ch;
		background: var(--bar);
		white-space: nowrap;
		overflow: hidden;
	}
	b {
		font-weight: normal;
		color: var(--fg-strong);
	}
	.sep {
		color: var(--dim);
	}
	.conn.connected {
		color: var(--unviewed);
	}
	.conn.connecting {
		color: var(--dim);
	}
	.conn.disconnected {
		color: var(--error);
	}
	.note {
		color: var(--accent);
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.follow.on {
		color: var(--unviewed);
	}
	.follow.off {
		color: var(--accent);
		background: none;
		border: none;
		padding: 0;
		cursor: pointer;
		white-space: nowrap;
	}
	.right {
		margin-left: auto;
		color: var(--dim);
	}
	.help {
		background: none;
		border: none;
		padding: 0;
		color: var(--dim);
		cursor: pointer;
	}
	@media (max-width: 719.98px) {
		.bar {
			padding: 0 8px env(safe-area-inset-bottom);
			height: calc(var(--bar-h) + env(safe-area-inset-bottom));
		}
		/* A touch target: the whole bar's height. */
		.follow.off {
			height: 100%;
			padding: 0 4px;
		}
		.wide {
			display: none;
		}
	}
</style>
