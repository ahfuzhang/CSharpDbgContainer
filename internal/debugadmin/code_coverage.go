package debugadmin

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var reportUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var coberturaFileNamePattern = regexp.MustCompile(`^\d{14}\.cobertura\.xml$`)

const maxCoverageHistory = 20

// CoverageRecord 记录一次代码覆盖率采集的结果，用于在首页展示历史趋势。
type CoverageRecord struct {
	Timestamp     time.Time `json:"timestamp"`
	CoberturaFile string    `json:"cobertura_file"`
	ReportID      string    `json:"report_id"`
	LineRate      float64   `json:"line_rate"`
	LinesCovered  int       `json:"lines_covered"`
	LinesValid    int       `json:"lines_valid"`
}

var (
	coverageHistoryMu sync.Mutex
	coverageHistory   []CoverageRecord

	// coverageRunMu 保证同一时刻只有一次采集在执行。
	// handleCodeCoverage 用同一秒的时间戳生成临时文件名（coverageFile/coberturaFile），
	// 如果两个请求并发执行，会互相覆盖对方的临时文件，导致 recordCoverageResult
	// 读取/解析失败而静默丢弃这次采集结果，使得历史记录数少于 maxCoverageHistory。
	coverageRunMu sync.Mutex
)

func appendCoverageHistory(rec CoverageRecord) {
	coverageHistoryMu.Lock()
	defer coverageHistoryMu.Unlock()
	coverageHistory = append(coverageHistory, rec)
	if len(coverageHistory) > maxCoverageHistory {
		coverageHistory = coverageHistory[len(coverageHistory)-maxCoverageHistory:]
	}
}

// SnapshotCoverageHistory 返回当前代码覆盖率历史记录的一份拷贝，供首页模板渲染。
func SnapshotCoverageHistory() []CoverageRecord {
	coverageHistoryMu.Lock()
	defer coverageHistoryMu.Unlock()
	out := make([]CoverageRecord, len(coverageHistory))
	copy(out, coverageHistory)
	return out
}

type coberturaCoverageXML struct {
	XMLName      xml.Name `xml:"coverage"`
	LineRate     float64  `xml:"line-rate,attr"`
	LinesCovered int      `xml:"lines-covered,attr"`
	LinesValid   int      `xml:"lines-valid,attr"`
}

// recordCoverageResult 解析 cobertura xml 里的总体覆盖率数据（line-rate/lines-covered/lines-valid），
// 追加到全局历史记录中，超过 maxCoverageHistory 条后丢弃最旧的一条。
func recordCoverageResult(coberturaFile, reportID string, timestamp time.Time) {
	data, err := os.ReadFile(coberturaFile)
	if err != nil {
		return
	}
	var cov coberturaCoverageXML
	if err := xml.Unmarshal(data, &cov); err != nil {
		return
	}
	appendCoverageHistory(CoverageRecord{
		Timestamp:     timestamp,
		CoberturaFile: coberturaFile,
		ReportID:      reportID,
		LineRate:      cov.LineRate,
		LinesCovered:  cov.LinesCovered,
		LinesValid:    cov.LinesValid,
	})
}

// handleCodeCoverage 采集目标进程当前的代码覆盖率数据并生成 HTML 报告：
//  1. dotnet-coverage snapshot 从正在运行的目标进程（通过 CoverageName 对应的 session）抓取一份 .coverage 快照；
//  2. dotnet-coverage merge 把快照转换为 cobertura xml；
//  3. cleanCoberturaCompilerGeneratedClasses 把 cobertura xml 中编译器生成的类
//     （async 状态机、lambda 缓存、闭包）合并进父类，修复 reportgenerator 对泛型类
//     渲染 async 方法覆盖率丢失/竞态的问题（见 cobertura_merge.go）；
//  4. reportgenerator 把 cobertura xml 渲染成 HTML 报告，输出到以随机 uuid 命名的目录；
//     完成后跳转到 /code_coverage_report/{uuid}/ 展示报告。
func (h *AdminHandler) handleCodeCoverage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !GlobalOptions.WithCoverage {
		http.Error(w, "code coverage is not enabled (missing -with.coverage)", http.StatusNotFound)
		return
	}

	if !coverageRunMu.TryLock() {
		http.Error(w, "another code coverage collection is already in progress, please retry later", http.StatusConflict)
		return
	}
	defer coverageRunMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	reportID := uuid.NewString()
	//timestamp := time.Now().Format("20060102150405")
	coverageFile := filepath.Join(os.TempDir(), reportID+".coverage")
	coberturaFile := filepath.Join(os.TempDir(), reportID+".cobertura.xml")
	htmlDir := filepath.Join(os.TempDir(), reportID)

	snapshotArgs := []string{"snapshot", "--output", coverageFile}
	// if settingsFile := GlobalOptions.CoverageOpts.CoverageXMLSettingsFile; settingsFile != "" {
	// 	snapshotArgs = append(snapshotArgs, "--settings", settingsFile)
	// }
	snapshotArgs = append(snapshotArgs, GlobalOptions.CoverageOpts.CoverageName)

	reportGeneratorArgs := []string{"-reports:" + coberturaFile, "-targetdir:" + htmlDir, "-reporttypes:Html"}
	if sourceDirs := GlobalOptions.CoverageOpts.SourceDirs; sourceDirs != "" {
		reportGeneratorArgs = append(reportGeneratorArgs, "-sourcedirs:"+sourceDirs)
	}

	steps := []struct {
		label string
		cmd   *exec.Cmd
	}{
		{"dotnet-coverage snapshot", exec.CommandContext(ctx, "dotnet-coverage", snapshotArgs...)},
		{"dotnet-coverage merge", exec.CommandContext(ctx, "dotnet-coverage", "merge", coverageFile, "--output", coberturaFile, "--output-format", "cobertura")},
		{"reportgenerator", exec.CommandContext(ctx, "reportgenerator", reportGeneratorArgs...)},
	}
	for _, step := range steps {
		output, err := step.cmd.CombinedOutput()
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			renderCodeCoverageErrorHTML(w, step.label, err, string(output))
			return
		}
		if step.label == "dotnet-coverage merge" {
			if err := cleanCoberturaCompilerGeneratedClasses(coberturaFile); err != nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusInternalServerError)
				renderCodeCoverageErrorHTML(w, "merge compiler-generated classes in cobertura xml", err, "")
				return
			}
		}
	}
	recordCoverageResult(coberturaFile, reportID, time.Now())
	http.Redirect(w, r, "/code_coverage_report/"+reportID+"/", http.StatusFound)
}

