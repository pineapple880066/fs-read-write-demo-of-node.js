// 按固定窗口切分文本，并保留 overlap 重叠。
// 例：size=800, overlap=120 -> step=680
export function chunkText(text: string, size: number, overlap: number): string[] {
    // 防御式处理：保证后续都在字符串上操作
    const safeText = String(text || '');
    const chunks: string[] = [];
    if (!safeText) return chunks;

    // 步长至少为 1，避免 size <= overlap 时死循环
    const step = Math.max(1, size - overlap);

    // 以 step 向前移动，以 size 截断形成重叠块
    for (let i = 0; i < safeText.length; i += step) {
        const slice = safeText.slice(i, i + size);
        // 过滤纯空白 chunk
        if (slice.trim()) chunks.push(slice);
    }

    return chunks;
}
