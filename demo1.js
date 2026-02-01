import FileSystem from "fs";
import path from "path";

console.log("program start");

function scanDir(dir) {
    let result = [];

    // 检查路径是否存在
    if (!FileSystem.existsSync(dir)) {
        console.error("Error: path does not exist ->", dir);
        return;
    }

    const baseStat = FileSystem.statSync(dir);
    if (!baseStat.isDirectory()) {
        console.error("Error: path is not a directory ->", dir);
        return;
    }

    function walk(currentDir) {
        const entries = FileSystem.readdirSync(currentDir);

        for (const entry of entries) {
            const fullPath = path.join(currentDir, entry);
            const stat = FileSystem.statSync(fullPath);
            if (stat.isDirectory()) {
                walk(fullPath);
            }

            else if (entry.endsWith(".js")) {
                const relativePath = path.relative(dir, fullPath);
                result.push(relativePath);
            }
        }
    }

    walk(dir);

    FileSystem.writeFileSync("result.txt", result.join("\n"), "utf-8");
    console.log("result written to result.txt");
}

function main() {
    const args = process.argv;
    const dir = args[2];
    scanDir(dir);
}

main();