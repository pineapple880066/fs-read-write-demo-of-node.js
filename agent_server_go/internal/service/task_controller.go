package service

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type lineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type taskPlan struct {
	Route           string
	Steps           []string
	TargetFiles     []string
	MandatoryRanges map[string][]lineRange
}

type fileInspectionState struct {
	FullRead bool
	Ranges   []lineRange
}

var (
	fileMentionRe = regexp.MustCompile(`[A-Za-z0-9_./\\-]+\.[A-Za-z0-9]+`)
)

func inferEffectiveMode(req ChatRequest) string {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	msg := strings.TrimSpace(req.Message)
	if mode == "edit" || mode == "code" || mode == "coding" {
		return "edit"
	}
	if looksLikeEditIntent(msg) {
		return "edit"
	}
	return "chat"
}

func looksLikeEditIntent(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if lower == "" {
		return false
	}
	keywords := []string{
		"加注释", "加上注释", "添加注释", "补注释", "写注释", "注释", "修改", "改一下", "重构", "修复", "fix", "refactor",
		"annotate", "comment", "edit", "update", "write", "改代码", "补上", "帮我加上",
	}
	for _, k := range keywords {
		if strings.Contains(lower, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func classifyTaskRoute(req ChatRequest, targetFiles []string) string {
	msg := strings.ToLower(strings.TrimSpace(req.Message))
	mode := inferEffectiveMode(req)
	if mode != "edit" {
		return "qa"
	}
	if len(targetFiles) > 0 {
		if strings.Contains(msg, "修复") || strings.Contains(msg, "fix") || strings.Contains(msg, "报错") || strings.Contains(msg, "测试") {
			return "fix_file"
		}
		return "edit_file"
	}
	if strings.Contains(msg, "修复") || strings.Contains(msg, "fix") || strings.Contains(msg, "测试") || strings.Contains(msg, "lint") {
		return "fix_and_verify"
	}
	return "edit_general"
}

func buildTaskPlan(req ChatRequest, rootDir string, evidence []string) taskPlan {
	targetFiles := detectTargetFiles(rootDir, req.Message, evidence)
	route := classifyTaskRoute(req, targetFiles)
	plan := taskPlan{
		Route:           route,
		TargetFiles:     targetFiles,
		MandatoryRanges: make(map[string][]lineRange),
	}

	switch route {
	case "qa":
		plan.Steps = []string{
			"检索相关代码与文档片段",
			"必要时继续读取关键文件",
			"基于证据给出中文回答",
		}
	case "edit_file":
		plan.Steps = []string{
			"完整读取目标文件",
			"如有需要读取相关依赖文件",
			"最小化修改目标文件",
			"执行语法或测试校验",
			"总结变更与校验结果",
		}
	case "fix_file", "fix_and_verify":
		plan.Steps = []string{
			"定位报错或目标文件",
			"完整读取关键文件与相关上下文",
			"修改代码并保留最小 diff",
			"运行校验命令，失败则继续修复",
			"输出最终结论和验证结果",
		}
	default:
		plan.Steps = []string{
			"定位目标代码",
			"读取关键文件",
			"进行最小修改",
			"执行校验",
			"总结结果",
		}
	}

	for _, rel := range plan.TargetFiles {
		absPath, _, err := safeWorkspacePath(rootDir, rel)
		if err != nil {
			continue
		}
		totalLines, err := countFileLines(absPath)
		if err != nil || totalLines <= 0 {
			continue
		}
		plan.MandatoryRanges[rel] = splitIntoLineRanges(totalLines, 220)
	}
	return plan
}

func detectTargetFiles(rootDir, message string, evidence []string) []string {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil
	}
	candidates := fileMentionRe.FindAllString(message, -1)
	if len(candidates) == 0 {
		return nil
	}

	out := make([]string, 0, 4)
	seen := map[string]struct{}{}
	for _, cand := range candidates {
		cand = filepath.ToSlash(strings.TrimSpace(cand))
		if cand == "" {
			continue
		}
		if rel := resolveMentionedFile(rootDir, cand, evidence); rel != "" {
			if _, ok := seen[rel]; ok {
				continue
			}
			seen[rel] = struct{}{}
			out = append(out, rel)
		}
		if len(out) >= 3 {
			break
		}
	}
	sort.Strings(out)
	return out
}

func resolveMentionedFile(rootDir, candidate string, evidence []string) string {
	if _, rel, err := safeWorkspacePath(rootDir, candidate); err == nil {
		return rel
	}
	base := filepath.Base(candidate)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	for _, ev := range evidence {
		if filepath.Base(ev) == base {
			return filepath.ToSlash(ev)
		}
	}
	matches := findWorkspaceFilesByBase(rootDir, base, 5)
	if len(matches) == 1 {
		return matches[0]
	}
	if len(matches) > 1 {
		for _, ev := range evidence {
			for _, m := range matches {
				if m == filepath.ToSlash(ev) {
					return m
				}
			}
		}
	}
	return ""
}

func findWorkspaceFilesByBase(rootDir, base string, limit int) []string {
	out := make([]string, 0, limit)
	_ = filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if _, skip := localRAGSkipDirs[strings.ToLower(d.Name())]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != base {
			return nil
		}
		rel, relErr := filepath.Rel(rootDir, path)
		if relErr == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		if len(out) >= limit {
			return errStopLocalWalk
		}
		return nil
	})
	return out
}

func countFileLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lines := 0
	for scanner.Scan() {
		lines++
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if lines == 0 {
		lines = 1
	}
	return lines, nil
}

func splitIntoLineRanges(totalLines, chunkSize int) []lineRange {
	if totalLines <= 0 {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = 200
	}
	out := make([]lineRange, 0, (totalLines/chunkSize)+1)
	for start := 1; start <= totalLines; start += chunkSize {
		end := start + chunkSize - 1
		if end > totalLines {
			end = totalLines
		}
		out = append(out, lineRange{Start: start, End: end})
	}
	return out
}

func planToPrompt(plan taskPlan) string {
	var b strings.Builder
	b.WriteString("Controller route: " + plan.Route + "\n")
	if len(plan.TargetFiles) > 0 {
		b.WriteString("Target files:\n")
		for _, f := range plan.TargetFiles {
			b.WriteString("- " + f + "\n")
		}
	}
	if len(plan.Steps) > 0 {
		b.WriteString("Execution plan:\n")
		for i, step := range plan.Steps {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
		}
	}
	if len(plan.MandatoryRanges) > 0 {
		b.WriteString("Mandatory inspection before write:\n")
		for path, ranges := range plan.MandatoryRanges {
			b.WriteString("- " + path + ": ")
			if len(ranges) == 1 {
				b.WriteString(fmt.Sprintf("read line %d-%d before write\n", ranges[0].Start, ranges[0].End))
				continue
			}
			b.WriteString(fmt.Sprintf("read all %d ranges before write\n", len(ranges)))
		}
	}
	return strings.TrimSpace(b.String())
}

func newInspectionState(plan taskPlan) map[string]*fileInspectionState {
	out := make(map[string]*fileInspectionState, len(plan.TargetFiles))
	for _, path := range plan.TargetFiles {
		out[path] = &fileInspectionState{}
	}
	return out
}

func markFileFullyRead(state map[string]*fileInspectionState, path string) {
	item := state[path]
	if item == nil {
		item = &fileInspectionState{}
		state[path] = item
	}
	item.FullRead = true
	item.Ranges = nil
}

func markFileRangeRead(state map[string]*fileInspectionState, path string, rng lineRange) {
	item := state[path]
	if item == nil {
		item = &fileInspectionState{}
		state[path] = item
	}
	if item.FullRead {
		return
	}
	item.Ranges = append(item.Ranges, rng)
	item.Ranges = mergeLineRanges(item.Ranges)
}

func mergeLineRanges(ranges []lineRange) []lineRange {
	if len(ranges) <= 1 {
		return ranges
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	out := make([]lineRange, 0, len(ranges))
	cur := ranges[0]
	for _, r := range ranges[1:] {
		if r.Start <= cur.End+1 {
			if r.End > cur.End {
				cur.End = r.End
			}
			continue
		}
		out = append(out, cur)
		cur = r
	}
	out = append(out, cur)
	return out
}

func hasReadCoverage(state map[string]*fileInspectionState, path string, required []lineRange) bool {
	item := state[path]
	if item == nil {
		return false
	}
	if item.FullRead {
		return true
	}
	covered := mergeLineRanges(item.Ranges)
	for _, need := range required {
		ok := false
		for _, have := range covered {
			if have.Start <= need.Start && have.End >= need.End {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func missingLineRanges(state map[string]*fileInspectionState, path string, required []lineRange) []lineRange {
	item := state[path]
	if item == nil {
		return required
	}
	if item.FullRead {
		return nil
	}
	have := mergeLineRanges(item.Ranges)
	out := make([]lineRange, 0, len(required))
	for _, need := range required {
		covered := false
		for _, got := range have {
			if got.Start <= need.Start && got.End >= need.End {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, need)
		}
	}
	return out
}
