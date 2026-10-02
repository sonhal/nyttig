import { describe, expect, it } from 'vitest';
import {
	formatInterval,
	hasErrors,
	parseInterval,
	ruleBody,
	ruleForm,
	sameRule,
	sourceAddBody,
	sourceForm,
	sourcePatchBody,
	tagAddBody,
	tagForm,
	tagPatchBody,
	urlError,
	validateRule,
	validateSource,
	validateTag,
	validateView,
	viewAddBody,
	viewForm,
	viewPatchBody,
	type SourceForm
} from './forms';
import type { SavedView, Source, Tag } from './types';

describe('parseInterval / formatInterval', () => {
	it.each([
		['3600', 3600],
		['90s', 90],
		['30m', 1800],
		['1h', 3600],
		['2d', 172800],
		['1h30m', 5400],
		[' 1H 30M ', 5400],
		['', null],
		['h', null],
		['1x', null],
		['1h 30', null],
		['-5m', null],
		['1.5h', null]
	])('parses %j', (input, want) => {
		expect(parseInterval(input)).toBe(want);
	});

	it.each([
		[3600, '1h'],
		[5400, '1h30m'],
		[60, '1m'],
		[90, '1m30s'],
		[604800, '7d'],
		[0, ''],
		[undefined, '']
	])('formats %j as %j', (sec, want) => {
		expect(formatInterval(sec)).toBe(want);
	});

	it('round-trips', () => {
		for (const sec of [60, 61, 600, 3599, 3600, 86400 + 60, 604800]) {
			expect(parseInterval(formatInterval(sec))).toBe(sec);
		}
	});
});

describe('urlError', () => {
	it('accepts absolute http(s) URLs', () => {
		expect(urlError('https://example.com/feed.xml')).toBeUndefined();
		expect(urlError('http://127.0.0.1:7811/alpha.xml')).toBeUndefined();
	});
	it.each([
		['', 'required'],
		['example.com/feed', 'valid'],
		['ftp://example.com/feed', 'http'],
		['javascript:alert(1)', 'http'],
		['file:///etc/passwd', 'http'],
		['https://user:pw@example.com/', 'credentials'],
		['https://example.com/' + 'a'.repeat(2048), 'bytes']
	])('rejects %j', (url, msg) => {
		expect(urlError(url)).toContain(msg);
	});
});

