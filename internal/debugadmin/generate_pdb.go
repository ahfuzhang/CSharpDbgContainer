package debugadmin

import (
	"encoding/xml"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// moduleFilterXML 对应 -coverage.xml.settings 指定的 dotnet-coverage settings xml 中
// ModulePaths 下的 Include/Exclude 列表，参考 doc/example.code.coverage.settings.xml。
type moduleFilterXML struct {
	XMLName      xml.Name `xml:"Configuration"`
	CodeCoverage struct {
		ModulePaths struct {
			Include struct {
				ModulePath []string `xml:"ModulePath"`
			} `xml:"Include"`
			Exclude struct {
				ModulePath []string `xml:"ModulePath"`
			} `xml:"Exclude"`
		} `xml:"ModulePaths"`
	} `xml:"CodeCoverage"`
}

// moduleFilter 是 moduleFilterXML 解析出的正则表达式，用于筛选需要生成 pdb 的 dll。
type moduleFilter struct {
	include []*regexp.Regexp
	exclude []*regexp.Regexp
}

// loadModuleFilterFromXML 解析 -coverage.xml.settings 指定的 xml 文件，取出 ModulePaths
// 下 Include/Exclude 的正则表达式列表。
func loadModuleFilterFromXML(path string) (*moduleFilter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q failed: %w", path, err)
	}
	var doc moduleFilterXML
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %q failed: %w", path, err)
	}
	filter := &moduleFilter{}
	for _, pattern := range doc.CodeCoverage.ModulePaths.Include.ModulePath {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid Include ModulePath pattern %q in %q: %w", pattern, path, err)
		}
		filter.include = append(filter.include, re)
	}
	for _, pattern := range doc.CodeCoverage.ModulePaths.Exclude.ModulePath {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid Exclude ModulePath pattern %q in %q: %w", pattern, path, err)
		}
		filter.exclude = append(filter.exclude, re)
	}
	return filter, nil
}

// matches 判断 dllPath 是否应当参与 pdb 生成：Include 为空表示全部命中，非空时必须至少
// 命中一条才算选中；之后再看 Exclude，命中任意一条即排除，优先级高于 Include。
func (f *moduleFilter) matches(dllPath string) bool {
	if f == nil {
		return true
	}
	if len(f.include) > 0 {
		included := false
		for _, re := range f.include {
			if re.MatchString(dllPath) {
				included = true
				break
			}
		}
		if !included {
			return false
		}
	}
	for _, re := range f.exclude {
		if re.MatchString(dllPath) {
			return false
		}
	}
	return true
}

// findDLLFiles 递归搜索 dir 下所有 .dll 文件，忽略单个条目的错误（如权限问题）以便
// 继续搜索其余部分。
func findDLLFiles(dir string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".dll") {
			result = append(result, path)
		}
		return nil
	})
	return result, err
}

// pdbPathForDLL 返回 dll 对应的 pdb 文件路径：同目录、同名、扩展名替换为 .pdb。
func pdbPathForDLL(dllPath string) string {
	return strings.TrimSuffix(dllPath, filepath.Ext(dllPath)) + ".pdb"
}

// buildIlspycmdGeneratePDBCommand 构造为单个 dll 生成 pdb 的命令行：
//
//	ilspycmd --generate-pdb --disable-updatecheck --referencepath <dll所在目录> <dllPath>
func buildIlspycmdGeneratePDBCommand(dllPath string) *exec.Cmd {
	return exec.Command("ilspycmd",
		"--generate-pdb",
		"--disable-updatecheck",
		"--referencepath", filepath.Dir(dllPath),
		dllPath,
	)
}

// GeneratePDBFromDLLs 递归遍历 runDir 下的所有 dll，为其中尚未生成 pdb 的调用 ilspycmd
// 生成 pdb 文件（与 dll 同目录）。当 coverageXMLSettingsFile 非空时，先按其中
// ModulePaths 的 Include/Exclude 规则筛选出需要处理的 dll。单个 dll 生成失败只打印
// 错误、跳过该 dll，不影响其余 dll 的处理；只应在进程启动时调用一次。runDir 应当是
// 目标进程实际的工作目录（例如通过 /proc/<pid>/cwd 读取），而不是启动 dll/可执行文件
// 所在目录：启动文件可能只是工作目录下某个子目录里的一个文件，同工作目录下的其他 dll
// 不一定和它在同一层级。
func GeneratePDBFromDLLs(runDir string, coverageXMLSettingsFile string) error {
	info, err := os.Stat(runDir)
	if err != nil {
		return fmt.Errorf("run directory %q not accessible: %w", runDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("run directory %q is not a directory", runDir)
	}

	var filter *moduleFilter
	if coverageXMLSettingsFile != "" {
		filter, err = loadModuleFilterFromXML(coverageXMLSettingsFile)
		if err != nil {
			return err
		}
	}

	dllFiles, err := findDLLFiles(runDir)
	if err != nil {
		return fmt.Errorf("search dll files under %q failed: %w", runDir, err)
	}

	for _, dllPath := range dllFiles {
		if !filter.matches(dllPath) {
			continue
		}
		pdbPath := pdbPathForDLL(dllPath)
		if _, statErr := os.Stat(pdbPath); statErr == nil {
			continue // pdb 已存在，跳过
		}
		output, runErr := buildIlspycmdGeneratePDBCommand(dllPath).CombinedOutput()
		if runErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "generate pdb for %q failed: %v: %s\n", dllPath, runErr, strings.TrimSpace(string(output)))
			continue
		}
		_, _ = fmt.Fprintf(os.Stdout, "generated pdb: %s\n", pdbPath)
	}
	return nil
}
