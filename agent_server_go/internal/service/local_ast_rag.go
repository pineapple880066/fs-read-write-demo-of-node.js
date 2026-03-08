package service

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agent_server_go/internal/retrieval"
)

const (
	localRAGMaxFiles     = 400
	localRAGMaxDocs      = 3200
	localRAGMaxFileBytes = 512 * 1024
)

var (
	localRAGSupportedExts = map[string]struct{}{
		".go":   {},
		".js":   {},
		".jsx":  {},
		".ts":   {},
		".tsx":  {},
		".py":   {},
		".json": {},
		".md":   {},
		".txt":  {},
		".sql":  {},
		".yaml": {},
		".yml":  {},
	}
	localRAGSkipDirs = map[string]struct{}{
		".git":         {},
		".hg":          {},
		".svn":         {},
		".idea":        {},
		".vscode":      {},
		"node_modules": {},
		"dist":         {},
		"build":        {},
		"coverage":     {},
		"vendor":       {},
		".next":        {},
	}
	errStopLocalWalk = errors.New("stop local rag walk")
)

func (s *Services) searchLocalWorkspaceWithAST(rootDir, query string, queryVariants []string, topK int) (SearchResponse, string, bool) {
	// searchLocalWorkspaceWithAST 扫描本地目录，并为 .go 文件额外生成 AST 摘要文档。
	// 这样结构类问题（函数、类型、方法关系）会比纯文本 chunk 更容易命中。
	root := s.resolveWorkspaceRoot(rootDir)
	docs, err := buildLocalRAGDocs(root)
	if err != nil || len(docs) == 0 {
		return SearchResponse{}, "", false
	}

	hits := retrieval.HybridSearchLocalDocs(docs, query, queryVariants, topK)
	if len(hits) == 0 {
		return SearchResponse{}, "", false
	}

	out := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHit{
			ChunkID:    fmt.Sprintf("%d", h.ID),
			RelPath:    h.RelPath,
			Score:      h.Score,
			BM25Score:  h.BM25Score,
			DenseScore: h.DenseScore,
		})
	}
	return SearchResponse{Hits: out}, retrieval.BuildContextFromHybridHits(hits, 8000), true
}

func buildLocalRAGDocs(rootDir string) ([]retrieval.HybridDoc, error) {
	rootAbs, err := filepath.Abs(strings.TrimSpace(rootDir))
	if err != nil {
		return nil, err
	}
	if stat, statErr := os.Stat(rootAbs); statErr != nil || !stat.IsDir() {
		return nil, fmt.Errorf("invalid root_dir: %s", rootAbs)
	}

	docs := make([]retrieval.HybridDoc, 0, 256)
	var nextID int64 = 1
	fileCount := 0

	walkErr := filepath.WalkDir(rootAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if _, skip := localRAGSkipDirs[strings.ToLower(d.Name())]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		if fileCount >= localRAGMaxFiles || len(docs) >= localRAGMaxDocs {
			return errStopLocalWalk
		}

		ext := strings.ToLower(filepath.Ext(d.Name()))
		if _, ok := localRAGSupportedExts[ext]; !ok {
			return nil
		}

		info, infoErr := d.Info()
		if infoErr == nil && info.Size() > localRAGMaxFileBytes {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil || looksBinary(body) {
			return nil
		}
		text := strings.TrimSpace(string(body))
		if text == "" {
			return nil
		}

		rel, relErr := filepath.Rel(rootAbs, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		fileCount++

		if ext == ".go" {
			if summary := buildGoASTSummary(rel, body); summary != "" {
				docs = append(docs, retrieval.HybridDoc{
					ID:      nextID,
					RelPath: rel,
					Text:    summary,
				})
				nextID++
			}
		}

		for _, chunk := range chunkTextForIngest(text, 1200, 150) {
			if len(docs) >= localRAGMaxDocs {
				return errStopLocalWalk
			}
			docs = append(docs, retrieval.HybridDoc{
				ID:      nextID,
				RelPath: rel,
				Text:    chunk,
			})
			nextID++
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errStopLocalWalk) {
		return nil, walkErr
	}
	return docs, nil
}

func looksBinary(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	sample := body
	if len(sample) > 2048 {
		sample = sample[:2048]
	}
	return bytes.IndexByte(sample, 0) >= 0
}

func buildGoASTSummary(relPath string, src []byte) string {
	// buildGoASTSummary 用 Go 标准库 parser/ast 提取结构摘要。
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, src, parser.ParseComments|parser.AllErrors)
	if file == nil {
		return ""
	}
	_ = err

	imports := make([]string, 0, len(file.Imports))
	types := make([]string, 0, 8)
	funcs := make([]string, 0, 12)
	methods := make([]string, 0, 12)
	vars := make([]string, 0, 8)
	consts := make([]string, 0, 8)

	for _, imp := range file.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if p != "" {
			imports = append(imports, p)
		}
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			sig := strings.TrimSpace(renderASTNode(fset, d.Type))
			sig = strings.TrimPrefix(sig, "func")
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recv := strings.TrimSpace(renderASTNode(fset, d.Recv.List[0].Type))
				methods = append(methods, fmt.Sprintf("(%s).%s%s", recv, d.Name.Name, sig))
			} else {
				funcs = append(funcs, fmt.Sprintf("%s%s", d.Name.Name, sig))
			}
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						types = append(types, fmt.Sprintf("%s %s", ts.Name.Name, classifyGoType(fset, ts.Type)))
					}
				}
			case token.VAR:
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						vars = append(vars, collectNames(vs.Names)...)
					}
				}
			case token.CONST:
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						consts = append(consts, collectNames(vs.Names)...)
					}
				}
			}
		}
	}

	sort.Strings(imports)
	sort.Strings(types)
	sort.Strings(funcs)
	sort.Strings(methods)
	sort.Strings(vars)
	sort.Strings(consts)

	var b strings.Builder
	b.WriteString("GO AST SUMMARY\n")
	b.WriteString("path: " + relPath + "\n")
	if file.Name != nil {
		b.WriteString("package: " + file.Name.Name + "\n")
	}
	writeSummaryList(&b, "imports", imports, 12)
	writeSummaryList(&b, "types", types, 18)
	writeSummaryList(&b, "functions", funcs, 20)
	writeSummaryList(&b, "methods", methods, 20)
	writeSummaryList(&b, "vars", vars, 12)
	writeSummaryList(&b, "consts", consts, 12)
	return strings.TrimSpace(b.String())
}

func renderASTNode(fset *token.FileSet, node any) string {
	var b bytes.Buffer
	if err := format.Node(&b, fset, node); err != nil {
		return ""
	}
	return b.String()
}

func collectNames(items []*ast.Ident) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item != nil && item.Name != "" {
			out = append(out, item.Name)
		}
	}
	return out
}

func classifyGoType(fset *token.FileSet, expr ast.Expr) string {
	switch expr.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	case *ast.FuncType:
		return "func-type"
	default:
		return strings.TrimSpace(renderASTNode(fset, expr))
	}
}

func writeSummaryList(b *strings.Builder, label string, items []string, limit int) {
	if len(items) == 0 {
		return
	}
	if limit <= 0 || len(items) < limit {
		limit = len(items)
	}
	b.WriteString(label + ":\n")
	for _, item := range items[:limit] {
		b.WriteString("- " + item + "\n")
	}
	if len(items) > limit {
		b.WriteString(fmt.Sprintf("- ... (%d more)\n", len(items)-limit))
	}
}