describe('source form', () => {
	const valid: SourceForm = {
		name: 'Hacker News',
		url: 'https://news.ycombinator.com/rss',
		type: 'rss',
		refresh: '10m',
		enabled: true,
		color: '#FF6600',
		abbreviation: 'HN'
	};

	it('defaults a new source to enabled, rss, every hour', () => {
		expect(sourceForm()).toEqual({
			name: '',
			url: '',
			type: 'rss',
			refresh: '1h',
			enabled: true,
			color: '',
			abbreviation: ''
		});
	});

	it('validates like the daemon', () => {
		expect(validateSource(valid)).toEqual({});
		const e = validateSource({
			name: '  ',
			url: 'ftp://x',
			type: 'rss',
			refresh: '30s',
			enabled: true,
			color: 'red',
			abbreviation: 'x'.repeat(17)
		});
		expect(Object.keys(e).sort()).toEqual(['abbreviation', 'color', 'name', 'refresh', 'url']);
		expect(validateSource({ ...valid, refresh: '8d' }).refresh).toBeDefined();
		expect(validateSource({ ...valid, refresh: 'soon' }).refresh).toBeDefined();
		expect(validateSource({ ...valid, name: 'a\u0007b' }).name).toContain('control');
		expect(validateSource({ ...valid, name: 'é'.repeat(200) })).toEqual({});
		expect(validateSource({ ...valid, name: 'é'.repeat(201) }).name).toBeDefined();
		// Color and abbreviation are optional.
		expect(validateSource({ ...valid, color: '', abbreviation: '' })).toEqual({});
	});

	it('always sends enabled when adding (proto3 defaults it to false)', () => {
		expect(sourceAddBody({ ...valid, enabled: false })).toMatchObject({ enabled: false });
		expect(sourceAddBody(valid)).toEqual({
			name: 'Hacker News',
			url: 'https://news.ycombinator.com/rss',
			type: 'rss',
			refresh_sec: 600,
			enabled: true,
			color: '#FF6600',
			abbreviation: 'HN'
		});
		// Empty optional fields are left out, and text is trimmed.
		expect(sourceAddBody({ ...valid, name: ' HN ', color: '', abbreviation: ' ' })).toEqual({
			name: 'HN',
			url: 'https://news.ycombinator.com/rss',
			type: 'rss',
			refresh_sec: 600,
			enabled: true
		});
	});

	it('patches only what changed', () => {
		const orig: Source = {
			id: '1',
			name: 'Hacker News',
			url: 'https://news.ycombinator.com/rss',
			type: 'rss',
			refresh_sec: 600,
			enabled: true,
			color: '#FF6600',
			abbreviation: 'HN'
		};
		const f = sourceForm(orig);
		expect(sourcePatchBody(orig, f)).toEqual({});
		expect(sourcePatchBody(orig, { ...f, name: 'HN front page' })).toEqual({ name: 'HN front page' });
		expect(sourcePatchBody(orig, { ...f, enabled: false })).toEqual({ enabled: false });
		expect(sourcePatchBody(orig, { ...f, refresh: '1h' })).toEqual({ refresh_sec: 3600 });
		// Clearing sends "" so the daemon clears it.
		expect(sourcePatchBody(orig, { ...f, color: '', abbreviation: '' })).toEqual({ color: '', abbreviation: '' });
	});

	it('keeps a bluesky source a bluesky source, and its name optional', () => {
		const orig: Source = {
			id: '4',
			name: 'Alice',
			url: 'https://bsky.app/profile/did:plc:z72i7hdynmk6r22z27h6tvur',
			type: 'bluesky',
			refresh_sec: 3600,
			enabled: true
		};
		const f = sourceForm(orig);
		expect(f.type).toBe('bluesky');
		// Editing something else must not send a type change (or flip it to rss).
		expect(sourcePatchBody(orig, f)).toEqual({});
		expect(sourcePatchBody(orig, { ...f, enabled: false })).toEqual({ enabled: false });

		// A handle, a DID or a profile URL is accepted instead of a feed URL.
		const add: SourceForm = { ...f, name: '', url: 'alice.bsky.social' };
		expect(validateSource(add)).toEqual({});
		expect(validateSource({ ...add, url: '@alice.bsky.social' })).toEqual({});
		expect(validateSource({ ...add, url: 'did:plc:z72i7hdynmk6r22z27h6tvur' })).toEqual({});
		expect(validateSource({ ...add, url: 'https://bsky.app/profile/alice.bsky.social' })).toEqual({});
		expect(validateSource({ ...add, url: '' }).url).toBeDefined();
		expect(validateSource({ ...add, url: 'alice bsky social' }).url).toBeDefined();
		// A blank name is left out, so the daemon names the source after the account.
		expect(sourceAddBody(add)).toMatchObject({ type: 'bluesky', url: 'alice.bsky.social', name: '' });
		// ...and on an edit it keeps the current name.
		expect(sourcePatchBody(orig, { ...f, name: '' })).toEqual({});
		expect(sourcePatchBody(orig, { ...f, name: 'Al' })).toEqual({ name: 'Al' });

		// The name stays required for rss and atom.
		for (const type of ['rss', 'atom'] as const) {
			expect(validateSource({ ...valid, type, name: '' }).name).toBeDefined();
		}
		// A bluesky account is not a feed URL.
		expect(validateSource({ ...valid, type: 'rss', url: 'alice.bsky.social' }).url).toBeDefined();
	});

	it('treats a missing type as rss and a missing enabled as false', () => {
		const orig: Source = { id: '2', name: 'x', url: 'https://x.example/f', refresh_sec: 3600 };
		const f = sourceForm(orig);
		expect(f.type).toBe('rss');
		expect(f.enabled).toBe(false);
		expect(sourcePatchBody(orig, f)).toEqual({});
	});
});

