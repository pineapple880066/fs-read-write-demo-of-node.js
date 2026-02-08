import fs from 'fs';
import path from 'path';

import { CHUNK_SIZE, CHUNK_OVERLAP } from './config.js';
import { chunkText } from './chunk.js';


// 把项目文件切分成可检索的文本块（chunk）并附带基础元信息
// 返回结果形如：[{ id, relPath, text }]
export function indexProject({ rootDir, files }) {
    // chunks: 最终索引数据；id: 全局递增主键，保证每个 chunk 唯一
    const chunks = [];
    let id = 0;

    // 逐个处理输入文件，失败文件直接跳过，避免影响整体索引流程
    for (const fp of files) { // filePath
        // 用相对路径而不是绝对路径，便于后续展示和跨环境复用
        const relPath = path.relative(rootDir, fp);
        let text = '';
        try {
            // 同步读取：索引阶段通常是离线任务，逻辑简单可控
            text = fs.readFileSync(fp, 'utf-8');
        } catch {
            // 文件不可读（权限/编码/临时删除等）时跳过该文件
            continue;
        }

        // 按固定大小 + 重叠窗口切分文本，减少上下文断裂
        const parts = chunkText(text, CHUNK_SIZE, CHUNK_OVERLAP); // 调用chunkText

        // 为每个 chunk 记录：唯一 id、来源文件相对路径、chunk 正文
        for (const p of parts) {
            chunks.push({ id: id++, relPath, text: p });
        }
        // 每个chunk id 不一样相当于一个索引, 所以叫 indexer 索引器
    }

    return chunks;
}
