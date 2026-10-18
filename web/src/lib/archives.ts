// Archives in the web app: the status badge of a feed row, when a copy can
// be downloaded, and the page's status filter.

import type { Archive, ArchiveInfo } from './types';

export const ARCHIVE_STATUSES = ['pending', 'running', 'done', 'partial', 'failed'] as const;

export interface ArchiveBadge {
	glyph: string;
	label: string;
}

/** The feed row's badge for an archive, or null when the item has none (or a status we do not know). */
export function archiveBadge(info: ArchiveInfo | undefined): ArchiveBadge | null {
	switch (info?.status) {
		case 'pending':
			return { glyph: '…', label: 'archive queued' };
		case 'running':
			return { glyph: '…', label: 'archiving' };
		case 'done':
			return { glyph: '⤓', label: 'archived' };
		case 'partial':
			return { glyph: '⤓~', label: 'archived, some images or styles missing' };
		case 'failed':
			return { glyph: '✗', label: 'archive failed' };
	}
	return null;
}

/** True while the daemon has yet to finish the archive. */
export function inFlight(status: string | undefined): boolean {
	return status === 'pending' || status === 'running';
}

/**
 * A copy can be downloaded when bytes are stored. That is also true after a
 * failed re-archive, which keeps the old copy.
 */
export function canDownload(a: Archive | undefined): boolean {
	return !!a && Number(a.size ?? 0) > 0;
}

/** The download URL; nyttig-api serves it as an attachment, never inline. */
export function fileHref(id: string | undefined): string {
	return '/api/archives/' + encodeURIComponent(id ?? '') + '/file';
}

/** The status filter after f: all, then each status, then all again. */
export function nextStatusFilter(cur: string): string {
	const order = ['', ...ARCHIVE_STATUSES];
	const i = order.indexOf(cur);
	return order[(i + 1) % order.length]!;
}

/** "1.2 MB" for a byte count given as protojson's string; "" for 0 or unknown. */
export function formatBytes(size: string | undefined): string {
	const n = Number(size ?? 0);
	if (!Number.isFinite(n) || n <= 0) return '';
	if (n < 1024) return n + ' B';
	if (n < 1024 * 1024) return (n / 1024).toFixed(n < 10 * 1024 ? 1 : 0) + ' kB';
	return (n / (1024 * 1024)).toFixed(n < 10 * 1024 * 1024 ? 1 : 0) + ' MB';
}
