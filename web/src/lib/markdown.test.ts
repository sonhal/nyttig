import { describe, expect, it } from 'vitest';
import { MAX_DEPTH, parseInlines, parseMarkdown, type Block, type Inline } from './markdown';

const t = (text: string): Inline => ({ t: 'text', text });
const para = (...children: Inline[]): Block => ({ t: 'para', children });

/** Every node of a tree, to check what kinds of node a body can produce. */
function walk(blocks: Block[], inl: (i: Inline) => void, blk: (b: Block) => void = () => {}) {
	const inlines = (xs: Inline[]) =>
		xs.forEach((x) => {
			inl(x);
			if (x.t === 'strong' || x.t === 'em' || x.t === 'link') inlines(x.children);
		});
	for (const b of blocks) {
		blk(b);
		if (b.t === 'heading' || b.t === 'para') inlines(b.children);
		else if (b.t === 'quote') walk(b.children, inl, blk);
		else if (b.t === 'list') for (const it of b.items) walk(it, inl, blk);
	}
}

function depthOf(blocks: Block[]): number {
	let d = 0;
	for (const b of blocks) {
		if (b.t === 'quote') d = Math.max(d, 1 + depthOf(b.children));
		else if (b.t === 'list') for (const it of b.items) d = Math.max(d, 1 + depthOf(it));
	}
	return d;
}

describe('blocks', () => {
	it('headings, with levels capped at 3 and closing hashes dropped', () => {
		expect(parseMarkdown('# One\n## Two ##\n### Three\n###### Six\n#')).toEqual([
			{ t: 'heading', level: 1, children: [t('One')] },
			{ t: 'heading', level: 2, children: [t('Two')] },
			{ t: 'heading', level: 3, children: [t('Three')] },
			{ t: 'heading', level: 3, children: [t('Six')] },
			{ t: 'heading', level: 1, children: [] }
		]);
		// No space after the hashes: not a heading. A hash inside a word is kept.
		expect(parseMarkdown('#nope')).toEqual([para(t('#nope'))]);
		expect(parseMarkdown('# C#')).toEqual([{ t: 'heading', level: 1, children: [t('C#')] }]);
	});

	it('paragraphs break at blank lines and keep line breaks', () => {
		expect(parseMarkdown('one\ntwo\n\nthree')).toEqual([para(t('one'), { t: 'br' }, t('two')), para(t('three'))]);
		expect(parseMarkdown('')).toEqual([]);
		expect(parseMarkdown('\n\n  \n')).toEqual([]);
	});

	it('bullet and numbered lists', () => {
		expect(parseMarkdown('- a\n- b\n* c')).toEqual([{ t: 'list', ordered: false, items: [[para(t('a'))], [para(t('b'))], [para(t('c'))]] }]);
		expect(parseMarkdown('1. a\n2) b')).toEqual([{ t: 'list', ordered: true, items: [[para(t('a'))], [para(t('b'))]] }]);
		// A blank line between items keeps one list.
		expect(parseMarkdown('- a\n\n- b')).toMatchObject([{ t: 'list', items: [[para(t('a'))], [para(t('b'))]] }]);
		// A paragraph after a blank line is not part of the list.
		expect(parseMarkdown('- a\n\nafter')).toEqual([{ t: 'list', ordered: false, items: [[para(t('a'))]] }, para(t('after'))]);
	});

	it('nested lists and a list that interrupts a paragraph', () => {
		expect(parseMarkdown('- a\n  - b\n    - c\n- d')).toEqual([
			{
				t: 'list',
				ordered: false,
				items: [
					[para(t('a')), { t: 'list', ordered: false, items: [[para(t('b')), { t: 'list', ordered: false, items: [[para(t('c'))]] }]] }],
					[para(t('d'))]
				]
			}
		]);
		expect(parseMarkdown('intro\n- a')).toEqual([para(t('intro')), { t: 'list', ordered: false, items: [[para(t('a'))]] }]);
	});

	it('a lazy line continues the item', () => {
		expect(parseMarkdown('- a\nmore')).toEqual([{ t: 'list', ordered: false, items: [[para(t('a'), { t: 'br' }, t('more'))]] }]);
	});

	it('does not take a bold line or a minus sign for a list', () => {
		expect(parseMarkdown('**bold** start')[0]).toMatchObject({ t: 'para' });
		expect(parseMarkdown('-1 degree')[0]).toMatchObject({ t: 'para' });
		expect(parseMarkdown('- ')[0]).toMatchObject({ t: 'para' });
	});

	it('block quotes, with lazy lines and nested quotes', () => {
		expect(parseMarkdown('> quoted\n> more\n\nafter')).toEqual([
			{ t: 'quote', children: [para(t('quoted'), { t: 'br' }, t('more'))] },
			para(t('after'))
		]);
		expect(parseMarkdown('> a\nlazy')).toEqual([{ t: 'quote', children: [para(t('a'), { t: 'br' }, t('lazy'))] }]);
		expect(parseMarkdown('> > deep')).toEqual([{ t: 'quote', children: [{ t: 'quote', children: [para(t('deep'))] }] }]);
		expect(parseMarkdown('> - item')).toEqual([{ t: 'quote', children: [{ t: 'list', ordered: false, items: [[para(t('item'))]] }] }]);
	});

	it('fenced code keeps its text verbatim, to the closing fence or the end', () => {
		expect(parseMarkdown('```go\nfunc *x() {}\n# not a heading\n```\nafter')).toEqual([
			{ t: 'code', text: 'func *x() {}\n# not a heading' },
			para(t('after'))
		]);
		expect(parseMarkdown('~~~\n<script>x</script>\n~~~')).toEqual([{ t: 'code', text: '<script>x</script>' }]);
		expect(parseMarkdown('```\nunclosed\n- a')).toEqual([{ t: 'code', text: 'unclosed\n- a' }]);
		// A shorter fence does not close a longer one.
		expect(parseMarkdown('````\n```\n````')).toEqual([{ t: 'code', text: '```' }]);
	});

	it('horizontal rules', () => {
		expect(parseMarkdown('a\n\n---\n\n* * *\n___')).toEqual([para(t('a')), { t: 'hr' }, { t: 'hr' }, { t: 'hr' }]);
		expect(parseMarkdown('--')).toEqual([para(t('--'))]);
	});

	it('tabs indent like four spaces', () => {
		expect(parseMarkdown('- a\n\t- b')).toMatchObject([{ t: 'list', items: [[para(t('a')), { t: 'list' }]] }]);
	});
});

