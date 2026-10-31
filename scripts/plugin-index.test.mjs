import assert from 'node:assert/strict';
import test from 'node:test';

import { buildIndex, parseSums, versionFromRelease } from './plugin-index.mjs';

const manifest = { protocol: 1, id: 'yahoo', name: 'Yahoo Finance', version: '1.1.0', provider: { id: 'YAHOO' } };
const sum = 'a'.repeat(64);

test('maps release binaries to platform artifacts pinned by checksum', () => {
  const v = versionFromRelease({
    manifest,
    assets: {
      'floweysales-plugin-yahoo-linux-amd64': { url: 'https://x/l', size: 10 },
      'floweysales-plugin-yahoo-windows-amd64.exe': { url: 'https://x/w', size: 11 },
      'plugin.json': { url: 'https://x/p', size: 1 },
      'floweysales-plugin-binance-linux-amd64': { url: 'https://x/other', size: 1 },
    },
    sums: parseSums(`${sum}  floweysales-plugin-yahoo-linux-amd64\n${sum} *floweysales-plugin-yahoo-windows-amd64.exe\n`),
    releasedAt: '2026-10-31T00:00:00Z',
  });
  assert.deepEqual(Object.keys(v.artifacts).sort(), ['linux-amd64', 'windows-amd64']);
  assert.equal(v.artifacts['windows-amd64'].url, 'https://x/w');
  assert.equal(v.artifacts['linux-amd64'].sha256, sum);
});

test('refuses a binary without a checksum', () => {
  assert.throws(() => versionFromRelease({
    manifest, assets: { 'floweysales-plugin-yahoo-linux-amd64': { url: 'https://x', size: 1 } }, sums: new Map(),
  }), /SHA-256/);
});

test('groups versions per plugin, newest first', () => {
  const v = (id, version, releasedAt) => ({ manifest: { id, version }, artifacts: {}, released_at: releasedAt });
  const index = buildIndex([v('yahoo', '1.0.0', '2026-01-01'), v('binance', '1.0.0', '2026-01-01'), v('yahoo', '1.1.0', '2026-02-01')]);
  assert.equal(index.schema, 1);
  assert.deepEqual(index.plugins.map(p => p.id), ['binance', 'yahoo']);
  assert.deepEqual(index.plugins[1].versions.map(x => x.manifest.version), ['1.1.0', '1.0.0']);
});
