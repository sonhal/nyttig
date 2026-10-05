import { describe, expect, it } from 'vitest';
import { applyToFilter, commonPrefix, completeCommand, parseCommand, type Command } from './command';
import { defaultFilter } from './filter';
import type { Assessor, DigestSeries, SavedView, Source, Tag } from './types';

const sources: Source[] = [
	{ id: '1', name: 'Alpha News', abbreviation: 'ALP' },
	{ id: '2', name: 'Hacker News', abbreviation: 'HN' }
];
const tags: Tag[] = [
	{ id: '1', name: 'linux' },
	{ id: '2', name: 'Release notes' }
];
const views: SavedView[] = [
	{ id: '1', name: 'Security', favorite: true },
	{ id: '2', name: 'Linux news' }
];
const ctx = { sources, tags, views };

const parse = (s: string) => parseCommand(s, ctx);
const cmd = (s: string): Command => {
	const r = parse(s);
	if (!r.ok) throw new Error(r.error);
	return r.cmd;
};
const error = (s: string): string => {
	const r = parse(s);
	if (r.ok) throw new Error('parsed: ' + JSON.stringify(r.cmd));
	return r.error;
};

describe('parseCommand: pages', () => {
	it.each([
		['sources', 'sources'],
		[':sources', 'sources'],
		['  :tags ', 'tags'],
		['RULES', 'rules'],
		['feed', 'feed'],
		// Short forms kept from before the longer commands existed.
		['so', 'sources'],
		['s', 'sources'],
		['r', 'rules'],
		['ta', 'tags'],
		['t', 'tags'],
		['f', 'feed'],
		['q', 'feed'],
		['quit', 'feed'],
		['src', 'sources'],
		['ru', 'rules'],
		['sou', 'sources']
	])('%j → %s', (input, page) => {
		expect(cmd(input)).toEqual({ type: 'page', page });
	});

	it('takes no argument', () => {
		expect(error('sources now')).toBe('sources takes no argument');
		expect(error('help me')).toBe('help takes no argument');
	});
});

describe('parseCommand: saved views', () => {
	it('opens a view by name, any case, or a unique prefix', () => {
		expect(cmd('view Security')).toEqual({ type: 'view', id: '1' });
		expect(cmd('view security')).toEqual({ type: 'view', id: '1' });
		expect(cmd('v linux')).toEqual({ type: 'view', id: '2' });
		expect(cmd('view "Linux news"')).toEqual({ type: 'view', id: '2' });
		expect(cmd('view sec')).toEqual({ type: 'view', id: '1' });
	});

	it('opens the unfiltered feed with all', () => {
		expect(cmd('view all')).toEqual({ type: 'view', id: '' });
		expect(cmd('v *')).toEqual({ type: 'view', id: '' });
		// A view called "all" wins over the keyword.
		const r = parseCommand('view all', { ...ctx, views: [{ id: '9', name: 'all' }] });
		expect(r).toEqual({ ok: true, cmd: { type: 'view', id: '9' } });
	});

	it('explains what is wrong', () => {
		expect(error('view')).toBe('view: a view name, or all');
		expect(error('view nope')).toBe('unknown view: nope');
		expect(parseCommand('view x').ok).toBe(false);
	});

	it('saves into the active view, or under a name', () => {
		expect(cmd('save')).toEqual({ type: 'save', name: '' });
		expect(cmd('save My view')).toEqual({ type: 'save', name: 'My view' });
		expect(cmd('save "Quoted"')).toEqual({ type: 'save', name: 'Quoted' });
		expect(cmd('sav')).toEqual({ type: 'save', name: '' });
	});

	it('opens the management page', () => {
		expect(cmd('views')).toEqual({ type: 'page', page: 'views' });
		expect(error('views x')).toBe('views takes no argument');
		expect(error('vie')).toContain('ambiguous');
	});
});

