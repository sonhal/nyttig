import { describe, expect, it } from 'vitest';
import { versionLine } from './version';

describe('versionLine', () => {
	it('lists all three builds', () => {
		expect(versionLine({ api: 'v0.4.0', daemon: 'v0.4.1' }, 'v0.4.0')).toBe('web v0.4.0 · nyttig-api v0.4.0 · nyttigd v0.4.1');
	});

	it('says why the daemon has no version', () => {
		expect(versionLine({ api: 'dev', daemon: '', error: 'connection refused' }, 'dev')).toBe(
			'web dev · nyttig-api dev · nyttigd unreachable (connection refused)'
		);
		expect(versionLine({ api: 'dev', daemon: '' }, 'dev')).toBe('web dev · nyttig-api dev · nyttigd unreachable (no answer)');
	});

	it('keeps it on one line', () => {
		expect(versionLine({ api: 'v1\n', daemon: 'v1\u0007x' }, 'v1')).toBe('web v1 · nyttig-api v1 · nyttigd v1x');
	});
});
