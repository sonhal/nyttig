// ":version": which builds are running. The web app's own version is stamped
// at build time from VITE_NYTTIG_VERSION (the image build sets it from its
// VERSION argument); nyttig-api reports its own and the daemon's.

import { oneLine } from './sanitize';

/** The web app's version, or "dev" for a build without one. */
export const WEB_VERSION: string = import.meta.env.VITE_NYTTIG_VERSION || 'dev';

/** GET /api/version. */
export interface Versions {
	api: string;
	/** "" when the daemon did not answer; error says why. */
	daemon: string;
	error?: string;
}

/** The one-line message ":version" shows. */
export function versionLine(v: Versions, web: string = WEB_VERSION): string {
	const daemon = v.daemon ? oneLine(v.daemon) : `unreachable (${oneLine(v.error) || 'no answer'})`;
	return `web ${oneLine(web)} · nyttig-api ${oneLine(v.api)} · nyttigd ${daemon}`;
}
