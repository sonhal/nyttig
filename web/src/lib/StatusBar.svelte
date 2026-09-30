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
	}

	let { status, unviewed, times, now, note, shown }: Props = $props();

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
	{#if note}
		<span class="sep">·</span>
		<span class="note">{note}</span>
	{/if}
	<span class="right wide">{shown} shown</span>
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
	.right {
		margin-left: auto;
		color: var(--dim);
	}
	@media (max-width: 719.98px) {
		.bar {
			padding: 0 8px env(safe-area-inset-bottom);
			height: calc(var(--bar-h) + env(safe-area-inset-bottom));
		}
		.wide {
			display: none;
		}
	}
</style>
