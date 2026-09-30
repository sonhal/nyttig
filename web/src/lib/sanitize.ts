// Feed content is untrusted. Everything a feed controls goes through here
// before it reaches the DOM, and is only ever rendered as text (never with
// {@html}). nyttig-api applies the same link and color checks server-side.

const COLOR_RE = /^#[0-9A-Fa-f]{6}$/;

/** Returns link if it is an absolute http(s) URL, otherwise undefined. */
export function safeLink(link: string | undefined): string | undefined {
	if (!link) return undefined;
	let u: URL;
	try {
		u = new URL(link);
	} catch {
		return undefined;
	}
	if (u.protocol !== 'http:' && u.protocol !== 'https:') return undefined;
	if (!u.hostname) return undefined;
	return u.href;
}

/** Returns c if it is a #RRGGBB color, otherwise undefined. */
export function safeColor(c: string | undefined): string | undefined {
	return c && COLOR_RE.test(c) ? c : undefined;
}

/** Host of a link without a leading "www.", or "" when there is none. */
export function domainOf(link: string | undefined): string {
	const safe = safeLink(link);
	if (!safe) return '';
	return new URL(safe).hostname.replace(/^www\./, '');
}

/**
 * Collapses whitespace runs to one space and drops control characters, so
 * untrusted text fits on one line (the TUI's SanitizeLine).
 */
export function oneLine(s: string | undefined): string {
	if (!s) return '';
	return s.replace(/[\u0000-\u0008\u000B\u000E-\u001F\u007F-\u009F]/g, '').replace(/\s+/g, ' ').trim();
}

/** Extracts the text of an HTML fragment without running or loading anything. */
export type HTMLParser = (html: string) => string;

/** Elements whose content is not readable text. */
const NON_TEXT = 'script, style, noscript, template, iframe, object, svg, math';

function domText(html: string): string {
	// A <template>'s content is parsed into an inert fragment that is never
	// connected to a document: scripts don't run, images don't load and
	// style blocks are never processed. (DOMParser is inert too, but it
	// processes <style>, which makes CSP log a violation per description.)
	// Script and style bodies would still show up in textContent, so those
	// elements are dropped first.
	const t = document.createElement('template');
	t.innerHTML = html;
	for (const el of t.content.querySelectorAll(NON_TEXT)) el.remove();
	return t.content.textContent ?? '';
}

/** Converts a feed description (often HTML) to one line of plain text. */
export function htmlToText(html: string | undefined, parse: HTMLParser = domText): string {
	if (!html) return '';
	// Plain text needs no parsing (and must keep a literal "<" as typed).
	if (!/[<&]/.test(html)) return oneLine(html);
	return oneLine(parse(html));
}
