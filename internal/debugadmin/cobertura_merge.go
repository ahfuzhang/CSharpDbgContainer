package debugadmin

// ReportGenerator 5.5.11 对泛型父类与编译器生成类（async 状态机 <X>d__N、
// lambda 缓存 <>c、闭包 <>c__DisplayClass）的类名处理不对称：泛型父类的 Name
// 保留泛型参数，而这些生成类的 Name 被剥掉泛型，导致二者被识别为不同的类分组，
// 却又共享同一个 DisplayName / 输出 HTML 文件名，形成并发写入的竞态——
// async 方法体在报表里时而全灰（不可覆盖）、时而随机消失。
//
// 这里的做法是在 dotnet-coverage 产出 cobertura xml 之后、喂给 reportgenerator
// 之前，把这些编译器生成类的行数据合并进它们的父类，并把状态机的行合成为一个
// 以原始异步方法命名的 <method>，从而让 reportgenerator 按父类正常渲染。
// 诊断细节见 internal-repo 仓库
// AI_test/prompt/2026-08-17/overview_20260817_1932.md。

import (
	"encoding/xml"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ahfuzhang/CSharpDbgContainer/internal/pdb"
)

type coberturaDoc struct {
	XMLName      xml.Name          `xml:"coverage"`
	LineRate     string            `xml:"line-rate,attr"`
	BranchRate   string            `xml:"branch-rate,attr"`
	Complexity   string            `xml:"complexity,attr"`
	Version      string            `xml:"version,attr"`
	Timestamp    string            `xml:"timestamp,attr"`
	LinesCovered string            `xml:"lines-covered,attr"`
	LinesValid   string            `xml:"lines-valid,attr"`
	Sources      *coberturaSources `xml:"sources"`
	Packages     coberturaPackages `xml:"packages"`
}

type coberturaSources struct {
	Source []string `xml:"source"`
}

type coberturaPackages struct {
	Package []*coberturaPackage `xml:"package"`
}

type coberturaPackage struct {
	Name       string           `xml:"name,attr"`
	LineRate   string           `xml:"line-rate,attr"`
	BranchRate string           `xml:"branch-rate,attr"`
	Complexity string           `xml:"complexity,attr"`
	Classes    coberturaClasses `xml:"classes"`
}

type coberturaClasses struct {
	Class []*coberturaClass `xml:"class"`
}

type coberturaClass struct {
	Name       string            `xml:"name,attr"`
	Filename   string            `xml:"filename,attr"`
	LineRate   string            `xml:"line-rate,attr"`
	BranchRate string            `xml:"branch-rate,attr"`
	Complexity string            `xml:"complexity,attr"`
	Methods    *coberturaMethods `xml:"methods"`
	Lines      *coberturaLines   `xml:"lines"`
}

type coberturaMethods struct {
	Method []*coberturaMethod `xml:"method"`
}

type coberturaMethod struct {
	Name       string          `xml:"name,attr"`
	Signature  string          `xml:"signature,attr"`
	LineRate   string          `xml:"line-rate,attr"`
	BranchRate string          `xml:"branch-rate,attr"`
	Complexity string          `xml:"complexity,attr"`
	Lines      *coberturaLines `xml:"lines"`
}

type coberturaLines struct {
	Line []*coberturaLine `xml:"line"`
}

type coberturaLine struct {
	Number string `xml:"number,attr"`
	Hits   string `xml:"hits,attr"`
	Branch string `xml:"branch,attr"`
}

var (
	// stateMachineClassPattern 匹配 async/iterator 状态机类名，例如
	// "Flow.<TriggerAsync>d__5<TRequest, TResponse>"，捕获父类名与原始方法名。
	stateMachineClassPattern = regexp.MustCompile(`^(.+?)\.<([^>]+)>d__\d+(<[^>]+>)?$`)
	// lambdaCacheClassPattern 匹配 lambda 缓存类名，例如 "Foo.<>c"。
	lambdaCacheClassPattern = regexp.MustCompile(`^(.+?)\.<>c(<[^>]+>)?$`)
	// closureClassPattern 匹配闭包类名，例如 "Foo.<>c__DisplayClass1_0"。
	closureClassPattern = regexp.MustCompile(`^(.+?)\.<>c__DisplayClass\d+_\d+(<[^>]+>)?$`)
)

