import { describe, expect, it } from 'vitest';
import { archiveBadge, canDownload, fileHref, formatBytes, inFlight, nextStatusFilter } from './archives';
import { parseCommand } from './command';
import { keyAction } from './keymap';

describe('archiveBadge', () => {
	it.each([
		['pending', '…'],
		['running', '…'],
		['done', '⤓'],
		['partial', '⤓~'],
		['failed', '✗']
	])('%s', (status, glyph) => {
		expect(archiveBadge({ id: '1', status })?.glyph).toBe(glyph);
	});

	it('has none for no archive or an unknown status', () => {
		expect(archiveBadge(undefined)).toBeNull();
		expect(archiveBadge({ id: '1' })).toBeNull();
		expect(archiveBadge({ id: '1', status: 'bogus' })).toBeNull();
	});
});

describe('archive helpers', () => {
	it('knows what is in flight', () => {
		expect(inFlight('pending')).toBe(true);
		expect(inFlight('running')).toBe(true);
		expect(inFlight('done')).toBe(false);
		expect(inFlight(undefined)).toBe(false);
	});

	it('downloads whenever bytes are stored, even for a failed archive', () => {
		expect(canDownload({ status: 'done', size: '120' })).toBe(true);
		expect(canDownload({ status: 'failed', size: '120' })).toBe(true);
		expect(canDownload({ status: 'failed' })).toBe(false);
		expect(canDownload({ status: 'done', size: '0' })).toBe(false);
		expect(canDownload(undefined)).toBe(false);
	});

	it('builds the file URL from an escaped ID', () => {
		expect(fileHref('12')).toBe('/api/archives/12/file');
		expect(fileHref('../x')).toBe('/api/archives/..%2Fx/file');
	});

	it('cycles the status filter through every status and back to all', () => {
		const seen = [''];
		for (let s = nextStatusFilter(''); s !== ''; s = nextStatusFilter(s)) seen.push(s);
		expect(seen).toEqual(['', 'pending', 'running', 'done', 'partial', 'failed']);
		expect(nextStatusFilter('nonsense')).toBe('');
	});

	it('formats sizes', () => {
		expect(formatBytes(undefined)).toBe('');
		expect(formatBytes('0')).toBe('');
		expect(formatBytes('512')).toBe('512 B');
		expect(formatBytes('2048')).toBe('2.0 kB');
		expect(formatBytes('5242880')).toBe('5.0 MB');
	});
});

describe('archive command and key', () => {
	it('parses :archive and :archives', () => {
		expect(parseCommand('archive')).toEqual({ ok: true, cmd: { type: 'archive' } });
		expect(parseCommand('archives')).toEqual({ ok: true, cmd: { type: 'page', page: 'archives' } });
		expect(parseCommand('archive now').ok).toBe(false);
		expect(parseCommand('archives x').ok).toBe(false);
	});

	it('binds b in the feed', () => {
		expect(keyAction('normal', { key: 'b' })).toEqual({ type: 'archive' });
		expect(keyAction('search', { key: 'b' })).toBeNull();
	});
});
