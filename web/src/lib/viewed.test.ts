import { afterEach, describe, expect, it, vi } from 'vitest';
import { FLUSH_INTERVAL_MS, ViewTracker } from './viewed';

afterEach(() => {
	vi.useRealTimers();
});

describe('ViewTracker', () => {
	it('sends seen ids in batches about every 3 seconds', async () => {
		vi.useFakeTimers();
		const sent: string[][] = [];
		const flushed: string[][] = [];
		const t = new ViewTracker(
			async (ids) => void sent.push(ids),
			() => {},
			(ids) => void flushed.push([...ids])
		);
		t.start();
		t.see(['1', '2']);
		t.see(['2', '3']);
		expect(sent).toEqual([]);
		await vi.advanceTimersByTimeAsync(FLUSH_INTERVAL_MS);
		expect(sent).toEqual([['1', '2', '3']]);
		expect(flushed).toEqual([['1', '2', '3']]);

		// Already sent ids are not sent again; empty ticks send nothing.
		t.see(['3', '4']);
		await vi.advanceTimersByTimeAsync(FLUSH_INTERVAL_MS);
		await vi.advanceTimersByTimeAsync(FLUSH_INTERVAL_MS);
		expect(sent).toEqual([['1', '2', '3'], ['4']]);
		t.stop();
	});

	it('queues ids again when sending fails', async () => {
		let fail = true;
		const sent: string[][] = [];
		const t = new ViewTracker(
			async (ids) => {
				if (fail) throw new Error('offline');
				sent.push(ids);
			},
			() => {}
		);
		t.see(['1']);
		await t.flush();
		expect(t.pendingCount).toBe(1);
		fail = false;
		await t.flush();
		expect(sent).toEqual([['1']]);
		expect(t.pendingCount).toBe(0);
	});

	it('uses the beacon for what is pending on pagehide', () => {
		const beacons: string[][] = [];
		const t = new ViewTracker(async () => {}, (ids) => void beacons.push(ids));
		t.flushBeacon();
		expect(beacons).toEqual([]);
		t.see(['7', '8']);
		t.flushBeacon();
		expect(beacons).toEqual([['7', '8']]);
		t.see(['7']);
		t.flushBeacon();
		expect(beacons).toHaveLength(1);
	});
});