describe('parseCommand: filter commands', () => {
	it('sort', () => {
		expect(cmd('sort newest')).toEqual({ type: 'sort', sort: 'newest' });
		expect(cmd('sor OLDEST')).toEqual({ type: 'sort', sort: 'oldest' });
		expect(cmd('sort')).toEqual({ type: 'sort', sort: 'toggle' });
		expect(error('sort sideways')).toBe('sort: newest, oldest or score, not sideways');
	});

	it('unviewed', () => {
		expect(cmd('unviewed on')).toEqual({ type: 'unviewed', value: true });
		expect(cmd('un off')).toEqual({ type: 'unviewed', value: false });
		expect(cmd('unviewed')).toEqual({ type: 'unviewed', value: 'toggle' });
		expect(error('unviewed maybe')).toBe('unviewed: on or off, not maybe');
	});

	it('src and tag resolve names, with or without quotes, to IDs', () => {
		expect(cmd('src Hacker News')).toEqual({ type: 'source', id: '2' });
		expect(cmd('src "hacker news"')).toEqual({ type: 'source', id: '2' });
		expect(cmd('src hn')).toEqual({ type: 'source', id: '2' });
		expect(cmd('src all')).toEqual({ type: 'source', id: '' });
		expect(cmd('tag Release notes')).toEqual({ type: 'tag', id: '2' });
		expect(cmd('tag LINUX')).toEqual({ type: 'tag', id: '1' });
		expect(cmd('tag all')).toEqual({ type: 'tag', id: '' });
		expect(error('src nope')).toBe('unknown source: nope');
		expect(error('tag nope')).toBe('unknown tag: nope');
		expect(error('tag')).toContain('a tag name');
	});

	it('a source or tag that is called "all" wins over the keyword', () => {
		expect(parseCommand('tag all', { sources: [], tags: [{ id: '7', name: 'All' }] })).toEqual({
			ok: true,
			cmd: { type: 'tag', id: '7' }
		});
	});

	it('refresh', () => {
		expect(cmd('refresh')).toEqual({ type: 'refresh', source: '' });
		expect(cmd('refresh alpha news')).toEqual({ type: 'refresh', source: '1' });
		expect(error('refresh nope')).toBe('unknown source: nope');
	});

	it('time, follow and help', () => {
		expect(cmd('time')).toEqual({ type: 'time', mode: 'toggle' });
		expect(cmd('time relative')).toEqual({ type: 'time', mode: 'relative' });
		expect(cmd('ti absolute')).toEqual({ type: 'time', mode: 'absolute' });
		expect(error('time fast')).toBe('time: relative or absolute, not fast');
		expect(cmd('follow')).toEqual({ type: 'follow' });
		expect(cmd('help')).toEqual({ type: 'help' });
		expect(cmd('h')).toEqual({ type: 'help' });
		expect(cmd('?')).toEqual({ type: 'help' });
	});
});

describe('parseCommand: errors', () => {
	it('lists the commands for an empty line', () => {
		const e = error(':');
		expect(e).toContain('sources');
		expect(e).toContain('refresh');
	});

	it('rejects unknown commands', () => {
		expect(error('nonsense')).toContain('unknown command: nonsense');
		expect(error('nonsense')).toContain('refresh');
	});

	it('takes unique prefixes', () => {
		expect(cmd('re')).toEqual({ type: 'refresh', source: '' });
		expect(cmd('fol')).toEqual({ type: 'follow' });
	});
});

describe('applyToFilter', () => {
	it('applies filter commands and ignores the others', () => {
		expect(applyToFilter(cmd('sort oldest'), defaultFilter)).toEqual({ ...defaultFilter, sort: 'oldest' });
		expect(applyToFilter(cmd('sort'), { ...defaultFilter, sort: 'oldest' })).toEqual(defaultFilter);
		expect(applyToFilter(cmd('unviewed'), defaultFilter)).toEqual({ ...defaultFilter, unviewed: true });
		expect(applyToFilter(cmd('unviewed off'), { ...defaultFilter, unviewed: true })).toEqual(defaultFilter);
		expect(applyToFilter(cmd('src hn'), defaultFilter)).toEqual({ ...defaultFilter, source: '2' });
		expect(applyToFilter(cmd('tag all'), { ...defaultFilter, tag: '1' })).toEqual(defaultFilter);
		expect(applyToFilter(cmd('help'), defaultFilter)).toBeNull();
		expect(applyToFilter(cmd('sources'), defaultFilter)).toBeNull();
	});
});