// parseGeneratedClassName 判断 name 是否为编译器生成的类名，是则返回其所属的
// 父类全名；如果是 async 状态机，还会返回原始的异步方法名（method != ""）。
func parseGeneratedClassName(name string) (parent string, method string, ok bool) {
	if m := stateMachineClassPattern.FindStringSubmatch(name); m != nil {
		return m[1] + m[3], m[2], true
	}
	if m := lambdaCacheClassPattern.FindStringSubmatch(name); m != nil {
		return m[1] + m[2], "", true
	}
	if m := closureClassPattern.FindStringSubmatch(name); m != nil {
		return m[1] + m[2], "", true
	}
	return "", "", false
}

type coberturaClassKey struct {
	filename string
	name     string
}

func coberturaLinesToMap(l *coberturaLines) map[int]int {
	res := make(map[int]int)
	if l == nil {
		return res
	}
	for _, ln := range l.Line {
		n, errN := strconv.Atoi(ln.Number)
		h, errH := strconv.Atoi(ln.Hits)
		if errN != nil || errH != nil {
			continue
		}
		if cur, exists := res[n]; !exists || h > cur {
			res[n] = h
		}
	}
	return res
}

func coberturaMapToLines(m map[int]int) *coberturaLines {
	nums := make([]int, 0, len(m))
	for n := range m {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	lines := make([]*coberturaLine, 0, len(nums))
	for _, n := range nums {
		lines = append(lines, &coberturaLine{
			Number: strconv.Itoa(n),
			Hits:   strconv.Itoa(m[n]),
			Branch: "False",
		})
	}
	return &coberturaLines{Line: lines}
}

// mergeGeneratedClassLines 把生成类的行覆盖数据（取 hits 较大值）合并进父类的行。
func mergeGeneratedClassLines(parent, generated *coberturaClass) {
	merged := coberturaLinesToMap(parent.Lines)
	for n, h := range coberturaLinesToMap(generated.Lines) {
		if h > merged[n] {
			merged[n] = h
		}
	}
	parent.Lines = coberturaMapToLines(merged)
}

// addMergedAsyncMethod 把状态机 MoveNext 里的行数据合成一个以原始异步方法命名的
// <method>，加入父类的方法表，使 reportgenerator 能在方法列表里展示它。
func addMergedAsyncMethod(parent *coberturaClass, methodName string, generated *coberturaClass) {
	smLines := make(map[int]int)
	if generated.Methods != nil {
		for _, m := range generated.Methods.Method {
			for n, h := range coberturaLinesToMap(m.Lines) {
				if h > smLines[n] {
					smLines[n] = h
				}
			}
		}
	}
	if len(smLines) == 0 {
		return
	}
	covered := 0
	for _, h := range smLines {
		if h > 0 {
			covered++
		}
	}
	lineRate := float64(covered) / float64(len(smLines))

	if parent.Methods == nil {
		parent.Methods = &coberturaMethods{}
	}
	parent.Methods.Method = append(parent.Methods.Method, &coberturaMethod{
		Name:       methodName,
		Signature:  "()",
		LineRate:   strconv.FormatFloat(lineRate, 'g', -1, 64),
		BranchRate: "1",
		Complexity: "1",
		Lines:      coberturaMapToLines(smLines),
	})
}

func recomputeClassLineRate(cls *coberturaClass) {
	m := coberturaLinesToMap(cls.Lines)
	if len(m) == 0 {
		return
	}
	covered := 0
	for _, h := range m {
		if h > 0 {
			covered++
		}
	}
	cls.LineRate = strconv.FormatFloat(float64(covered)/float64(len(m)), 'g', -1, 64)
}

// isCoveragePackageExcluded 判断 package 的 name 是否命中 GlobalOptions.CoverageOpts.ExcludeRegexpsForCoverage
// 中的任意一个正则表达式，命中则说明该 package 需要从覆盖率报表中排除。
func isCoveragePackageExcluded(name string) bool {
	if GlobalOptions == nil {
		return false
	}
	for i := range GlobalOptions.CoverageOpts.ExcludeRegexpsForCoverage {
		if GlobalOptions.CoverageOpts.ExcludeRegexpsForCoverage[i].MatchString(name) {
			return true
		}
	}
	return false
}

// mergeCompilerGeneratedClassesIntoParents 解析 cobertura xml，把编译器生成的类
// （async 状态机、lambda 缓存、闭包）合并进各自的父类，并从输出中移除这些生成类，
// 使每个源文件只保留“真实”的类，避免 reportgenerator 因泛型父类与生成类
// DisplayName 相同而产生的渲染竞态。
// 处理前会先按 GlobalOptions.CoverageOpts.ExcludeRegexpsForCoverage 过滤掉匹配的 package，
// 使其不出现在最终的 html report 中。
// 当 GlobalOptions.CoverageOpts.SourceFromPDB 打开时，还会为 filename 在本地磁盘上不
// 存在的 class，尝试从 pid 对应目标进程工作目录下的 .pdb 文件里恢复源码（见
// fixMissingSourceFilenamesFromPDB），使 reportgenerator 生成的报表里也能展示源码。
func mergeCompilerGeneratedClassesIntoParents(data []byte, pid int) ([]byte, error) {
	var doc coberturaDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse cobertura xml: %w", err)
	}

	remainingPackages := doc.Packages.Package[:0]
	for _, pkg := range doc.Packages.Package {
		if isCoveragePackageExcluded(pkg.Name) {
			continue
		}
		remainingPackages = append(remainingPackages, pkg)
	}
	doc.Packages.Package = remainingPackages

	mainClasses := make(map[coberturaClassKey]*coberturaClass)
	for _, pkg := range doc.Packages.Package {
		for _, cls := range pkg.Classes.Class {
			if _, _, ok := parseGeneratedClassName(cls.Name); !ok {
				mainClasses[coberturaClassKey{cls.Filename, cls.Name}] = cls
			}
		}
	}

	modified := make(map[*coberturaClass]bool)
	for _, pkg := range doc.Packages.Package {
		remaining := pkg.Classes.Class[:0]
		for _, cls := range pkg.Classes.Class {
			parentName, methodName, ok := parseGeneratedClassName(cls.Name)
			if !ok {
				remaining = append(remaining, cls)
				continue
			}
			parent, found := mainClasses[coberturaClassKey{cls.Filename, parentName}]
			if !found {
				remaining = append(remaining, cls)
				continue
			}
			mergeGeneratedClassLines(parent, cls)
			if methodName != "" {
				addMergedAsyncMethod(parent, methodName, cls)
			}
			modified[parent] = true
		}
		pkg.Classes.Class = remaining
	}

	for cls := range modified {
		recomputeClassLineRate(cls)
	}

	if GlobalOptions != nil && GlobalOptions.CoverageOpts.SourceFromPDB {
		fixMissingSourceFilenamesFromPDB(doc.Packages.Package, pid)
	}

	out, err := xml.MarshalIndent(&doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal cobertura xml: %w", err)
	}
	return append([]byte(xml.Header), out...), nil
}

