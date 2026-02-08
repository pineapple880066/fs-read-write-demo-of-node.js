const EPS = 1e-6; // 10的-6次
function tokenize(text, stopWords) { // token化 （分成词）
    return String(text) // 转化为字符串
        .toLowerCase() // 转化为小写
        .split(/[^a-z0-9_\u4e00-\u9fa5]+/) // 不是英文小写字母， 数字， 下划线， 中文的字符就切开（例如空格，换行， 标点， 换行
        .filter(t => t && !stopWords.has(t) && t.length > 1); // 过滤掉 不为空 && 包含停止词 && 长度大于1的字符串
}

export function buildBm25Index(chunks, stopWords) { // 建立索引， 并且算出平均长度
    // chunks里面的每个元素c， 生成一个新对象 ， 保留id, relPath, text,然后再加一个c.text的分词结果(tokens)
    // 等价于(for)const c of chunks
    const docs = chunks.map(c => ({ 
        id: c.id,
        relPath: c.relPath,
        text: c.text,
        tokens: tokenize(c.text, stopWords), // 分成词（字符串）
    }));

    const df = new Map(); // document frequency 词频
    let totalLen = 0; // 总长度 用于统计 所有文档的平均长度

    for (const d of docs) { // 
        totalLen += d.tokens.length; // 计算总长度
        const uniq = new Set(d.tokens); // 去重， 保证一个文档里面同一个词只给 df 记一次

        for (const t of uniq) { // 遍历每个词
            df.set(t, (df.get(t) || 0) + 1); // 文件频次加一
        }
    }

    return { // 返回
        docs, // docs对象
        df, // 索引的哈希表
        avgLen: docs.length ? totalLen / docs.length : 0, // 平均长度
        N: docs.length, // 总数
    };
}

// 给每个文档计算一个 分数 然后取前 TOP_K 个
export function bm25Search(index, query, stopWords, topK = 8) {
    const k1 = 1.2; // 词频饱和度 (词频越大， 分数越高，但增长越来越缓慢，k1越大，增长越快)
    const b = 0.75; // 控制长度归一化， 文档越长，越容易产生查询词， 需要做惩罚，b = 0表示不惩罚长度， b = 1表示完全按长度惩罚

    const qTokens = tokenize(query, stopWords); // 把query 分成词

    const scores = index.docs.map(d => {
        const tf = new Map(); // 统计词出现的次数
        for (const t of d.tokens) {
            tf.set(t, (tf.get(t) || 0) + 1);
        }

        let score = 0;
        for (const t of qTokens) {
            const f = tf.get(t) || 0; // f: 出现几次
            if (!f) continue; // f = 0 没出现过直接跳过

            const df = index.df.get(t) || 0; // df : 在几个文档里面出现过
            //                       (总文档数  -  出现过的文档总数 + 0.5（平滑项防止除零，也不太影响结果） )    
            const idf = Math.log(1 + (index.N - df + 0.5) / (df + 0.5)); // idf: 逆文档概率(这个词出现过的文档数越少，越稀有)
            //           f是文档中出现次数       k1控制词频影响强度   b按长度惩罚
            const denom = f + k1 * (1 - b + b * (d.tokens.length / (index.avgLen || 1))); // 计算denom: 长度归一化
            score += idf * ((f * (k1 + 1)) / (denom + EPS)); // 累加分数到score (EPS只是防止分母为零)
        }

        return { doc: d, score };
    })

    return scores
        .sort((a, b) => b.score - a.score) // 按大小排序
        .slice(0, topK); // 只选前 topK 个
}