describe('tag form', () => {
	it('validates and builds bodies', () => {
		expect(validateTag({ name: 'rust', color: '#CE422B', parent_ids: [] })).toEqual({});
		expect(Object.keys(validateTag({ name: '', color: '#abc', parent_ids: [] })).sort()).toEqual(['color', 'name']);
		expect(validateTag({ name: 'x'.repeat(65), color: '', parent_ids: [] }).name).toBeDefined();
		expect(tagAddBody({ name: ' rust ', color: '', parent_ids: [] })).toEqual({ name: 'rust' });
		const orig = { id: '3', name: 'rust', color: '#CE422B' };
		expect(tagPatchBody(orig, tagForm(orig))).toEqual({});
		expect(tagPatchBody(orig, { name: 'rust', color: '#000000', parent_ids: [] })).toEqual({ color: '#000000' });
		expect(tagPatchBody(orig, { name: 'rustlang', color: '', parent_ids: [] })).toEqual({ name: 'rustlang', color: '' });
	});

	it('handles parents', () => {
		expect(tagAddBody({ name: 'CVE', color: '', parent_ids: ['1', '2'] })).toEqual({ name: 'CVE', parent_ids: ['1', '2'] });
		const many = Array.from({ length: 17 }, (_, i) => String(i + 1));
		expect(validateTag({ name: 'x', color: '', parent_ids: many }).parent_ids).toBeDefined();

		const orig = { id: '3', name: 'CVE', parent_ids: ['1', '2'] };
		expect(tagForm(orig).parent_ids).toEqual(['1', '2']);
		// Same set in another order: nothing to send.
		expect(tagPatchBody(orig, { name: 'CVE', color: '', parent_ids: ['2', '1'] })).toEqual({});
		expect(tagPatchBody(orig, { name: 'CVE', color: '', parent_ids: ['1'] })).toEqual({ parent_ids: ['1'] });
		// Clearing sends [] so the daemon makes the tag top-level.
		expect(tagPatchBody(orig, { name: 'CVE', color: '', parent_ids: [] })).toEqual({ parent_ids: [] });
	});
});

describe('rule form', () => {
	it('starts from a rule or a default tag', () => {
		expect(ruleForm(undefined, '4')).toEqual({ tag_id: '4', source_id: '', field: 'both', pattern: '', priority: '0' });
		expect(
			ruleForm({ id: '1', tag_id: '2', source_id: '3', field: 'title', pattern: '(?i)go', priority: 5 })
		).toEqual({ tag_id: '2', source_id: '3', field: 'title', pattern: '(?i)go', priority: '5' });
	});

	it('checks the pattern only for presence and length, never as a JS regex', () => {
		const f = ruleForm(undefined, '1');
		// Go RE2 syntax that JavaScript rejects must not be an error here.
		expect(validateRule({ ...f, pattern: '(?i)\\brust\\b' })).toEqual({});
		expect(validateRule({ ...f, pattern: '(?P<name>x)' })).toEqual({});
		expect(validateRule({ ...f, pattern: '' }).pattern).toContain('required');
		expect(validateRule({ ...f, pattern: 'é'.repeat(513) }).pattern).toContain('bytes');
		expect(validateRule({ ...f, pattern: 'x', tag_id: '' }).tag_id).toBeDefined();
		expect(validateRule({ ...f, pattern: 'x', priority: '1.5' }).priority).toBeDefined();
		expect(validateRule({ ...f, pattern: 'x', priority: '2147483648' }).priority).toBeDefined();
		expect(hasErrors(validateRule({ ...f, pattern: 'x', priority: '-3' }))).toBe(false);
	});

	it('sends the pattern exactly as typed', () => {
		expect(ruleBody({ tag_id: '2', source_id: '', field: 'title', pattern: ' go ', priority: '0' })).toEqual({
			tag_id: '2',
			field: 'title',
			pattern: ' go '
		});
		expect(ruleBody({ tag_id: '2', source_id: '7', field: 'both', pattern: 'x', priority: '3' })).toEqual({
			tag_id: '2',
			source_id: '7',
			field: 'both',
			pattern: 'x',
			priority: 3
		});
	});

	it('knows when an edit changes nothing', () => {
		const r = { id: '1', tag_id: '2', field: 'title', pattern: 'go', priority: 0 };
		expect(sameRule(r, ruleForm(r))).toBe(true);
		expect(sameRule(r, { ...ruleForm(r), priority: ' 0 ' })).toBe(true);
		expect(sameRule(r, { ...ruleForm(r), pattern: 'go ' })).toBe(false);
		expect(sameRule(r, { ...ruleForm(r), source_id: '5' })).toBe(false);
		expect(sameRule(r, { ...ruleForm(r), field: 'both' })).toBe(false);
	});
});