// cleanCoberturaCompilerGeneratedClasses 读取 coberturaFile，合并其中的编译器
// 生成类后原地写回，供 reportgenerator 生成正确反映覆盖率的 HTML 报表。
// pid 是目标进程的 pid，仅在 GlobalOptions.CoverageOpts.SourceFromPDB 打开时才会用到，
// 用于定位目标进程的工作目录以搜索 .pdb 文件。
func cleanCoberturaCompilerGeneratedClasses(coberturaFile string, pid int) error {
	data, err := os.ReadFile(coberturaFile)
	if err != nil {
		return fmt.Errorf("read cobertura xml: %w", err)
	}
	cleaned, err := mergeCompilerGeneratedClassesIntoParents(data, pid)
	if err != nil {
		return err
	}
	return os.WriteFile(coberturaFile, cleaned, 0o644)
}

// fixMissingSourceFilenamesFromPDB 为 packages 中 filename 在本地磁盘上不存在的 class，
// 尝试从 pid 对应目标进程工作目录（/proc/<pid>/cwd）下的 .pdb 文件里恢复源码：
//  1. 在目标进程的工作目录下递归搜索所有 .pdb 文件；
//  2. 用 internal/pdb 把每个 pdb 内嵌的 .cs 源文件 dump 到 /tmp 下的一个临时目录，
//     建立「文件名(不含路径) -> 完整路径集合」的索引（用集合是因为不同 pdb 可能
//     包含同名但内容不同的源文件，需要避免互相覆盖或被当成同一个文件）；
//  3. 对每个 filename 缺失的 class，按文件名在索引里查找：只有一个候选就直接采用；
//     有多个候选时，与原始 filename 从右向左逐段比较路径，取匹配段数最长的一个。
//
// 找不到目标进程工作目录、目录下没有 pdb、或 pdb 里没有匹配的源文件时，保持 filename 不变。
func fixMissingSourceFilenamesFromPDB(packages []*coberturaPackage, pid int) {
	missing := collectMissingFilenames(packages)
	if len(missing) == 0 {
		return
	}
	cwd := readProcessCwd(pid)
	if cwd == "" {
		return
	}
	pdbFiles, err := findPDBFiles(cwd)
	if err != nil || len(pdbFiles) == 0 {
		return
	}
	index := buildCSFileIndexFromPDBs(pdbFiles)
	if len(index) == 0 {
		return
	}
	for _, pkg := range packages {
		for _, cls := range pkg.Classes.Class {
			if _, ok := missing[cls.Filename]; !ok {
				continue
			}
			candidates := index[filepath.Base(cls.Filename)]
			if len(candidates) == 0 {
				continue
			}
			cls.Filename = pickBestFilenameMatch(cls.Filename, candidates)
		}
	}
}

