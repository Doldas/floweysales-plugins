#!/usr/bin/env node
// Builds a FloweySales plugin store index (schema 1, see
// https://github.com/Doldas/floweysales/blob/main/docs/plugins.md).
//
//   node scripts/plugin-index.mjs local <dir>          index of release dirs built by build-plugins.sh (file:// URLs)
//   node scripts/plugin-index.mjs releases <owner/repo> index of GitHub releases tagged <id>-v<version>
//
// A release is a directory (or GitHub release) holding plugin.json, SHA256SUMS
// and binaries named floweysales-plugin-<id>-<os>-<arch>[.exe]. The index is
// written to stdout.
import { createHash } from 'node:crypto';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const TAG = /^([a-z][a-z0-9-]{1,39})-v(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$/;
const ASSET = /^floweysales-plugin-([a-z][a-z0-9-]{1,39})-([a-z0-9]+)-([a-z0-9]+)(?:\.exe)?$/;

/** Parses `sha256  name` lines. */
export function parseSums(text) {
  const sums = new Map();
  for (const line of text.split('\n')) {
    const m = /^([0-9a-f]{64})\s+\*?(\S+)\s*$/.exec(line.trim());
    if (m) sums.set(m[2], m[1]);
  }
  return sums;
}

/**
 * One index version from a release. assets maps file name → {url, size};
 * sums maps file name → sha256. Assets without a checksum are skipped.
 */
export function versionFromRelease({ manifest, assets, sums, releasedAt, notes }) {
  const artifacts = {};
  for (const [name, asset] of Object.entries(assets)) {
    const m = ASSET.exec(name);
    if (!m || m[1] !== manifest.id) continue;
    const sha256 = sums.get(name);
    if (!sha256) throw new Error(`${name}: no SHA-256 in SHA256SUMS`);
    artifacts[`${m[2]}-${m[3]}`] = { url: asset.url, sha256, size: asset.size };
  }
  if (!Object.keys(artifacts).length) throw new Error(`${manifest.id} ${manifest.version}: no binaries`);
  const version = { manifest, artifacts };
  if (releasedAt) version.released_at = releasedAt;
  if (notes) version.notes = notes.slice(0, 2000);
  return version;
}

/** Groups versions by plugin id, newest release first, in a stable order. */
export function buildIndex(versions) {
  const byId = new Map();
  for (const v of versions) {
    if (!byId.has(v.manifest.id)) byId.set(v.manifest.id, []);
    byId.get(v.manifest.id).push(v);
  }
  return {
    schema: 1,
    generated_at: new Date().toISOString(),
    plugins: [...byId.keys()].sort().map(id => ({
      id,
      versions: byId.get(id).sort((a, b) => (b.released_at ?? '').localeCompare(a.released_at ?? '')),
    })),
  };
}

function sha256File(path) {
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

function local(dir) {
  const root = resolve(dir);
  const versions = [];
  for (const entry of readdirSync(root).sort()) {
    const release = join(root, entry);
    if (!statSync(release).isDirectory()) continue;
    const manifest = JSON.parse(readFileSync(join(release, 'plugin.json'), 'utf8'));
    const assets = {};
    const sums = new Map();
    for (const name of readdirSync(release)) {
      if (!ASSET.test(name)) continue;
      const path = join(release, name);
      assets[name] = { url: pathToFileURL(path).href, size: statSync(path).size };
      sums.set(name, sha256File(path));
    }
    versions.push(versionFromRelease({ manifest, assets, sums, releasedAt: statSync(join(release, 'plugin.json')).mtime.toISOString() }));
  }
  return buildIndex(versions);
}

async function github(path, token) {
  const res = await fetch(`https://api.github.com${path}`, {
    headers: { Accept: 'application/vnd.github+json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
  });
  if (!res.ok) throw new Error(`GitHub ${path}: HTTP ${res.status}`);
  return res.json();
}

async function download(url) {
  const res = await fetch(url);
  if (!res.ok) throw new Error(`${url}: HTTP ${res.status}`);
  return res.text();
}

async function releases(repo) {
  if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repo)) throw new Error(`invalid repository ${repo}`);
  const token = process.env.GITHUB_TOKEN;
  const versions = [];
  for (let page = 1; page <= 20; page++) {
    const list = await github(`/repos/${repo}/releases?per_page=100&page=${page}`, token);
    for (const rel of list) {
      const tag = TAG.exec(rel.tag_name);
      if (!tag || rel.draft) continue;
      const files = Object.fromEntries(rel.assets.map(a => [a.name, a]));
      if (!files['plugin.json'] || !files.SHA256SUMS) {
        console.error(`skip ${rel.tag_name}: plugin.json or SHA256SUMS missing`);
        continue;
      }
      try {
        const manifest = JSON.parse(await download(files['plugin.json'].browser_download_url));
        if (manifest.id !== tag[1] || manifest.version !== tag[2]) {
          throw new Error(`plugin.json is ${manifest.id} ${manifest.version}`);
        }
        const sums = parseSums(await download(files.SHA256SUMS.browser_download_url));
        const assets = Object.fromEntries(rel.assets.map(a => [a.name, { url: a.browser_download_url, size: a.size }]));
        versions.push(versionFromRelease({ manifest, assets, sums, releasedAt: rel.published_at, notes: rel.body }));
      } catch (err) {
        console.error(`skip ${rel.tag_name}: ${err.message}`);
      }
    }
    if (list.length < 100) break;
  }
  return buildIndex(versions);
}

const isMain = import.meta.url === pathToFileURL(process.argv[1] ?? '').href;
if (isMain) {
  const [mode, arg] = process.argv.slice(2);
  try {
    const index = mode === 'local' && arg ? local(arg) : mode === 'releases' && arg ? await releases(arg) : null;
    if (!index) {
      console.error('usage: plugin-index.mjs local <dir> | releases <owner/repo>');
      process.exit(2);
    }
    process.stdout.write(JSON.stringify(index, null, 2) + '\n');
  } catch (err) {
    console.error(err.message);
    process.exit(1);
  }
}
