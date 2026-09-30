import { describe, expect, it } from 'vitest';
import { applyToFilter, commonPrefix, completeCommand, parseCommand, type Command } from './command';
import { defaultFilter } from './filter';
import type { Source, Tag } from './types';

const sources: Source[] = [
	{ id: '1', name: 'Alpha News', abbreviation: 'ALP' },
	{ id: '2', name: 'Hacker News', abbreviation: 'HN' }
];
const tags: Tag[] = [
	{ id: '1', name: 'linux' },
	{ id: '2', name: 'Release notes' }
];
const ctx = { sources, tags };

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

describe('parseCommand: views', () => {
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
	])('%j → %s', (input, view) => {
		expect(cmd(input)).toEqual({ type: 'view', view });
	});

	it('takes no argument', () => {
		expect(error('sources now')).toBe('sources takes no argument');
		expect(error('help me')).toBe('help takes no argument');
	});
});

describe('parseCommand: filter commands', () => {
	it('sort', () => {
		expect(cmd('sort newest')).toEqual({ type: 'sort', sort: 'newest' });
		expect(cmd('sor OLDEST')).toEqual({ type: 'sort', sort: 'oldest' });
		expect(cmd('sort')).toEqual({ type: 'sort', sort: 'toggle' });
		expect(error('sort sideways')).toBe('sort: newest or oldest, not sideways');
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
		expect(completeCommand('sort ', ctx).candidates).toEqual(['newest', 'oldest']);
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
