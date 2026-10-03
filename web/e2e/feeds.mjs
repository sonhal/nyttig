// A local RSS server for the end-to-end tests. Items can be added at
// runtime (POST /add?feed=alpha&title=...) to exercise live pushes, and it
// counts requests for the tracking pixel an item embeds, which the web app
// must never load.

import http from 'node:http';

export const FEED_PORT = Number(process.env.NYTTIG_E2E_FEED_PORT ?? 7811);
/** The proxy the browser talks to, like Caddy in production. */
export const BASE_PORT = Number(process.env.NYTTIG_E2E_BASE_PORT ?? 7812);
/** nyttig-web: the SvelteKit Node server ("node build"). */
export const WEB_PORT = BASE_PORT + 1;
/** nyttig-api. */
export const API_PORT = BASE_PORT + 2;
export const FEEDS_URL = `http://127.0.0.1:${FEED_PORT}`;
export const BASE_URL = `http://127.0.0.1:${BASE_PORT}`;

const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

function hoursAgo(h) {
	return new Date(Date.now() - h * 3600_000).toUTCString();
}

const feeds = {
	alpha: [
		{ guid: 'a1', title: 'Linux 6.18 released', link: 'https://www.kernel.org/linux-6.18', ago: 1, description: 'Linus announced the release of <b>Linux 6.18</b>.' },
		{ guid: 'a2', title: 'Rust for Linux status', link: 'https://lwn.net/Articles/rust', ago: 2, description: 'An overview of where Rust support stands.' },
		{
			guid: 'a3',
			title: 'Evil item',
			link: 'javascript:window.__pwned=1',
			ago: 3,
			// nyttigd strips tags and then unescapes entities, so the
			// escaped markup here reaches the browser as real HTML.
			description:
				'<b>bold</b> ' +
				esc(
					`<img src="http://127.0.0.1:${FEED_PORT}/pixel.png" onerror="window.__pwned=2">` +
						'<script>window.__pwned=3</script><style>body{display:none}</style>'
				) +
				'Tracked text'
		}
	],
	beta: [
		{ guid: 'b1', title: 'Go 1.26.8 security release', link: 'https://go.dev/blog/go1.26.8', ago: 4, description: 'Fixes GO-2026-6443 in net/http.' },
		{ guid: 'b2', title: 'Old post', link: 'https://beta.example/old', ago: 48, description: '' }
	]
};

function rss(name) {
	const items = feeds[name]
		.map(
			(it) => `<item>
<title>${esc(it.title)}</title>
<link>${esc(it.link)}</link>
<guid>${esc(it.guid)}</guid>
<pubDate>${it.pubDate ?? hoursAgo(it.ago)}</pubDate>
<description>${esc(it.description)}</description>
</item>`
		)
		.join('\n');
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>${name}</title><link>https://${name}.example</link><description>${name}</description>
${items}
</channel></rss>`;
}

// Big feeds for the tests that need a long list (follow mode, load older).
// GET /bulk/<name>.xml?n=230 serves that many items, the first one an hour
// old and each next one an hour older; the first request for a name fixes
// its size. Items pushed with POST /add?feed=bulk/<name> come first.
const bulk = new Map();

function bulkFeed(name, n) {
	let b = bulk.get(name);
	if (!b) bulk.set(name, (b = { n, pushed: [], base: Date.now() }));
	const items = [];
	for (let k = 1; k <= b.n; k++) {
		items.push({ guid: `${name}-${k}`, title: `${name} item ${k}`, pubDate: new Date(b.base - k * 3600_000).toUTCString() });
	}
	const all = [...b.pushed, ...items];
	const xml = all
		.map(
			(it) => `<item>
<title>${esc(it.title)}</title>
<link>https://${esc(name)}.example/${esc(it.guid)}</link>
<guid>${esc(it.guid)}</guid>
<pubDate>${it.pubDate}</pubDate>
<description>${esc(it.description ?? `Body of ${it.title}.`)}</description>
</item>`
		)
		.join('\n');
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>${esc(name)}</title><link>https://${esc(name)}.example</link><description>bulk</description>
${xml}
</channel></rss>`;
}

