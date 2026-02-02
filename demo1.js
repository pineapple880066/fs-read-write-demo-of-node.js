// 导入 fs 和 path 模块
import FileSystem from "fs";
import path from "path";

const IGNORE_DIRS = new Set([ // 忽略这些目录
    "node_modules",
    ".git",
    "dist",
    "build"
]);

console.log("program start"); // 提示开始运行

function scanJsFiles(dir, options = {}) { // 只负责扫描目录，返回.js文件相对路径数组
    let result = [];

    const ignoreDirs = options.ignoreDirs || IGNORE_DIRS; // 忽略目录集合 默认IGNORE_DIRS

    let rawExts;
    if (options.exts) {
        rawExts = options.exts;
    } else {
        rawExts = ['.js'];
    }
    let exts = [];

    for (const e of rawExts) { // 规范化扩展名
        if (e.startsWith('.')) { // 如果是.开头，直接放入
            exts.push(e);
        } else {
            exts.push('.' + e); // 如果不是.开头，加上.再放进去
        }
    }

    // 检查路径是否存在
    if (!FileSystem.existsSync(dir)) {
        throw new Error('path does not exit -> ' + dir);
    }
    // 检查路径是否是目录
    const baseStat = FileSystem.statSync(dir);
    if (!baseStat.isDirectory()) {
        throw new Error('path is not a directory ->' + dir);
    }

    function walk(currentDir) {
        const entries = FileSystem.readdirSync(currentDir);

        for (const entry of entries) { // 遍历目录项
            if (ignoreDirs && ignoreDirs.has(entry)) {
                continue; // 忽略指定目录
            }

            const fullPath = path.join(currentDir, entry);
            const stat = FileSystem.statSync(fullPath);

            if (stat.isDirectory()) {
                walk(fullPath);
            } else if (exts.includes(path.extname(entry))) {
                const relativePath = path.relative(dir, fullPath);
                result.push(relativePath);
            }
        }
    }

    walk(dir);
    return result;
}

function writeResultToFile(lines, outFile = 'result.txt') { // 把结果写入文件
    FileSystem.writeFileSync(outFile, lines.join("\n"), "utf-8");
}

function main() {
    // 用法：node demo1.js <dir> [--out result] [--ext js, ts ,tsx] 后面方括号里的是可选参数
    // [--out result] 指定输出文件名，默认result.txt
    // [--ext js,ts,tsx] 指定要扫描的文件扩展名，默认js 或者.js
    const args = process.argv.slice(2);

    if (args.length === 0) { // 如果没有提供目录参数（为空），打印用法说明并退出
        console.error('Usage: node demo1.js <dir> [--out result.txt] [--ext js,ts,tsx]');
        process.exit(1);
    }

    const dir = args[0]; // 第一个参数是目录

    // 简单参数解析
    let outFile = 'result.txt'; // 默认输出文件
    let exts = ['.js']; // 默认扩展名

    for (let i = 1; i < args.length; i++){
        const a = args[i];
        if (a === '--out' && i + 1 < args.length) {
            outFile = args[i + 1]; // 下一个参数是输出文件名
            i++;
        } else if (a === '--ext' && i + 1 < args.length) {
            const raw = args[i + 1];
            // 解析扩展名列表
            exts = raw
                .split(',') // 按,分割
                .map(function (s) { return s.trim(); }) // 去掉空格
                .filter(Boolean); // 把空字符串‘’过滤掉
            i++;
        } else {
            console.error('Unknown or incomplete option:', a);
            console.error('Usage: node demo1.js <dir> [--out example: result.txt] [--ext example: js, ts, tsx]');
            process.exit(1); // 正常退出程序
        }
    }
    try {
        // 扫描目录并写入结果文件
        const files = scanJsFiles(dir, { exts }); // 收到的目录数组 或者还有 指定拓展名的数组
        writeResultToFile(files, outFile); // 写入指定的outFile
          // 写入了XXX.txt , XX数量个 file
        console.log('result written to ' + outFile + ' ' + files.length + ' files');
    } catch (err) {
        console.log('Error: ', (err && err.message) ? err.message : String(err));
        process.exit(1);
    }
}

main();
