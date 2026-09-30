import type { Handle } from '@sveltejs/kit';

// Security headers for every page SvelteKit renders. The CSP itself comes
// from kit.csp in svelte.config.js. Static assets are served before this
// hook runs; the example Caddyfile sets these headers on everything too.
const headers: Record<string, string> = {
	'X-Content-Type-Options': 'nosniff',
	'Referrer-Policy': 'no-referrer',
	'Cross-Origin-Opener-Policy': 'same-origin',
	'Cross-Origin-Resource-Policy': 'same-origin',
	'X-Frame-Options': 'DENY',
	'Permissions-Policy': 'camera=(), microphone=(), geolocation=()',
	'Cache-Control': 'no-cache'
};

export const handle: Handle = async ({ event, resolve }) => {
	const response = await resolve(event);
	for (const [k, v] of Object.entries(headers)) {
		if (!response.headers.has(k)) response.headers.set(k, v);
	}
	return response;
};