describe('completeCommand', () => {
	it('completes command names', () => {
		expect(completeCommand('', ctx)).toMatchObject({ from: 0 });
		expect(completeCommand('so', ctx)).toEqual({ from: 0, candidates: ['sources', 'sort'] });
		expect(completeCommand(':re', ctx)).toEqual({ from: 1, candidates: ['refresh'] });
		expect(completeCommand('zz', ctx).candidates).toEqual([]);
	});

	it('completes arguments', () => {
		expect(completeCommand('sort o', ctx)).toEqual({ from: 5, candidates: ['oldest'] });
		expect(completeCommand('sort ', ctx).candidates).toEqual(['newest', 'oldest', 'score']);
		expect(completeCommand('unviewed o', ctx).candidates).toEqual(['on', 'off']);
		expect(completeCommand('time a', ctx).candidates).toEqual(['absolute']);
		expect(completeCommand('src ha', ctx)).toEqual({ from: 4, candidates: ['Hacker News'] });
		expect(completeCommand('src al', ctx).candidates).toEqual(['Alpha News', 'all']);
		expect(completeCommand('src ', ctx).candidates).toEqual(['Alpha News', 'Hacker News', 'all']);
		expect(completeCommand('tag r', ctx).candidates).toEqual(['Release notes']);
		expect(completeCommand('refresh al', ctx).candidates).toEqual(['Alpha News']);
		// An abbreviated command completes its argument too.
		expect(completeCommand('ta l', ctx).candidates).toEqual([]);
		expect(completeCommand('sor n', ctx).candidates).toEqual(['newest']);
		expect(completeCommand('sources x', ctx).candidates).toEqual([]);
		expect(completeCommand('view ', ctx).candidates).toEqual(['Security', 'Linux news', 'all']);
		expect(completeCommand('v li', ctx)).toEqual({ from: 2, candidates: ['Linux news'] });
		expect(completeCommand('view se', ctx).candidates).toEqual(['Security']);
		expect(completeCommand('save x', ctx).candidates).toEqual([]);
	});
});

describe('commonPrefix', () => {
	it('finds what the words share, ignoring case', () => {
		expect(commonPrefix(['sources', 'sort'])).toBe('so');
		expect(commonPrefix(['Hacker', 'hackney'])).toBe('Hack');
		expect(commonPrefix(['a'])).toBe('a');
		expect(commonPrefix([])).toBe('');
		expect(commonPrefix(['abc', 'xyz'])).toBe('');
	});
});

describe(':rate', () => {
	it('takes a score and an optional note', () => {
		expect(cmd('rate 0.8')).toEqual({ type: 'rate', score: 0.8, note: '' });
		expect(cmd('rate 1 worth a read, really')).toEqual({ type: 'rate', score: 1, note: 'worth a read, really' });
		expect(cmd('rate .25')).toEqual({ type: 'rate', score: 0.25, note: '' });
		expect(cmd('rate 0')).toEqual({ type: 'rate', score: 0, note: '' });
		expect(cmd('ra 0.5 x')).toEqual({ type: 'rate', score: 0.5, note: 'x' });
	});

	it('needs a score from 0 to 1', () => {
		expect(error('rate')).toContain('a score from 0 to 1');
		expect(error('rate high')).toContain('not high');
		expect(error('rate 2')).toContain('not 2');
		expect(error('rate -1')).toContain('not -1');
	});

	it('does not take over :r (rules) or :re (refresh)', () => {
		expect(cmd('r')).toEqual({ type: 'page', page: 'rules' });
		expect(cmd('re')).toEqual({ type: 'refresh', source: '' });
	});

	it('does nothing to the filter', () => {
		expect(applyToFilter({ type: 'rate', score: 1, note: '' }, defaultFilter)).toBeNull();
	});
});

