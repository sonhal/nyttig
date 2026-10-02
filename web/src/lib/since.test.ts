import { describe, expect, it } from 'vitest';
import { cutoff, describeSince, nextSince, parseSince, sinceAfter, validSince } from './since';

// The same cases are in internal/since/since_test.go; keep them in step.
const CASES: [now: string, window: string, want: string][] = [
	['2026-03-31T12:00:00Z', '24h', '2026-03-30T12:00:00Z'],
	['2026-03-31T12:00:00Z', '36h', '2026-03-30T00:00:00Z'],
	['2026-03-31T12:00:00Z', '7d', '2026-03-24T12:00:00Z'],
	['2026-03-31T12:00:00Z', '30d', '2026-03-01T12:00:00Z'],
	['2026-03-31T12:00:00Z', '2w', '2026-03-17T12:00:00Z'],
	['2026-03-31T12:00:00Z', '1y', '2025-03-31T12:00:00Z'],
	// 31 March minus a month is "31 February", which normalises forward.
	['2026-03-31T12:00:00Z', '1mo', '2026-03-03T12:00:00Z'],
	['2024-03-31T12:00:00Z', '1mo', '2024-03-02T12:00:00Z'],
	// 29 February minus a year is "29 February" of a non-leap year.
	['2028-02-29T12:00:00Z', '1y', '2027-03-01T12:00:00Z'],
	['2026-01-15T00:00:00Z', '1mo', '2025-12-15T00:00:00Z'],
	['2026-01-15T00:00:00Z', '13mo', '2024-12-15T00:00:00Z'],
	['2026-01-15T00:00:00Z', '9999h', '2024-11-24T09:00:00Z']
];

describe('cutoff', () => {
	it.each(CASES)('%s - %s = %s', (now, window, want) => {
		const r = parseSince(window);
		if (!r.ok) throw new Error(r.error);
		expect(new Date(cutoff(r.window, Date.parse(now))).toISOString().replace('.000Z', 'Z')).toBe(want);
	});

	it('does not depend on the local time zone', () => {
		// Calendar months are counted in UTC: 30 March 20:00Z is 31 March in +05:00.
		const now = Date.parse('2026-03-30T20:00:00Z');
		const r = parseSince('1mo');
		if (!r.ok) throw new Error(r.error);
		expect(new Date(cutoff(r.window, now)).toISOString()).toBe('2026-03-02T20:00:00.000Z');
	});
});

describe('parseSince', () => {
	it('accepts every unit', () => {
		expect(parseSince('1h')).toEqual({ ok: true, window: { n: 1, unit: 'h' } });
		expect(parseSince('7d')).toEqual({ ok: true, window: { n: 7, unit: 'd' } });
		expect(parseSince('2w')).toEqual({ ok: true, window: { n: 2, unit: 'w' } });
		expect(parseSince('12mo')).toEqual({ ok: true, window: { n: 12, unit: 'mo' } });
		expect(parseSince('1y')).toEqual({ ok: true, window: { n: 1, unit: 'y' } });
		expect(parseSince('9999d')).toEqual({ ok: true, window: { n: 9999, unit: 'd' } });
		expect(parseSince('07d')).toEqual({ ok: true, window: { n: 7, unit: 'd' } });
	});

	it.each([
		'', '7', 'd', 'mo', '0d', '0h', '10000d', '00007d', '-1d', '+7d', '1m', '1M', '1D', '7H',
		' 7d', '7d ', '7 d', '1.5d', '7days', '7min', '7s', '1d1h', '7d\n', '٣d'
	])('rejects %j', (text) => {
		expect(parseSince(text).ok).toBe(false);
	});

	it('explains the mistakes people make', () => {
		const err = (t: string) => {
			const r = parseSince(t);
			return r.ok ? '' : r.error;
		};
		expect(err('1m')).toContain('ambiguous');
		expect(err('0d')).toContain('between 1 and 9999');
		expect(err('10000d')).toContain('between 1 and 9999');
		expect(err('')).toContain('needs a window');
		expect(err('soon')).toContain('not a window');
	});
});

describe('validSince', () => {
	it('keeps a valid window and drops anything else', () => {
		expect(validSince('7d')).toBe('7d');
		expect(validSince('7D')).toBe('');
		expect(validSince('')).toBe('');
		expect(validSince(undefined)).toBe('');
	});
});

describe('sinceAfter', () => {
	const now = Date.parse('2026-09-10T12:00:00.789Z');
	it('is unix seconds, fixed from now', () => {
		expect(sinceAfter('7d', now)).toBe(Date.parse('2026-09-03T12:00:00Z') / 1000);
		expect(sinceAfter('24h', now)).toBe(Date.parse('2026-09-09T12:00:00Z') / 1000);
	});
	it('is undefined without a window', () => {
		expect(sinceAfter('', now)).toBeUndefined();
		expect(sinceAfter('nonsense', now)).toBeUndefined();
	});
	it('is never negative', () => {
		expect(sinceAfter('9999y', now)).toBe(0);
	});
});

describe('nextSince', () => {
	it('cycles the presets and sends other windows back to any time', () => {
		expect(nextSince('')).toBe('24h');
		expect(nextSince('24h')).toBe('7d');
		expect(nextSince('7d')).toBe('30d');
		expect(nextSince('30d')).toBe('1y');
		expect(nextSince('1y')).toBe('');
		expect(nextSince('2w')).toBe('');
	});
});

describe('describeSince', () => {
	it('words the window for the empty feed', () => {
		expect(describeSince('7d')).toBe('the last 7d');
		expect(describeSince('')).toBe('');
	});
});