describe('inlines', () => {
	it('strong, em and code', () => {
		expect(parseInlines('a **b** c *d* `e`')).toEqual([
			t('a '),
			{ t: 'strong', children: [t('b')] },
			t(' c '),
			{ t: 'em', children: [t('d')] },
			t(' '),
			{ t: 'code', text: 'e' }
		]);
		expect(parseInlines('**a *b* c**')).toEqual([{ t: 'strong', children: [t('a '), { t: 'em', children: [t('b')] }, t(' c')] }]);
		expect(parseInlines('*a **b** c*')).toEqual([{ t: 'em', children: [t('a '), { t: 'strong', children: [t('b')] }, t(' c')] }]);
	});

	it('code is verbatim, with longer fences for backticks', () => {
		expect(parseInlines('`**x** <b>`')).toEqual([{ t: 'code', text: '**x** <b>' }]);
		expect(parseInlines('`` a ` b ``')).toEqual([{ t: 'code', text: ' a ` b ' }]);
		expect(parseInlines('`open')).toEqual([t('`open')]);
	});

	it('unclosed and spaced markers are text', () => {
		expect(parseInlines('**open')).toEqual([t('**open')]);
		expect(parseInlines('*open')).toEqual([t('*open')]);
		expect(parseInlines('2 * 3 * 4')).toEqual([t('2 * 3 * 4')]);
		expect(parseInlines('a ** b ** c')).toEqual([t('a ** b ** c')]);
		expect(parseInlines('****')).toEqual([t('****')]);
	});

	it('backslash escapes', () => {
		expect(parseInlines('\\*not em\\* \\[x\\](y) \\\\ \\q')).toEqual([t('*not em* [x](y) \\ \\q')]);
	});

	it('links need a safe http(s) target', () => {
		expect(parseInlines('[a **b**](https://example.com/x?y=1)')).toEqual([
			{ t: 'link', href: 'https://example.com/x?y=1', children: [t('a '), { t: 'strong', children: [t('b')] }] }
		]);
		expect(parseInlines('[x](http://example.com)')).toMatchObject([{ t: 'link', href: 'http://example.com/' }]);
	});

	it('a link the sanitizer rejects shows its text', () => {
		for (const url of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', 'data:text/html,x', 'vbscript:x', '//evil.example', '/relative', 'file:///etc/passwd', 'ftp://x.example']) {
			expect(parseInlines(`[click](${url})`), url).toEqual([t('click')]);
		}
	});

	it('a link with whitespace, balance or a closing paren missing is not a link', () => {
		for (const src of ['[a](https://x.example/ b)', '[a](https://x.example', '[a] (https://x.example)', '[a]()', '[a](']) {
			// (An https:// inside it is still an autolink of its own.)
			expect(walkTexts(parseInlines(src)), src).toBe(src);
		}
		// Balanced parentheses belong to the target.
		expect(parseInlines('[w](https://en.example/Foo_(bar))')).toEqual([{ t: 'link', href: 'https://en.example/Foo_(bar)', children: [t('w')] }]);
		// An unsafe target with parentheses leaves exactly the text.
		expect(parseInlines('[x](javascript:alert(1)) after')).toEqual([t('x'), t(' after')]);
	});

	it('links do not nest and do not autolink inside link text', () => {
		const r = parseInlines('[a [b](https://one.example) c](https://two.example)');
		walk([para(...r)], (i) => {
			if (i.t === 'link') expect(i.children.some((c) => c.t === 'link')).toBe(false);
		});
		const inner = parseInlines('[see https://one.example](https://two.example)');
		expect(inner).toEqual([{ t: 'link', href: 'https://two.example/', children: [t('see https://one.example')] }]);
	});

	it('autolinks https:// text and leaves the sentence punctuation out', () => {
		expect(parseInlines('see https://example.com/a.')).toEqual([t('see '), { t: 'link', href: 'https://example.com/a', children: [t('https://example.com/a')] }, t('.')]);
		expect(parseInlines('(http://example.com/x)')).toEqual([t('('), { t: 'link', href: 'http://example.com/x', children: [t('http://example.com/x')] }, t(')')]);
		expect(parseInlines('nohttps://example.com')).toEqual([t('nohttps://example.com')]);
		expect(parseInlines('https://')).toEqual([t('https://')]);
	});

	it('[#123] is an item reference', () => {
		expect(parseInlines('see [#123] and [#4].')).toEqual([t('see '), { t: 'item', id: '123' }, t(' and '), { t: 'item', id: '4' }, t('.')]);
		for (const bad of ['[#]', '[# 1]', '[#1a]', '[#-1]', '[#1', '[ #1]']) expect(walkTexts(parseInlines(bad)), bad).toBe(bad);
	});

	it('raw HTML is text', () => {
		const src = '<script>alert(1)</script> <img src=x onerror=alert(1)> <a href="javascript:alert(1)">x</a>';
		const r = parseInlines(src);
		expect(walkTexts(r)).toBe(src);
		expect(r.every((n) => n.t === 'text')).toBe(true);
		// Markup inside emphasis stays text too.
		expect(parseInlines('**<b>x</b>**')).toEqual([{ t: 'strong', children: [t('<b>x</b>')] }]);
	});

	it('images are not a thing: ![alt](url) is text and a link at most', () => {
		const kinds = new Set<string>();
		walk(parseMarkdown('![alt](https://example.com/i.png)\n\n![x](javascript:alert(1))'), (i) => kinds.add(i.t));
		expect([...kinds].every((k) => ['text', 'link', 'br'].includes(k))).toBe(true);
		expect(walkTexts(parseInlines('![x](javascript:alert(1))'))).toBe('!x');
		expect(JSON.stringify(parseMarkdown('![x](javascript:alert(1))'))).not.toContain('javascript');
	});
});