describe('assessor commands', () => {
	const assessors: Assessor[] = [
		{ id: '1', name: 'claude' },
		{ id: '2', name: 'CVSS reader' },
		{ id: '3', name: 'gpt 4' }
	];
	const actx = { sources, tags, views, assessors };
	const acmd = (s: string): Command => {
		const r = parseCommand(s, actx);
		if (!r.ok) throw new Error(r.error);
		return r.cmd;
	};
	const aerr = (s: string): string => {
		const r = parseCommand(s, actx);
		if (r.ok) throw new Error('parsed: ' + JSON.stringify(r.cmd));
		return r.error;
	};

	it(':assessors is a page', () => {
		expect(acmd('assessors')).toEqual({ type: 'page', page: 'assessors' });
		expect(acmd('ass')).toEqual({ type: 'page', page: 'assessors' });
		expect(aerr('assessors x')).toBe('assessors takes no argument');
	});

	it(':score picks an assessor, with an optional minimum', () => {
		expect(acmd('score claude')).toEqual({ type: 'score', id: '1', min: null });
		expect(acmd('sc CLAUDE 0.7')).toEqual({ type: 'score', id: '1', min: 0.7 });
		expect(acmd('score claude >=0.7')).toEqual({ type: 'score', id: '1', min: 0.7 });
		expect(acmd('score claude 0')).toEqual({ type: 'score', id: '1', min: 0 });
		expect(acmd('score "CVSS reader" 0.5')).toEqual({ type: 'score', id: '2', min: 0.5 });
		expect(acmd('score CVSS reader .5')).toEqual({ type: 'score', id: '2', min: 0.5 });
		// The whole text is a name first.
		expect(acmd('score gpt 4')).toEqual({ type: 'score', id: '3', min: null });
		expect(acmd('score all')).toEqual({ type: 'score', id: '', min: null });
		expect(aerr('score')).toBe('score: an assessor name, or all');
		expect(aerr('score nobody')).toBe('unknown assessor: nobody');
		expect(aerr('score nobody 0.5')).toBe('unknown assessor: nobody');
		expect(aerr('score claude 1.5')).toContain('the minimum is a number from 0 to 1');
	});

	it(':unassessed picks the assessor whose work is open', () => {
		expect(acmd('unassessed claude')).toEqual({ type: 'unassessed', id: '1' });
		expect(acmd('unassessed all')).toEqual({ type: 'unassessed', id: '' });
		expect(aerr('unassessed')).toBe('unassessed: an assessor name, or all');
		expect(aerr('unassessed nobody')).toBe('unknown assessor: nobody');
	});

	it(':u and :un stay :unviewed', () => {
		expect(acmd('u')).toEqual({ type: 'unviewed', value: 'toggle' });
		expect(acmd('un on')).toEqual({ type: 'unviewed', value: true });
		expect(acmd('unv off')).toEqual({ type: 'unviewed', value: false });
		expect(acmd('unas claude')).toEqual({ type: 'unassessed', id: '1' });
	});

	it(':sort score', () => {
		expect(acmd('sort score')).toEqual({ type: 'sort', sort: 'score' });
	});

	it('apply to the filter', () => {
		const base = { ...defaultFilter, assessor: '1', minScore: 0.5, sort: 'score' as const, unassessed: '2' };
		expect(applyToFilter({ type: 'score', id: '2', min: 0.8 }, base)).toEqual({ ...base, assessor: '2', minScore: 0.8 });
		expect(applyToFilter({ type: 'score', id: '2', min: null }, base)).toEqual({ ...base, assessor: '2', minScore: null });
		// "all" drops the assessor, the minimum and the score sort, not the unassessed filter.
		expect(applyToFilter({ type: 'score', id: '', min: null }, base)).toEqual({ ...defaultFilter, unassessed: '2' });
		expect(applyToFilter({ type: 'unassessed', id: '' }, base)).toEqual({ ...base, unassessed: '' });
		expect(applyToFilter({ type: 'unassessed', id: '3' }, defaultFilter)).toEqual({ ...defaultFilter, unassessed: '3' });
		expect(applyToFilter({ type: 'sort', sort: 'toggle' }, base)).toEqual({ ...base, sort: 'newest' });
	});

	it('complete assessor names', () => {
		expect(completeCommand('score cl', actx).candidates).toEqual(['claude']);
		expect(completeCommand('score c', actx).candidates).toEqual(['claude', 'CVSS reader']);
		expect(completeCommand('score ', actx).candidates).toEqual(['claude', 'CVSS reader', 'gpt 4', 'all']);
		expect(completeCommand('unassessed cv', actx).candidates).toEqual(['CVSS reader']);
		expect(completeCommand('score c', ctx).candidates).toEqual([]);
	});
});

