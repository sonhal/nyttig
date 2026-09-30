import adapter from '@sveltejs/adapter-node';

/** @type {import('@sveltejs/kit').Config} */
const config = {
	compilerOptions: {
		runes: true
	},
	kit: {
		// "pnpm build" writes a standalone Node server to build/; run it with
		// "node build" (see deploy/systemd/nyttig-web.service). Caddy sends
		// /api/* to nyttig-api and everything else here.
		adapter: adapter(),
		// SvelteKit sets this header on every page it renders, with a fresh
		// nonce for its own inline bootstrap script. Feed content is
		// untrusted, so nothing else may run, load or be framed.
		csp: {
			mode: 'nonce',
			directives: {
				'default-src': ['none'],
				'script-src': ['self'],
				'style-src': ['self'],
				'style-src-attr': ['unsafe-inline'],
				'connect-src': ['self'],
				'img-src': ['self'],
				'font-src': ['self'],
				'manifest-src': ['self'],
				'base-uri': ['none'],
				'form-action': ['self'],
				'frame-ancestors': ['none']
			}
		}
	}
};

export default config;
