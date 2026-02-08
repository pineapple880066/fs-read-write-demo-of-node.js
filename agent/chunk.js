export function chunkText(text, size, overlap) {
    const safeText = String(text || ''); // 保证是字符串，防止为null
    const chunks = []; // 文本块数组 接收 文本块
    if (!safeText) return chunks; // 如果为空字符串，直接返回空数组

    const step = Math.max(1, size - overlap); // 步长为 大小 减去 重叠长度

    for (let i = 0; i < safeText.length; i += step){ // 按 step 前进， 按 size 切片（产生重叠）
        const slice = safeText.slice(i, i + size); // 长度为 size
        if (slice.trim()) chunks.push(slice); // 过滤空白块， 并且加入chunks列表
    }

    return chunks;
}