// handleResetCoverageData 清空目标进程当前的代码覆盖率数据：
// dotnet-coverage snapshot ${session_id} --output /dev/null --reset true
// 命令行创建成功后立即返回，不等待其执行完成。
func (h *AdminHandler) handleResetCoverageData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !GlobalOptions.WithCoverage {
		http.Error(w, "code coverage is not enabled (missing -with.coverage)", http.StatusNotFound)
		return
	}

	cmd := exec.Command("dotnet-coverage", "snapshot", GlobalOptions.CoverageOpts.CoverageName, "--output", "/dev/null", "--reset", "true")
	if err := cmd.Start(); err != nil {
		http.Error(w, fmt.Sprintf("start dotnet-coverage snapshot failed: %v", err), http.StatusInternalServerError)
		return
	}
	go func() { _ = cmd.Wait() }()

	w.WriteHeader(http.StatusOK)
}

func renderCodeCoverageErrorHTML(w http.ResponseWriter, step string, err error, output string) {
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>Code Coverage Error</title></head><body>
<h2 style="color:#b91c1c">%s failed</h2>
<pre>%s</pre>
</body></html>`, html.EscapeString(step), html.EscapeString(strings.TrimSpace(output+"\n"+err.Error())))
}

// handleCodeCoverageReport 把 /code_coverage_report/{uuid}/... 映射到 reportgenerator
// 输出的报告目录 /tmp/{uuid}/，供浏览器直接访问生成好的 HTML 报告。
func (h *AdminHandler) handleCodeCoverageReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	reportID := r.PathValue("uuid")
	if !reportUUIDPattern.MatchString(reportID) {
		http.Error(w, "invalid report id", http.StatusBadRequest)
		return
	}
	dir := filepath.Join(os.TempDir(), reportID)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		http.NotFound(w, r)
		return
	}
	prefix := "/code_coverage_report/" + reportID + "/"
	http.StripPrefix(prefix, http.FileServer(http.Dir(dir))).ServeHTTP(w, r)
}

// handleCodeCoverageXML 提供 cobertura xml 文件下载，文件名限定为 handleCodeCoverage 生成的格式，
// 避免任意路径读取。
func (h *AdminHandler) handleCodeCoverageXML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := r.PathValue("name")
	if !coberturaFileNamePattern.MatchString(name) {
		http.Error(w, "invalid file name", http.StatusBadRequest)
		return
	}
	path := filepath.Join(os.TempDir(), name)
	if _, err := os.Stat(path); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, path)
}

// handleGetCodeCoverageList 以 json 格式返回当前所有代码覆盖率历史记录（最新的排在最前面）。
func (h *AdminHandler) handleGetCodeCoverageList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !GlobalOptions.WithCoverage {
		http.Error(w, "code coverage is not enabled (missing -with.coverage)", http.StatusNotFound)
		return
	}

	records := SnapshotCoverageHistory()
	list := make([]CoverageRecord, len(records))
	for i, record := range records {
		list[len(records)-1-i] = record
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(list); err != nil {
		http.Error(w, "encode response failed: "+err.Error(), http.StatusInternalServerError)
	}
}