// collectMissingFilenames 返回 packages 中所有本地磁盘上不存在对应文件的 filename 集合。
func collectMissingFilenames(packages []*coberturaPackage) map[string]struct{} {
	missing := make(map[string]struct{})
	for _, pkg := range packages {
		for _, cls := range pkg.Classes.Class {
			if cls.Filename == "" {
				continue
			}
			if _, ok := missing[cls.Filename]; ok {
				continue
			}
			if _, err := os.Stat(cls.Filename); err != nil {
				missing[cls.Filename] = struct{}{}
			}
		}
	}
	return missing
}

// findPDBFiles 递归搜索 dir 下所有 .pdb 文件，忽略单个条目的错误（如权限问题）以便
// 继续搜索其余部分。
func findPDBFiles(dir string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".pdb") {
			result = append(result, path)
		}
		return nil
	})
	return result, err
}

// buildCSFileIndexFromPDBs 把每个 pdb 文件内嵌的 .cs 源码 dump 到 /tmp 下的独立临时目录，
// 并建立「文件名(不含路径) -> 完整路径集合」的索引。单个 pdb 提取失败不影响其余 pdb 的处理。
func buildCSFileIndexFromPDBs(pdbFiles []string) map[string]map[string]struct{} {
	index := make(map[string]map[string]struct{})
	for _, pdbFile := range pdbFiles {
		targetDir, err := os.MkdirTemp(os.TempDir(), "pdb-source-")
		if err != nil {
			continue
		}
		if _, err := pdb.Extract(pdbFile, targetDir, true); err != nil {
			continue
		}
		_ = filepath.WalkDir(targetDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".cs") {
				return nil
			}
			base := filepath.Base(path)
			if index[base] == nil {
				index[base] = make(map[string]struct{})
			}
			index[base][path] = struct{}{}
			return nil
		})
	}
	return index
}

// pickBestFilenameMatch 在 candidates 中选出与 original 路径从右向左匹配路径段数最长
// 的一个；匹配段数相同则取字典序最小的路径，保证结果确定性。
func pickBestFilenameMatch(original string, candidates map[string]struct{}) string {
	paths := make([]string, 0, len(candidates))
	for p := range candidates {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	best := paths[0]
	bestLen := commonPathSuffixLen(original, best)
	for _, p := range paths[1:] {
		if l := commonPathSuffixLen(original, p); l > bestLen {
			bestLen = l
			best = p
		}
	}
	return best
}

// commonPathSuffixLen 计算 a、b 两个路径从右向左连续相同的路径段数。
func commonPathSuffixLen(a, b string) int {
	as := strings.Split(filepath.ToSlash(a), "/")
	bs := strings.Split(filepath.ToSlash(b), "/")
	n := 0
	for i, j := len(as)-1, len(bs)-1; i >= 0 && j >= 0; i, j = i-1, j-1 {
		if as[i] != bs[j] {
			break
		}
		n++
	}
	return n
}