describe('view forms', () => {
	const sources: Source[] = [{ id: '5', name: 'Hacker News', abbreviation: 'HN' }];
	const tags: Tag[] = [{ id: '4', name: 'cyber security' }];
	const orig: SavedView = {
		id: '1',
		name: 'Security',
		filter: { q: 'openssl', source: '5', tag: '4', sort: 'oldest', unviewed: true },
		favorite: true,
		position: 0
	};

	it('shows the filter as query text, and a new view is a favorite', () => {
		expect(viewForm(orig, sources, tags)).toEqual({
			name: 'Security',
			query: 'openssl src:"Hacker News" tag:"cyber security" is:unviewed sort:oldest',
			favorite: true
		});
		expect(viewForm()).toEqual({ name: '', query: '', favorite: true });
		expect(viewForm({ id: '2', name: 'x' }, sources, tags)).toEqual({ name: 'x', query: '', favorite: false });
	});

	it('validates the name and the query', () => {
		const ok = viewForm(orig, sources, tags);
		expect(validateView(ok, sources, tags)).toEqual({});
		expect(validateView({ ...ok, name: ' ' }, sources, tags).name).toContain('required');
		expect(validateView({ ...ok, name: 'x'.repeat(65) }, sources, tags).name).toContain('64');
		expect(validateView({ ...ok, name: 'x'.repeat(64) }, sources, tags)).toEqual({});
		expect(validateView({ ...ok, query: 'tag:nope' }, sources, tags).query).toBe('unknown tag: nope');
		expect(validateView({ ...ok, query: 'sort:sideways' }, sources, tags).query).toContain('sort');
		expect(validateView({ ...ok, query: '' }, sources, tags)).toEqual({});
	});

	it('builds the add body from the query text', () => {
		const f = { name: ' Sec ', query: 'openssl tag:"cyber security" is:unviewed', favorite: false };
		expect(viewAddBody(f, sources, tags)).toEqual({
			name: 'Sec',
			filter: { q: 'openssl', tag: '4', unviewed: true },
			favorite: false
		});
		expect(viewAddBody({ name: 'All', query: '', favorite: true }, sources, tags)).toEqual({
			name: 'All',
			filter: {},
			favorite: true
		});
	});

	it('keeps the window of a view: shown in the query, sent as typed', () => {
		const week: SavedView = { id: '2', name: 'Week', filter: { tag: '4', since: '7d' } };
		expect(viewForm(week, sources, tags).query).toBe('tag:"cyber security" since:7d');
		expect(viewAddBody({ name: 'w', query: 'since:30d tag:#4', favorite: true }, sources, tags)).toEqual({
			name: 'w',
			filter: { tag: '4', since: '30d' },
			favorite: true
		});
		expect(validateView({ name: 'w', query: 'since:1m', favorite: true }, sources, tags).query).toContain('ambiguous');
		const f = viewForm(week, sources, tags);
		expect(viewPatchBody(week, f, sources, tags)).toEqual({});
		expect(viewPatchBody(week, { ...f, query: 'tag:#4 since:1y' }, sources, tags)).toEqual({ filter: { tag: '4', since: '1y' } });
		// Dropping the window drops it from the stored filter.
		expect(viewPatchBody(week, { ...f, query: 'tag:#4' }, sources, tags)).toEqual({ filter: { tag: '4' } });
	});

	it('patches only what changed, sending the whole filter when it does', () => {
		const f = viewForm(orig, sources, tags);
		expect(viewPatchBody(orig, f, sources, tags)).toEqual({});
		expect(viewPatchBody(orig, { ...f, name: 'Sec' }, sources, tags)).toEqual({ name: 'Sec' });
		expect(viewPatchBody(orig, { ...f, favorite: false }, sources, tags)).toEqual({ favorite: false });
		expect(viewPatchBody(orig, { ...f, query: 'tag:"cyber security"' }, sources, tags)).toEqual({ filter: { tag: '4' } });
		// Clearing the query clears the filter: an empty filter is sent, not omitted.
		expect(viewPatchBody(orig, { ...f, query: '' }, sources, tags)).toEqual({ filter: {} });
		// Respelling the same filter is no change.
		expect(
			viewPatchBody(orig, { ...f, query: 'sort:oldest is:unviewed tag:#4 src:HN openssl' }, sources, tags)
		).toEqual({});
	});
});
