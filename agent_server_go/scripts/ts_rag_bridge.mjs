#!/usr/bin/env node

// TS RAG bridge:
// - Imports the user's existing compiled TS retrieval module (agent/dist/retrieve.js)
// - Scans files in target root
// - Returns hits + packed context as JSON for Go service to consume

import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const IGNORE_DIRS = new Set(['node_modules', '.git', 'dist', 'build']);
const DEFAULT_EXTS = ['.js', '.ts', '.tsx', '.json', '.md', '.txt', '.go', '.sql', '.yaml', '.yml'];

function readStdin() {
  return new Promise((resolve, reject) => {
    let buf = '';
    process.stdin.setEncoding('utf8');
    process.stdin.on('data', (chunk) => {
      buf += chunk;
    });
    process.stdin.on('end', () => resolve(buf));
    process.stdin.on('error', reject);
  });
}

function scanFiles(rootDir, exts = DEFAULT_EXTS) {
  const out = [];

  function walk(current) {
    const entries = fs.readdirSync(current, { withFileTypes: true });
    for (const entry of entries) {
      if (IGNORE_DIRS.has(entry.name)) continue;
      const full = path.join(current, entry.name);
      if (entry.isDirectory()) {
        walk(full);
        continue;
      }
      const ext = path.extname(entry.name).toLowerCase();
      if (exts.includes(ext)) out.push(full);
    }
  }

  if (!fs.existsSync(rootDir)) {
    throw new Error(`target root does not exist: ${rootDir}`);
  }
  if (!fs.statSync(rootDir).isDirectory()) {
    throw new Error(`target root is not a directory: ${rootDir}`);
  }

  walk(rootDir);
  out.sort();
  return out;
}

async function main() {
  const raw = await readStdin();
  const input = raw ? JSON.parse(raw) : {};

  const agentDistDir = String(input.agentDistDir || '').trim();
  const targetRootDir = String(input.targetRootDir || '').trim();
  const query = String(input.query || '').trim();
  const queryVariants = Array.isArray(input.queryVariants)
    ? input.queryVariants.map((x) => String(x || '').trim()).filter(Boolean)
    : [];
  const topK = Number.isFinite(input.topK) ? Number(input.topK) : 8;

  if (!agentDistDir) throw new Error('agentDistDir is required');
  if (!targetRootDir) throw new Error('targetRootDir is required');
  if (!query) throw new Error('query is required');

  const retrieveModUrl = pathToFileURL(path.join(agentDistDir, 'retrieve.js')).href;
  const retrieveMod = await import(retrieveModUrl);
  if (typeof retrieveMod.buildRagData !== 'function') {
    throw new Error('buildRagData not found in TS retrieve module');
  }

  const files = scanFiles(targetRootDir);
  const out = retrieveMod.buildRagData({
    rootDir: targetRootDir,
    files,
    query,
    queryVariants,
    topK,
  });

  process.stdout.write(JSON.stringify({
    hits: out?.hits || [],
    context: out?.context || '',
    file_count: files.length,
  }));
}

main().catch((err) => {
  const msg = err instanceof Error ? err.message : String(err);
  process.stderr.write(msg + '\n');
  process.exit(1);
});
