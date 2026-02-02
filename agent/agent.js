import fs from 'fs';
import path from 'path';

// 模型 API 配置
const API_BASE = process.env.LLM_BASE_URL;
const API_KEY  = process.env.LLM_API_KEY;
const MODEL = process.env.LLM_MODEL || 'gpt-4o-mini';

// 最小扫描: (递归 + ignore + ext)
const IGNORE_DIRS = new Set(['node_modules', '.git', 'dist', 'build']);

function scanFile(rootDir, exts = ['.js', '.ts', '.tsx', '.json', 'md']) {
    const result = [];
    // walk只负责递归扫描目录
    function walk(current) {
        const entries = fs.readdirSync(current);

        for (const entry of entries) {
            if (IGNORE_DIRS.has(entry)) { continue; }

            const full = path.join(current, entry);
            const st = fs.statSync(full);
            if (st.isDirectory()) {
                walk(full);
            } else {
                const ext = path.extname(entry);
                if (ext && exts.includes(ext)) { result.push(full); }
            }
        }
    }

    if (!fs.existsSync(rootDir)) { throw new Error('path does not exit -> ' + rootDir); }
    if (!fs.statSync(rootDir).isDirectory()) { throw new Error('path is not a directory -> ' + rootDir); }
    
    walk(rootDir);
    result.sort();
    return result;
}

// 读取文本文件，限制最大字符数，防止过大文件读取(如果超过6000字符就截断)
function readTextSafe(filePath, maxChars = 6000) {
    const buf = fs.readFileSync(filePath);
    const text = buf.toString("utf-8");
    if (text.length <= maxChars) {
        return text;
    } else {
        return text.slice(0, maxChars) + '\n\n...<truncated>...';
    }
}

async function callLLM({ userTask, rootDir, files }) {
    if (!API_BASE || !API_KEY) {
        throw new Error('Missing env: LLM_BASE_URL and/or LLM_API_KEY');
    }

    // 先限制为只喂最大20个文件 // 之后再改为相关性挑选
    const MAX_FILES = 20;
    const picked = files.slice(0, MAX_FILES);

    const fileBlobs = picked.map((fp) => {
        const rel = path.relative(rootDir, fp);
        const content = readTextSafe(fp, 6000);
        return `--- FILE: ${rel} ---\n${content}\n`;
    }).join('\n');
    // 设置提示词
    const prompt = `
You are a coding assistant agent.
Goal: propose a plan and concrete code changes.
Constraints:
- Do NOT invent files that don't exist.
- If you suggest edits, specify exact file paths and exact changes.
- Prefer minimal changes.

Project root: ${rootDir}
Known files (sampled ${picked.length}/${files.length}):
${picked.map(fp => "- " + path.relative(rootDir, fp)).join("\n")}

User task:
${userTask}

File contents:
${fileBlobs}

Return:
1) A short plan
2) A list of edits: file path + what to change (prefer unified diff if possible)
`;    
    // 等待获取模型输出
    const res = await fetch(`${API_BASE}/chat/completions`, {
        method: 'post',
        headers: {
            'Authrization': `Bear${API_KEY}`,
            'Content-Type': 'application/json',
        },
        body: JSON.stringify({
            model: MODEL,
            message: [{
                role: 'user', content: prompt
            }],
            temperature: 0.2,
        }),
    });

    if (!res.ok) {
        const t = await res.text();
        throw new Error(`LLM HPPT ${res.status}: ${t}`);
    }

    const json = await res.json();
    const content = json?.choice?.[0]?.message?.content;

    if (!content) { throw new Error('No content in LLM response'); }
    return content;
}


