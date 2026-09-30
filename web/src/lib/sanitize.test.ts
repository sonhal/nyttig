import { describe, expect, it } from 'vitest';
import { domainOf, htmlToText, oneLine, safeColor, safeLink, type HTMLParser } from './sanitize';

describe('safeLink', () => {
	it.each([
		['https://example.com/a?b=1#c', 'https://example.com/a?b=1#c'],
		['http://example.com', 'http://example.com/'],
		['HTTPS://EXAMPLE.com/X', 'https://example.com/X']
	])('keeps %s', (link, want) => {
		expect(safeLink(link)).toBe(want);
	});

	it.each([
		'javascript:alert(1)',
		'JavaScript:alert(1)',
		' javascript:alert(1)',
		'java\tscript:alert(1)',
		'data:text/html,<script>alert(1)</script>',
		'vbscript:msgbox(1)',
		'file:///etc/passwd',
		'ftp://example.com',
		'//example.com/x',
		'/relative',
		'not a url',
		'',
		undefined
	])('drops %j', (link) => {
		expect(safeLink(link)).toBeUndefined();
	});
});

describe('safeColor', () => {
	it('accepts #RRGGBB only', () => {
		expect(safeColor('#FF6600')).toBe('#FF6600');
		expect(safeColor('#aBcDeF')).toBe('#aBcDeF');
		for (const c of ['#FFF', 'red', '#FF6600;background:url(x)', 'rgb(1,2,3)', '#GG0000', ' #FF6600', '', undefined]) {
			expect(safeColor(c)).toBeUndefined();
		}
	});
});

describe('domainOf', () => {
	it('shows the host without www', () => {
		expect(domainOf('https://www.kernel.org/x')).toBe('kernel.org');
		expect(domainOf('https://lwn.net/Articles/1')).toBe('lwn.net');
		expect(domainOf('javascript:alert(1)')).toBe('');
		expect(domainOf(undefined)).toBe('');
	});
});

describe('oneLine', () => {
	it('flattens whitespace and drops control characters', () => {
		expect(oneLine('  a\n\tb  c ')).toBe('a b c');
		expect(oneLine('red\u001b[31mtext\u0007\u009b')).toBe('red[31mtext');
		expect(oneLine(undefined)).toBe('');
	});
});

describe('htmlToText', () => {
	// A stand-in for DOMParser (the browser path runs in the e2e tests):
	// strips tags and decodes a few entities.
	const fakeParse: HTMLParser = (html) =>
		html
			.replace(/<script[\s\S]*?<\/script>/gi, '')
			.replace(/<[^>]*>/g, '')
			.replace(/&lt;/g, '<')
			.replace(/&gt;/g, '>')
			.replace(/&amp;/g, '&');

	it('returns plain text unchanged apart from whitespace', () => {
		let called = false;
		const parse: HTMLParser = (h) => {
			called = true;
			return fakeParse(h);
		};
		expect(htmlToText('plain  text\nhere', parse)).toBe('plain text here');
		expect(called).toBe(false);
	});

	it('parses markup to text', () => {
		expect(htmlToText('<p>Hello <b>world</b></p>\n<p>&lt;tag&gt; &amp; more</p>', fakeParse)).toBe(
			'Hello world <tag> & more'
		);
	});

	it('handles empty input', () => {
		expect(htmlToText('', fakeParse)).toBe('');
		expect(htmlToText(undefined, fakeParse)).toBe('');
	});
});