describe('digest commands', () => {
	const series: DigestSeries[] = [
		{ id: '1', assessor_id: '10', assessor_name: 'claude', name: 'daily-cve' },
		{ id: '2', assessor_id: '20', assessor_name: 'gpt', name: 'daily-cve' },
		{ id: '3', assessor_id: '10', assessor_name: 'claude', name: 'monthly' }
	];
	const dctx = { sources, tags, views, series };
	const dparse = (s: string) => parseCommand(s, dctx);

	it(':digests is a page, with short forms', () => {
		expect(dparse('digests')).toEqual({ ok: true, cmd: { type: 'page', page: 'digests' } });
		expect(dparse('d')).toEqual({ ok: true, cmd: { type: 'page', page: 'digests' } });
		expect(dparse('digests x')).toEqual({ ok: false, error: 'digests takes no argument' });
	});

	it(':digest opens a series by assessor/name', () => {
		expect(dparse('digest claude/monthly')).toEqual({ ok: true, cmd: { type: 'digest', series: '3' } });
		expect(dparse('digest GPT/Daily-CVE')).toEqual({ ok: true, cmd: { type: 'digest', series: '2' } });
		expect(dparse('digest "claude/monthly"')).toEqual({ ok: true, cmd: { type: 'digest', series: '3' } });
		expect(dparse('digest monthly')).toEqual({ ok: true, cmd: { type: 'digest', series: '3' } });
	});

	it(':digest says what is wrong', () => {
		expect(dparse('digest')).toEqual({ ok: false, error: 'digest: <assessor>/<series>' });
		expect(dparse('digest claude/nope')).toEqual({ ok: false, error: 'unknown series: claude/nope' });
		const r = dparse('digest daily-cve');
		expect(!r.ok && r.error).toMatch(/^ambiguous series/);
		expect(parseCommand('digest claude/monthly', { sources, tags })).toMatchObject({ ok: false });
	});

	it('the two names are not mixed up by prefixes', () => {
		expect(dparse('dig')).toMatchObject({ ok: false });
		expect(dparse('digest claude/monthly').ok).toBe(true);
	});

	it('completes series names', () => {
		expect(completeCommand('digest ', dctx).candidates).toEqual(['claude/daily-cve', 'gpt/daily-cve', 'claude/monthly']);
		expect(completeCommand('digest cl', dctx)).toEqual({ from: 7, candidates: ['claude/daily-cve', 'claude/monthly'] });
		expect(completeCommand('digest g', dctx).candidates).toEqual(['gpt/daily-cve']);
		expect(completeCommand('digest ', { sources, tags }).candidates).toEqual([]);
		expect(completeCommand('digests', dctx).candidates).toEqual(['digests']);
	});
});
