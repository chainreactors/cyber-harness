// Checks the inline local links and fences used in our hand-written documentation.
// This is not a full Markdown parser or an external URL checker.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const walk = dir => fs.readdirSync(dir, { withFileTypes: true }).flatMap(entry => {
  const full = path.join(dir, entry.name);
  if (entry.isDirectory()) return entry.name === 'api' ? [] : walk(full);
  return entry.name.endsWith('.md') ? [full] : [];
});
const sources = ['README.md', 'README_CN.md'].map(p => path.join(root, p))
  .concat(walk(path.join(root, 'docs')));
const cache = new Map();
const failures = [];
const relative = file => path.relative(root, file).replaceAll(path.sep, '/');
function parse(file) {
  if (cache.has(file)) return cache.get(file);
  const lines = fs.readFileSync(file, 'utf8').split(/\r?\n/);
  const prose = [];
  const anchors = new Set();
  const seen = new Map();
  let fence = null;
  lines.forEach((line, index) => {
    const marker = line.match(/^\s{0,3}(\x60{3,}|~{3,})(.*)$/);
    if (marker) {
      if (!fence) fence = { char: marker[1][0], length: marker[1].length, line: index + 1 };
      else if (marker[1][0] === fence.char && marker[1].length >= fence.length && !marker[2].trim()) fence = null;
      return;
    }
    if (fence) return;
    prose.push([line, index + 1]);
    const heading = line.match(/^#{1,6}\s+(.+?)\s*#*$/);
    if (heading) {
      const label = heading[1].replace(/<[^>]*>/g, '').replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1');
      const slug = label.toLowerCase().replace(/[^\p{L}\p{M}\p{N}_\-\s]/gu, '').replace(/\s/g, '-');
      let unique = slug;
      let suffix = seen.get(slug) ?? 0;
      while (anchors.has(unique)) unique = slug + '-' + (++suffix);
      seen.set(slug, suffix);
      anchors.add(unique);
    }
    for (const match of line.matchAll(/<(?:a|h[1-6])\b[^>]*\b(?:id|name)=["']([^"']+)["']/gi)) anchors.add(match[1]);
  });
  const result = { prose, anchors, fence };
  cache.set(file, result);
  return result;
}
let checked = 0;
for (const file of sources) {
  const { prose, fence } = parse(file);
  if (fence) failures.push(relative(file) + ':' + fence.line + ': unclosed code fence');
  for (const [line, number] of prose) {
    // Targets may be angle-bracketed or contain a single balanced parenthesis pair.
    const links = line.matchAll(/!?\[[^\]]*\]\((<[^>]+>|(?:[^\s()]|\([^()]*\))+)(?:\s+"[^"]*")?\)/g);
    for (const match of links) {
      const href = match[1].replace(/^<|>$/g, '');
      if (/^(?:[a-z][a-z0-9+.-]*:|\/\/)/i.test(href)) continue;
      checked++;
      try {
        const [rawPath, rawAnchor] = href.split('#');
        const target = rawPath ? path.resolve(path.dirname(file), decodeURIComponent(rawPath)) : file;
        if (!fs.existsSync(target)) throw new Error('missing target ' + href);
        if (rawAnchor && /\.md$/i.test(target) && !parse(target).anchors.has(decodeURIComponent(rawAnchor))) {
          throw new Error('missing anchor ' + href);
        }
      } catch (error) {
        failures.push(relative(file) + ':' + number + ': ' + error.message);
      }
    }
  }
}
if (failures.length) {
  console.error(failures.join('\n'));
  process.exitCode = 1;
} else {
  console.log('Checked ' + checked + ' local links and code fences in ' + sources.length + ' documents.');
}
