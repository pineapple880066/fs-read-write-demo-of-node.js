import fs from 'node:fs';
import path from 'node:path';

import { CHUNK_SIZE, CHUNK_OVERLAP } from './config.js';
import { chunkText } from './chunk.js';
import type { ChunkRecord } from './types.js';

interface IndexProjectInput {
    rootDir: string;
    files: string[];
}

// 扫描到的文件 -> 按 chunk 切分 -> 带上 id / relPath 元信息
// 返回：[{ id, relPath, text }]
export function indexProject({ rootDir, files }: IndexProjectInput): ChunkRecord[] {
    const chunks: ChunkRecord[] = [];
    let id = 0;

    // 逐个文件处理，单文件异常不影响整体索引
    for (const fp of files) {
        // 统一用相对路径，便于展示/跨环境复用
        const relPath = path.relative(rootDir, fp);

        let text = '';
        try {
            // 同步读取：离线索引阶段可接受，逻辑更简单
            text = fs.readFileSync(fp, 'utf-8');
        } catch {
            // 文件不可读（权限、删除、编码等）时跳过
            continue;
        }

        const parts = chunkText(text, CHUNK_SIZE, CHUNK_OVERLAP);
        for (const p of parts) {
            chunks.push({ id: id++, relPath, text: p });
        }
    }

    return chunks;
}