/** The text a run of inlines shows, markers aside. */
function walkTexts(xs: Inline[]): string {
	return xs
		.map((x) => (x.t === 'text' ? x.text : x.t === 'code' ? x.text : x.t === 'item' ? `[#${x.id}]` : x.t === 'br' ? '\n' : x.t === 'strong' || x.t === 'em' || x.t === 'link' ? walkTexts(x.children) : ''))
		.join('');
}

describe('hostile input', () => {
	const time = (f: () => unknown) => {
		const t0 = performance.now();
		f();
		return performance.now() - t0;
	};
	const KiB64 = 64 * 1024;

	it('64 KiB of markers parses in linear time', () => {
		for (const ch of ['*', '`', '[', ']', '(', '_', '>', '-', '#', '\\', '<', '!']) {
			const ms = time(() => parseMarkdown(ch.repeat(KiB64)));
			expect(ms, `${ch} x 64 KiB`).toBeLessThan(1500);
		}
		for (const unit of ['*a ', '**a ', '[a](', '[a [', '](', '`a `', '[#1', '- ', '> ', '1. ', '* * ', 'https://a.example/', '[a](https://b ', '`` ` ']) {
			const ms = time(() => parseMarkdown(unit.repeat(Math.floor(KiB64 / unit.length))));
			expect(ms, `${JSON.stringify(unit)} repeated`).toBeLessThan(1500);
		}
		for (const unit of ['*a\n', '- a\n', '> a\n', '  - a\n', '# a\n', '```\n', '\n']) {
			const ms = time(() => parseMarkdown(unit.repeat(Math.floor(KiB64 / unit.length))));
			expect(ms, `${JSON.stringify(unit)} lines`).toBeLessThan(1500);
		}
	});

	it('long runs of spaces do not slow headings, rules or lists down', () => {
		const sp = ' '.repeat(KiB64);
		for (const src of ['# a' + sp + 'b', '#' + sp, '-' + sp + 'x', '- - ' + sp + 'x', sp + '- x', 'a' + sp + '#']) {
			expect(time(() => parseMarkdown(src)), src.slice(0, 8)).toBeLessThan(1000);
		}
	});

	it('deep nesting stops at the cap and never overflows the stack', () => {
		const quotes = '> '.repeat(5000) + 'x';
		const lists = Array.from({ length: 3000 }, (_, i) => ' '.repeat(2 * i % 7) + '- a').join('\n');
		const nested = Array.from({ length: 2000 }, (_, i) => ' '.repeat(i * 2) + '- a').join('\n');
		for (const src of [quotes, lists, nested, '*'.repeat(3) + '['.repeat(5000) + ']('.repeat(5000), '**a *b `c [d **e *f*'.repeat(1000)]) {
			let tree: Block[] = [];
			expect(() => (tree = parseMarkdown(src))).not.toThrow();
			expect(depthOf(tree)).toBeLessThanOrEqual(MAX_DEPTH);
		}
		// What is past the cap is text, not lost.
		const tree = parseMarkdown('> > > > > deep');
		expect(depthOf(tree)).toBe(MAX_DEPTH);
		const texts: string[] = [];
		walk(tree, (i) => i.t === 'text' && texts.push(i.text));
		expect(texts.join('')).toContain('deep');
	});

	it('inline spans nest to a depth, then are text', () => {
		let depth = 0;
		const deepest = (xs: Inline[], d: number) => {
			depth = Math.max(depth, d);
			for (const x of xs) if (x.t === 'strong' || x.t === 'em' || x.t === 'link') deepest(x.children, d + 1);
		};
		deepest(parseInlines('**a *b **c *d **e *f* e** d* c** b* a**'), 0);
		expect(depth).toBeLessThanOrEqual(3);
	});

	it('no tree contains a node kind other than the known ones, whatever the input', () => {
		const known = new Set(['text', 'br', 'strong', 'em', 'code', 'link', 'item']);
		const blocks = new Set(['heading', 'para', 'list', 'quote', 'code', 'hr']);
		const src = ['<script>', '[a](javascript:x)', '![i](https://x.example)', '<!-- c -->', '&lt;b&gt;', '[x]: https://y.example', '`<`', '|a|b|', '# <h1>', '> <q>'].join('\n');
		walk(parseMarkdown(src), (i) => expect(known.has(i.t)).toBe(true), (b) => expect(blocks.has(b.t)).toBe(true));
	});

	it('cuts a body past the limit instead of parsing it all', () => {
		const tree = parseMarkdown('a\n\n'.repeat(400_000));
		expect(tree.length).toBeLessThanOrEqual(MAX_DEPTH * 0 + 140_000);
	});
});