function extraFeed(name) {
	const items = [1, 2]
		.map(
			(n) => `<item>
<title>${esc(name)} story ${n}</title>
<link>https://${esc(name)}.example/${n}</link>
<guid>${esc(name)}-${n}</guid>
<pubDate>${hoursAgo(24 + n)}</pubDate>
<description>Item ${n} of the ${esc(name)} feed.</description>
</item>`
		)
		.join('\n');
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>${esc(name)}</title><link>https://${esc(name)}.example</link><description>extra</description>
${items}
</channel></rss>`;
}

// GET /dated/<name>.xml has four items with titles "<name> hour" (1.5 h old), "<name> 2
// days", "<name> 10 days" and "<name> 60 days", published that long ago, for
// the date window tests (since:7d shows the first two, since:1mo three).
function datedFeed(name) {
	const items = [
		// An hour and a half, so a one-hour window is empty however fast the test runs.
		['hour', 1.5],
		['2 days', 48],
		['10 days', 240],
		['60 days', 1440]
	]
		.map(
			([label, ago]) => `<item>
<title>${esc(name)} ${label}</title>
<link>https://${esc(name)}.example/${ago}</link>
<guid>${esc(name)}-${ago}</guid>
<pubDate>${hoursAgo(ago)}</pubDate>
<description>Published ${label} ago.</description>
</item>`
		)
		.join('\n');
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>${esc(name)}</title><link>https://${esc(name)}.example</link><description>dated</description>
${items}
</channel></rss>`;
}

// GET /undated/<name>.xml has "<name> cisco", dated an hour ago in Cisco
// PSIRT's zoneless "2006-01-02 15:04:05.0" shape, and "<name> unreadable",
// whose date the daemon can't parse, so it is shown with its fetch time.
function undatedFeed(name) {
	const cisco = new Date(Date.now() - 3600_000).toISOString().replace('T', ' ').replace(/\.\d+Z$/, '.0');
	const items = [
		['cisco', cisco],
		['unreadable', 'sometime last week']
	]
		.map(
			([label, date]) => `<item>
<title>${esc(name)} ${label}</title>
<link>https://${esc(name)}.example/${label}</link>
<guid>${esc(name)}-${label}</guid>
<pubDate>${date}</pubDate>
</item>`
		)
		.join('\n');
	return `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>${esc(name)}</title><link>https://${esc(name)}.example</link><description>undated</description>
${items}
</channel></rss>`;
}

export function startFeedServer() {
	let pixelHits = 0;
	let added = 0;
	// Pushed items get strictly increasing publication times (RSS has one
	// second of resolution), so items pushed back to back keep their order.
	let lastPub = 0;
	const server = http.createServer((req, res) => {
		const url = new URL(req.url ?? '/', FEEDS_URL);
		const m = url.pathname.match(/^\/(\w+)\.xml$/);
		if (m && feeds[m[1]]) {
			res.writeHead(200, { 'Content-Type': 'application/rss+xml' });
			res.end(rss(m[1]));
			return;
		}
		// Feeds for sources the management tests add: /extra/<name>.xml has
		// two items titled after the name. Any other path is a 404, which
		// shows up as the source's fetch error.
		const extra = url.pathname.match(/^\/extra\/([\w-]+)\.xml$/);
		if (extra) {
			res.writeHead(200, { 'Content-Type': 'application/rss+xml' });
			res.end(extraFeed(extra[1]));
			return;
		}
		const undated = url.pathname.match(/^\/undated\/([\w-]+)\.xml$/);
		if (undated) {
			res.writeHead(200, { 'Content-Type': 'application/rss+xml' });
			res.end(undatedFeed(undated[1]));
			return;
		}
		const dated = url.pathname.match(/^\/dated\/([\w-]+)\.xml$/);
		if (dated) {
			res.writeHead(200, { 'Content-Type': 'application/rss+xml' });
			res.end(datedFeed(dated[1]));
			return;
		}
		const big = url.pathname.match(/^\/bulk\/([\w-]+)\.xml$/);
		if (big) {
			res.writeHead(200, { 'Content-Type': 'application/rss+xml' });
			res.end(bulkFeed(big[1], Number(url.searchParams.get('n') ?? 50)));
			return;
		}
		if (url.pathname === '/pixel.png') {
			pixelHits++;
			res.writeHead(404);
			res.end();
			return;
		}
		if (url.pathname === '/pixel-hits') {
			res.writeHead(200, { 'Content-Type': 'application/json' });
			res.end(JSON.stringify({ hits: pixelHits }));
			return;
		}
		if (url.pathname === '/add' && req.method === 'POST') {
			const feed = url.searchParams.get('feed') ?? 'alpha';
			const title = url.searchParams.get('title') ?? `Live item ${added}`;
			added++;
			const pushTo = feed.startsWith('bulk/') ? bulk.get(feed.slice(5))?.pushed : feeds[feed];
			if (!pushTo) {
				res.writeHead(404);
				res.end();
				return;
			}
			lastPub = Math.max(Date.now(), lastPub + 1000);
			pushTo.unshift({
				guid: `live-${added}-${Date.now()}`,
				title,
				link: `https://${feed}.example/live/${added}`,
				pubDate: new Date(lastPub).toUTCString(),
				description: 'Pushed while the page was open.'
			});
			res.writeHead(204);
			res.end();
			return;
		}
		res.writeHead(404);
		res.end();
	});
	return new Promise((resolve) => server.listen(FEED_PORT, '127.0.0.1', () => resolve(server)));
}
