import type { AgentMode, RetrievedHit } from './types.js';

// 允许的 prompt 模式集合
const MODES = new Set<AgentMode>(['summary', 'code', 'chat']);

// prompt 里只需要 hits 的一小部分字段
type PromptHit = Pick<RetrievedHit, 'id' | 'relPath' | 'score'>;

interface PromptInput {
    mode: AgentMode | string;
    userTask: string;
    hits: PromptHit[];
    context: string;
}

interface PromptParts {
    userTask: string;
    fileList: string;
    hitList: string;
    context: string;
}

// 非法 mode 自动降级为 chat
function safeMode(mode: AgentMode | string): AgentMode {
    return MODES.has(mode as AgentMode) ? (mode as AgentMode) : 'chat';
}

// 把 chunk 命中列表格式化成多行字符串
function formatHitList(hits: PromptHit[]): string {
    if (!hits.length) return '(none)';
    return hits.map((h, i) => `${i + 1}. ${h.relPath}#${h.id} (score=${h.score})`).join('\n');
}

// 从 hits 提取去重后的文件列表
function formatFileList(hits: PromptHit[]): string {
    const uniq = [...new Set(hits.map(h => h.relPath))];
    if (!uniq.length) return '- (none)';
    return uniq.map(p => `- ${p}`).join('\n');
}

// context 为空时给占位，避免 prompt 结构断裂
function safeContext(context: string): string {
    return context && context.trim() ? context : '(no retrieved context)';
}

// 总结模式 prompt：输出 summary schema
function buildSummaryPrompt({ userTask, fileList, hitList, context }: PromptParts): string {
    return `
You are a software analyst. Summarize strictly based on retrieved chunks.
Do NOT propose refactors or code edits unless explicitly requested.

User task:
${userTask}

Retrieved files:
${fileList}

Retrieved chunks:
${hitList}

Context:
${context}

Return ONLY valid JSON (no markdown, no extra text) with this schema:
{
  "summary": "what this project does (5-10 sentences, Chinese preferred)",
  "key_files": ["most important files (relative paths)"],
  "entrypoints": ["likely entry files (relative paths) or empty array if unknown"]
}
`;
}

// 代码模式 prompt：输出 plan + diffs schema
function buildCodePrompt({ userTask, fileList, hitList, context }: PromptParts): string {
    return `
You are a coding assistant agent.
Goal: produce concrete code changes for the user task.

Hard constraints:
- Do NOT invent files that don't exist.
- Only modify files from the provided retrieved files.
- Return ONLY unified diffs for each file you change.
- Keep changes minimal and directly related to the user task.

User task:
${userTask}

Retrieved files:
${fileList}

Retrieved chunks:
${hitList}

Context:
${context}

Return ONLY valid JSON (no markdown, no extra text) with this schema:
{
  "plan": ["step1", "step2", "..."],
  "diffs": [
    {
      "path": "relative/path/to/file.js",
      "unified_diff": "diff --git a/... b/...\\n..."
    }
  ]
}
If no changes are needed, return:
{ "plan": ["no changes"], "diffs": [] }
`;
}

// 聊天模式 prompt：输出 answer/evidence/gaps/next_steps
function buildChatPrompt({ userTask, fileList, hitList, context }: PromptParts): string {
    return `
You are a pragmatic engineering mentor.
Answer the user's question based on retrieved context. Do not output code diffs.
If evidence is insufficient, clearly say what is missing.

User task:
${userTask}

Retrieved files:
${fileList}

Retrieved chunks:
${hitList}

Context:
${context}

Return ONLY valid JSON (no markdown, no extra text) with this schema:
{
  "answer": "direct answer in Chinese",
  "evidence_files": ["relative/path.js"],
  "gaps": ["what is unknown or uncertain"],
  "next_steps": ["practical actions user can take"]
}
`;
}

// 统一 prompt 入口：根据 mode 分发到不同模板
export function buildPrompt({ mode, userTask, hits, context }: PromptInput): string {
    const pickedMode = safeMode(mode);
    const hitList = formatHitList(hits || []);
    const fileList = formatFileList(hits || []);
    const packedContext = safeContext(context || '');

    if (pickedMode === 'summary') {
        return buildSummaryPrompt({ userTask, fileList, hitList, context: packedContext });
    }

    if (pickedMode === 'code') {
        return buildCodePrompt({ userTask, fileList, hitList, context: packedContext });
    }

    return buildChatPrompt({ userTask, fileList, hitList, context: packedContext });
}
