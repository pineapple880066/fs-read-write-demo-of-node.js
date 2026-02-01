import FileSystem from "fs";
import path from "path";

const IGNORE_DIRS = new Set([ // 忽略这些目录
    "node_modules",
    ".git",
    "dist",
    "build"
]);

console.log("program start");

function scanJsFiles(dir, ignoreDirs = IGNORE_DIRS) { // 
    let result = [];

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
            } else if (entry.endsWith(".js")) {
                const relativePath = path.relative(dir, fullPath);
                result.push(relativePath);
            }
        }
    }

    walk(dir);
    return result;
}

function writeResultToFile(lines, outFile = 'result.txt') {
    FileSystem.writeFileSync(outFile, lines.join("\n"), "utf-8");
}

function main() {
    const dir = process.argv[2];

    if (!dir) { // 如果没有提供目录参数（为空），打印用法说明并退出
        console.error('Usage: node demo1.js <dir>');
        process.exit(1);
    }

    try { // 扫描目录并写入结果文件
        const files = scanJsFiles(dir);
        writeResultToFile(files, 'result.txt');
        console.log('result written to result.txt');
    } catch (err) {
        console.log('Error:', err.message || err);
    }
}

main();