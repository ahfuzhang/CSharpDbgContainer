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
	"os"
	"regexp"
	"sort"
	"strconv"
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
	// "BetFlow.<TriggerAsync>d__5<TRequest, TResponse>"，捕获父类名与原始方法名。
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

// mergeCompilerGeneratedClassesIntoParents 解析 cobertura xml，把编译器生成的类
// （async 状态机、lambda 缓存、闭包）合并进各自的父类，并从输出中移除这些生成类，
// 使每个源文件只保留“真实”的类，避免 reportgenerator 因泛型父类与生成类
// DisplayName 相同而产生的渲染竞态。
func mergeCompilerGeneratedClassesIntoParents(data []byte) ([]byte, error) {
	var doc coberturaDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse cobertura xml: %w", err)
	}

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

	out, err := xml.MarshalIndent(&doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal cobertura xml: %w", err)
	}
	return append([]byte(xml.Header), out...), nil
}

// cleanCoberturaCompilerGeneratedClasses 读取 coberturaFile，合并其中的编译器
// 生成类后原地写回，供 reportgenerator 生成正确反映覆盖率的 HTML 报表。
func cleanCoberturaCompilerGeneratedClasses(coberturaFile string) error {
	data, err := os.ReadFile(coberturaFile)
	if err != nil {
		return fmt.Errorf("read cobertura xml: %w", err)
	}
	cleaned, err := mergeCompilerGeneratedClassesIntoParents(data)
	if err != nil {
		return err
	}
	return os.WriteFile(coberturaFile, cleaned, 0o644)
}
