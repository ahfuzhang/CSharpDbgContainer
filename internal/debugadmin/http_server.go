package debugadmin

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"text/template"
	"time"
)

//go:embed index.html.tpl
var indexHTMLContent string

var indexHTMLTemplate = template.Must(template.New("index.html").Parse(indexHTMLContent))

//go:embed threadinfo.html.tpl
var threadInfoHTMLContent string

var threadInfoHTMLTemplate = template.Must(template.New("threadinfo.html").Parse(threadInfoHTMLContent))

//go:embed stack_info.html.tpl
var stackInfoHTMLContent string

var stackInfoHTMLTemplate = template.Must(template.New("stack_info.html").Parse(stackInfoHTMLContent))

const traceIDLayout = "20060102150405.000"

var traceIDPattern = regexp.MustCompile(`^\d{14}\.\d{3}$`)
var gdbLogNamePattern = regexp.MustCompile(`^\d{8}-\d{6}\.log$`)

type AdminHandler struct {
	traces             *TraceStore
	broker             *LogBroker
	target             atomic.Pointer[TargetProcess]
	history            *RunHistory
	speedscope         fs.FS
	vectorTOMLTemplate *template.Template
	targetLabel        string
}

// NewHTTPServer 启动 http 服务器
func NewHTTPServer(staticFS fs.FS, vectorTOMLTemplate *template.Template, broker *LogBroker, target *TargetProcess, history *RunHistory) (*http.Server, *AdminHandler, error) {
	speedscopeFS, err := fs.Sub(staticFS, "build/speedscope")
	if err != nil {
		return nil, nil, fmt.Errorf("load embedded speedscope files: %w", err)
	}
	handler := &AdminHandler{
		traces:             NewTraceStore(),
		broker:             broker,
		history:            history,
		speedscope:         speedscopeFS,
		vectorTOMLTemplate: vectorTOMLTemplate,
		targetLabel:        strings.Join(GlobalOptions.StartupParams, " "),
	}
	handler.target.Store(target)
	mux := http.NewServeMux()
	handler.Register(mux)
	return &http.Server{
		Addr:    fmt.Sprintf(":%d", GlobalOptions.AdminPort),
		Handler: mux,
	}, handler, nil
}

// SetTarget 在子进程被重启后，切换 AdminHandler 指向的目标进程。
func (h *AdminHandler) SetTarget(target *TargetProcess) {
	h.target.Store(target)
}

func (h *AdminHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleRoot)
	mux.HandleFunc("/log", h.handleLog)
	mux.HandleFunc("/stack", h.handleStack)
	mux.HandleFunc("/show_threads", h.handleShowThreads)
	mux.HandleFunc("/trace", h.handleTrace)
	mux.HandleFunc("/profile_list", h.handleProfileList)
	mux.HandleFunc("/profile/", h.handleProfile)
	mux.HandleFunc("/gdb-log", h.handleGDBLog)
	mux.HandleFunc("/current-gdb-log", h.handleCurrentGDBLog)
	mux.HandleFunc("/code_coverage/", h.handleCodeCoverage)
	mux.HandleFunc("/reset_coverage_data", h.handleResetCoverageData)
	mux.HandleFunc("/code_coverage_report/{uuid}/", h.handleCodeCoverageReport)
	mux.HandleFunc("/code_coverage_xml/{name}", h.handleCodeCoverageXML)
	mux.HandleFunc("/get_code_coverage_list", h.handleGetCodeCoverageList)
	mux.Handle("/speedscope/", http.StripPrefix("/speedscope/", http.FileServer(http.FS(h.speedscope))))
}

type indexPageData struct {
	TargetLabel       string
	PID               int
	CWD               string
	ShowCurrentGDBLog bool
	WithCoverage      bool
	Processes         []ProcessInfo
	RunHistory        []runHistoryRow
	CoverageHistory   []coverageHistoryRow
}

// coverageHistoryRow 是 CoverageRecord 格式化之后、可直接交给模板渲染的一行。
type coverageHistoryRow struct {
	Time         string
	CoveragePct  string
	LinesCovered int
	LinesValid   int
	XMLName      string
	XMLURL       string
	ReportURL    string
}

// buildCoverageHistoryRows 把代码覆盖率采集历史格式化为模板可直接渲染的行，
// 按采集时间倒序排列（最近一次采集在最前面）。
func buildCoverageHistoryRows(records []CoverageRecord) []coverageHistoryRow {
	rows := make([]coverageHistoryRow, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		name := filepath.Base(record.CoberturaFile)
		rows = append(rows, coverageHistoryRow{
			Time:         html.EscapeString(record.Timestamp.Format(time.RFC3339)),
			CoveragePct:  fmt.Sprintf("%.2f%%", record.LineRate*100),
			LinesCovered: record.LinesCovered,
			LinesValid:   record.LinesValid,
			XMLName:      html.EscapeString(name),
			XMLURL:       "/code_coverage_xml/" + name,
			ReportURL:    "/code_coverage_report/" + record.ReportID + "/",
		})
	}
	return rows
}

// runHistoryRow 是 RunRecord 格式化之后、可直接交给模板渲染的一行。
// 字段值为转义后的纯数据，不包含任何 HTML 标签，具体展示样式由模板决定。
type runHistoryRow struct {
	Index        int
	PID          int
	Start        string
	End          string
	Duration     string
	ExitCode     int
	Signal       string
	Abnormal     bool
	ErrMsg       string
	CoreDumpPath string
	GDBLogPath   string
	GDBLogIndex  int
	LastLogs     string
}

func (h *AdminHandler) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Add("Content-Type", "text/html")
	target := h.target.Load()
	pid := h.resolveTargetPID()
	_ = indexHTMLTemplate.Execute(w, indexPageData{
		TargetLabel:       h.targetLabel,
		PID:               pid,
		CWD:               html.EscapeString(readProcessCwd(pid)),
		ShowCurrentGDBLog: target != nil && target.GDBLogPath() != "",
		WithCoverage:      GlobalOptions.WithCoverage,
		Processes:         listContainerProcesses(GlobalOptions.StartupParams),
		RunHistory:        buildRunHistoryRows(h.history.Snapshot()),
		CoverageHistory:   buildCoverageHistoryRows(SnapshotCoverageHistory()),
	})
}

// buildRunHistoryRows 把目标子进程的启动记录（启动时间、结束时间、退出码/信号、
// 是否有 core dump 文件，以及退出前的最后若干行日志）格式化为模板可直接渲染的行，
// 按时间倒序排列（最近一次启动在最前面）。
func buildRunHistoryRows(records []RunRecord) []runHistoryRow {
	rows := make([]runHistoryRow, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		rows = append(rows, runHistoryRow{
			Index:        i + 1,
			PID:          record.PID,
			Start:        html.EscapeString(record.StartTime.Format(time.RFC3339)),
			End:          html.EscapeString(record.EndTime.Format(time.RFC3339)),
			Duration:     html.EscapeString(record.EndTime.Sub(record.StartTime).Truncate(time.Millisecond).String()),
			ExitCode:     record.ExitCode,
			Signal:       html.EscapeString(record.Signal),
			Abnormal:     record.Abnormal,
			ErrMsg:       html.EscapeString(record.Err),
			CoreDumpPath: html.EscapeString(record.CoreDumpPath),
			GDBLogPath:   html.EscapeString(record.GDBLogPath),
			GDBLogIndex:  i,
			LastLogs:     html.EscapeString(strings.Join(record.LastLogs, "\n")),
		})
	}
	return rows
}

func (h *AdminHandler) handleGDBLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	index, err := strconv.Atoi(r.URL.Query().Get("index"))
	if err != nil || index < 0 {
		http.Error(w, "invalid gdb log index", http.StatusBadRequest)
		return
	}
	records := h.history.Snapshot()
	if index >= len(records) || !isGDBLogPath(records[index].GDBLogPath) {
		http.Error(w, "gdb log not found", http.StatusNotFound)
		return
	}
	h.writeGDBLog(w, r, records[index].GDBLogPath)
}

// handleCurrentGDBLog returns the log file associated with the currently running target.
func (h *AdminHandler) handleCurrentGDBLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	target := h.target.Load()
	if target == nil || !isGDBLogPath(target.GDBLogPath()) {
		http.Error(w, "current gdb log not found", http.StatusNotFound)
		return
	}
	h.writeGDBLog(w, r, target.GDBLogPath())
}

func (h *AdminHandler) writeGDBLog(w http.ResponseWriter, r *http.Request, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "read gdb log failed", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Vary", "Accept-Encoding")
	if acceptsGzip(r.Header.Get("Accept-Encoding")) {
		w.Header().Set("Content-Encoding", "gzip")
		gzipWriter := gzip.NewWriter(w)
		if _, err := gzipWriter.Write(data); err != nil {
			return
		}
		_ = gzipWriter.Close()
		return
	}
	_, _ = w.Write(data)
}

func isGDBLogPath(path string) bool {
	return filepath.Dir(path) == os.TempDir() && gdbLogNamePattern.MatchString(filepath.Base(path))
}

func acceptsGzip(acceptEncoding string) bool {
	for _, encoding := range strings.Split(acceptEncoding, ",") {
		parts := strings.Split(encoding, ";")
		if !strings.EqualFold(strings.TrimSpace(parts[0]), "gzip") {
			continue
		}
		for _, parameter := range parts[1:] {
			name, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if !found || !strings.EqualFold(name, "q") {
				continue
			}
			quality, err := strconv.ParseFloat(value, 64)
			return err == nil && quality > 0
		}
		return true
	}
	return false
}

func (h *AdminHandler) handleLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, cancel := h.broker.Subscribe()
	defer cancel()

	_, _ = fmt.Fprintf(w, "log stream connected at %s\n", time.Now().Format(time.RFC3339))
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case message, ok := <-ch:
			if !ok {
				return
			}
			if _, err := io.WriteString(w, message); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *AdminHandler) handleStack(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	startupOutput, stackOutput, stderrOutput, err := collectStackOutput(ctx, h.resolveTargetPID())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	renderStackHTML(w, startupOutput, stackOutput, stderrOutput, err)
}

// handleShowThreads attaches gdb to an arbitrary pid, runs "thread apply all bt",
// and renders the per-thread backtraces so they can be browsed in a separate window.
func (h *AdminHandler) handleShowThreads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pid, err := strconv.Atoi(r.URL.Query().Get("pid"))
	if err != nil || pid <= 0 {
		http.Error(w, "invalid pid", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	cmd := BuildThreadDumpCommand(ctx, pid)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_ = threadInfoHTMLTemplate.Execute(w, buildThreadInfoPageData(pid, stdout.String(), stderr.String(), runErr))
}

type threadInfoPageData struct {
	PID     int
	Threads []threadInfoRow
	Misc    string
	Stderr  string
	Error   string
}

type threadInfoRow struct {
	ID    int
	Label string
	Stack string
}

// buildThreadInfoPageData turns raw "thread apply all bt" output into rows the
// template can render directly, with all text pre-escaped.
func buildThreadInfoPageData(pid int, stdout, stderrOutput string, runErr error) threadInfoPageData {
	threads, misc := parseStackBlocks(stdout)
	rows := make([]threadInfoRow, 0, len(threads))
	for i, thread := range threads {
		lines := make([]string, 0, len(thread.frames)+len(thread.extra))
		lines = append(lines, thread.frames...)
		lines = append(lines, thread.extra...)
		rows = append(rows, threadInfoRow{
			ID:    i,
			Label: html.EscapeString(thread.header),
			Stack: html.EscapeString(strings.Join(lines, "\n")),
		})
	}
	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
	}
	return threadInfoPageData{
		PID:     pid,
		Threads: rows,
		Misc:    html.EscapeString(strings.Join(misc, "\n")),
		Stderr:  html.EscapeString(strings.TrimSpace(stderrOutput)),
		Error:   html.EscapeString(errMsg),
	}
}

type stackThreadBlock struct {
	header string
	frames []string
	extra  []string
}

func collectStackOutput(ctx context.Context, pid int) (string, string, string, error) {
	cmd := BuildStackCommand(ctx, pid)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", "", fmt.Errorf("read stack stdout failed: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", "", fmt.Errorf("read stack stderr failed: %w", err)
	}
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return "", "", "", fmt.Errorf("open stack stdin failed: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", "", "", fmt.Errorf("start stack command failed: %w", err)
	}

	stdoutCh := make(chan []byte, 32)
	stdoutErrCh := make(chan error, 1)
	go readPipeChunks(ctx, stdoutPipe, stdoutCh, stdoutErrCh)

	stderrCh := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(stderrPipe)
		stderrCh <- string(data)
	}()

	var startupStdout bytes.Buffer
	var allStdout bytes.Buffer
	idleTimer := time.NewTimer(100 * time.Millisecond)
	defer idleTimer.Stop()

	sendCommand := false
	commandIssued := false
	preCommandClosed := false

	for !sendCommand {
		select {
		case chunk, ok := <-stdoutCh:
			if !ok {
				preCommandClosed = true
				sendCommand = true
				break
			}
			allStdout.Write(chunk)
			startupStdout.Write(chunk)
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(100 * time.Millisecond)
		case <-idleTimer.C:
			commandIssued = true
			sendCommand = true
		case <-ctx.Done():
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			_ = cmd.Wait()
			return startupStdout.String(), "", <-stderrCh, ctx.Err()
		}
	}

	var runErr error
	if commandIssued {
		if _, err := io.WriteString(stdinPipe, "bt all\n"); err != nil {
			runErr = fmt.Errorf("send bt all command failed: %w", err)
		}
	}
	if !preCommandClosed {
		if err := stdinPipe.Close(); err != nil && runErr == nil {
			runErr = fmt.Errorf("close stack command stdin failed: %w", err)
		}
	}

	for chunk := range stdoutCh {
		allStdout.Write(chunk)
	}
	stdoutErr := <-stdoutErrCh
	waitErr := cmd.Wait()
	stderrOutput := <-stderrCh

	if runErr == nil && stdoutErr != nil && !errors.Is(stdoutErr, context.Canceled) {
		runErr = fmt.Errorf("read stack stdout failed: %w", stdoutErr)
	}
	if runErr == nil && waitErr != nil {
		runErr = waitErr
	}

	startupLen := startupStdout.Len()
	allBytes := allStdout.Bytes()
	stackBytes := []byte{}
	if startupLen < len(allBytes) {
		stackBytes = allBytes[startupLen:]
	}
	return startupStdout.String(), string(stackBytes), stderrOutput, runErr
}

func readPipeChunks(ctx context.Context, pipe io.Reader, chunkCh chan<- []byte, errCh chan<- error) {
	defer close(chunkCh)
	buffer := make([]byte, 4096)
	for {
		n, err := pipe.Read(buffer)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buffer[:n])
			select {
			case chunkCh <- chunk:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				errCh <- nil
			} else {
				errCh <- err
			}
			return
		}
	}
}

func parseStackBlocks(raw string) ([]stackThreadBlock, []string) {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")

	threads := make([]stackThreadBlock, 0, 16)
	misc := make([]string, 0, 8)
	var current *stackThreadBlock

	flush := func() {
		if current != nil {
			threads = append(threads, *current)
			current = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Thread ") {
			flush()
			current = &stackThreadBlock{header: trimmed}
			continue
		}
		if trimmed == "" {
			continue
		}
		if current == nil {
			misc = append(misc, trimmed)
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			current.frames = append(current.frames, trimmed)
		} else {
			current.extra = append(current.extra, trimmed)
		}
	}
	flush()

	sort.SliceStable(threads, func(i, j int) bool {
		left := strings.Contains(threads[i].header, `name=".NET ThreadPool Worker"`)
		right := strings.Contains(threads[j].header, `name=".NET ThreadPool Worker"`)
		if left == right {
			return false
		}
		return left
	})

	return threads, misc
}

type stackPageData struct {
	StartupOutput string
	NoData        bool
	Threads       []stackThreadRow
	Misc          string
	StderrOutput  string
	Error         string
}

type stackThreadRow struct {
	Header string
	Frames []string
	Extra  []string
}

// buildStackPageData turns raw "bt all" output into rows the template can render
// directly. thread.frames are rendered to HTML via formatStackFrameHTML (safe markup
// built entirely from escaped fragments); everything else is escaped plain text.
func buildStackPageData(startupOutput, stackOutput, stderrOutput string, runErr error) stackPageData {
	threads, misc := parseStackBlocks(stackOutput)

	rows := make([]stackThreadRow, 0, len(threads))
	for _, thread := range threads {
		frames := make([]string, 0, len(thread.frames))
		for _, frame := range thread.frames {
			frames = append(frames, formatStackFrameHTML(frame))
		}
		extra := make([]string, 0, len(thread.extra))
		for _, detail := range thread.extra {
			extra = append(extra, html.EscapeString(detail))
		}
		rows = append(rows, stackThreadRow{
			Header: html.EscapeString(thread.header),
			Frames: frames,
			Extra:  extra,
		})
	}

	errMsg := ""
	if runErr != nil {
		errMsg = html.EscapeString(runErr.Error())
	}

	return stackPageData{
		StartupOutput: html.EscapeString(strings.TrimSpace(startupOutput)),
		NoData:        len(threads) == 0 && strings.TrimSpace(stackOutput) == "",
		Threads:       rows,
		Misc:          html.EscapeString(strings.Join(misc, "\n")),
		StderrOutput:  html.EscapeString(strings.TrimSpace(stderrOutput)),
		Error:         errMsg,
	}
}

func renderStackHTML(w http.ResponseWriter, startupOutput, stackOutput, stderrOutput string, runErr error) {
	_ = stackInfoHTMLTemplate.Execute(w, buildStackPageData(startupOutput, stackOutput, stderrOutput, runErr))
}

func formatStackFrameHTML(frame string) string {
	line := strings.TrimSpace(frame)
	if !strings.HasPrefix(line, "#") {
		return html.EscapeString(line)
	}
	colon := strings.Index(line, ":")
	if colon < 0 {
		return html.EscapeString(line)
	}

	indexPart := strings.TrimSpace(line[:colon+1])
	rest := strings.TrimSpace(line[colon+1:])
	if rest == "" {
		return fmt.Sprintf(`<span class="frame-idx">%s</span>`, html.EscapeString(indexPart))
	}

	addrPart := ""
	if field := strings.Fields(rest); len(field) > 0 && strings.HasPrefix(field[0], "0x") {
		addrPart = field[0]
		rest = strings.TrimSpace(rest[len(field[0]):])
	}

	symbolPart := rest
	sourcePart := ""
	if at := strings.LastIndex(rest, " at "); at >= 0 {
		symbolPart = strings.TrimSpace(rest[:at])
		sourcePart = strings.TrimSpace(rest[at+4:])
	}

	dllPart := ""
	funcPart := symbolPart
	if sep := strings.Index(symbolPart, "`"); sep >= 0 {
		dllPart = strings.TrimSpace(symbolPart[:sep])
		funcPart = strings.TrimSpace(symbolPart[sep+1:])
	}

	sourcePath := ""
	sourceFile := ""
	if sourcePart != "" {
		slash := strings.LastIndex(sourcePart, "/")
		if slash < 0 {
			slash = strings.LastIndex(sourcePart, `\`)
		}
		if slash >= 0 {
			sourcePath = sourcePart[:slash+1]
			sourceFile = sourcePart[slash+1:]
		} else {
			sourceFile = sourcePart
		}
	}

	var b strings.Builder
	b.WriteString(`<span class="frame-idx">`)
	b.WriteString(html.EscapeString(indexPart))
	b.WriteString(`</span>`)

	if addrPart != "" {
		b.WriteString(` <span class="frame-ptr">`)
		b.WriteString(html.EscapeString(addrPart))
		b.WriteString(`</span>`)
	}

	if dllPart != "" {
		b.WriteString(` <span class="frame-dll">`)
		b.WriteString(html.EscapeString(dllPart))
		b.WriteString(`</span><span class="frame-sep">` + html.EscapeString("`") + `</span>`)
	}

	if funcPart != "" {
		b.WriteString(` <span class="frame-func">`)
		b.WriteString(html.EscapeString(funcPart))
		b.WriteString(`</span>`)
	}

	if sourcePart != "" {
		b.WriteString(` <span class="frame-at">at</span> `)
		if sourcePath != "" {
			b.WriteString(`<span class="frame-path">`)
			b.WriteString(html.EscapeString(sourcePath))
			b.WriteString(`</span>`)
		}
		b.WriteString(`<span class="frame-file">`)
		b.WriteString(html.EscapeString(sourceFile))
		b.WriteString(`</span>`)
	}

	return b.String()
}

// resolveTargetPID 返回真正的目标进程 pid。
// 当以 --with.gdb 或 --with.coverage 启动时，h.target.PID() 是 gdb / dotnet-coverage
// 外壳进程的 pid，必须通过 GlobalOptions.StartupParams 在进程树中定位真正的目标进程，
// 否则 dotnet-trace / netcoredbg 会挂到外壳进程上，采集不到目标进程的数据。
func (h *AdminHandler) resolveTargetPID() int {
	pid := h.target.Load().PID()
	if !GlobalOptions.WithGDB && !GlobalOptions.WithCoverage {
		return pid
	}
	if resolved, ok := findTargetDescendantPID(pid, GlobalOptions.StartupParams); ok {
		return resolved
	}
	return pid
}

func (h *AdminHandler) handleTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	seconds, err := parseTraceSeconds(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	traceID := time.Now().Format(traceIDLayout)
	outputPath := filepath.Join("/tmp", traceID+".nettrace")
	redirectURL := "/speedscope/index.html#profileURL=/profile/" + traceID + ".speedscope.json"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, "<!doctype html><html><body><pre>\n")
	_, _ = fmt.Fprintf(w, "trace id: %s\n", traceID)
	flusher.Flush()

	profiles := TraceProfileCandidates()
	for i, profile := range profiles {
		if i > 0 {
			_, _ = fmt.Fprintf(w, "profile %q unsupported, retrying with %q\n", profiles[i-1], profile)
			flusher.Flush()
		}
		stderrLog := &bytes.Buffer{}
		cmd := BuildTraceCommand(h.resolveTargetPID(), seconds, outputPath, profile)
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, stderrLog)
		if err := cmd.Start(); err != nil {
			_, _ = fmt.Fprintf(w, "trace failed: run dotnet-trace failed: %v\n", err)
			_, _ = io.WriteString(w, "</pre></body></html>")
			flusher.Flush()
			return
		}
		if err := streamCountdown(w, r.Context(), flusher, seconds, cmd); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			detail := strings.TrimSpace(stderrLog.String())
			if shouldRetryTraceWithAnotherProfile(detail) && i+1 < len(profiles) {
				continue
			}
			_, _ = fmt.Fprintf(w, "trace failed: %v\n", err)
			if detail != "" {
				_, _ = io.WriteString(w, "dotnet-trace stderr:\n")
				_, _ = io.WriteString(w, tailLines(detail, 12))
				_, _ = io.WriteString(w, "\n")
			}
			_, _ = io.WriteString(w, "</pre></body></html>")
			flusher.Flush()
			return
		}
		h.traces.Add(traceID)
		_, _ = io.WriteString(w, "trace completed, redirecting...\n")
		_, _ = io.WriteString(w, "</pre>")
		_, _ = fmt.Fprintf(w, "<script>window.location.href=%q;</script></body></html>", redirectURL)
		flusher.Flush()
		return
	}

	_, _ = io.WriteString(w, "trace failed: no available trace profile\n</pre></body></html>")
	flusher.Flush()
}

func streamCountdown(w io.Writer, ctx context.Context, flusher http.Flusher, seconds int, cmd *exec.Cmd) error {
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	start := time.Now()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		elapsed := int(time.Since(start).Seconds())
		remaining := seconds - elapsed
		if remaining < 0 {
			remaining = 0
		}
		if _, err := fmt.Fprintf(w, "collecting cpu trace... remaining %d seconds\n", remaining); err != nil {
			return err
		}
		flusher.Flush()

		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func parseTraceSeconds(r *http.Request) (int, error) {
	seconds := 10
	raw := strings.TrimSpace(r.URL.Query().Get("seconds"))
	if raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("invalid seconds: %s", raw)
		}
		seconds = value
	}
	if seconds < 1 || seconds > 30 {
		return 0, fmt.Errorf("seconds must be between 1 and 30")
	}
	return seconds, nil
}

func (h *AdminHandler) handleProfileList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	traceIDs := h.traces.List()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, "<!doctype html><html><head><meta charset=\"utf-8\"><title>Profile List</title></head><body>\n")
	_, _ = io.WriteString(w, "<h3>Profile List</h3>\n")
	if len(traceIDs) == 0 {
		_, _ = io.WriteString(w, "<div>no profiles</div>\n")
		_, _ = io.WriteString(w, "</body></html>")
		return
	}
	for i := len(traceIDs) - 1; i >= 0; i-- {
		traceID := traceIDs[i]
		speedscopeURL := "/speedscope/index.html#profileURL=/profile/" + traceID + ".speedscope.json"
		_, _ = fmt.Fprintf(w, "<div><a href=%q target=\"_blank\" rel=\"noopener\">%s.speedscope.json</a></div>\n", speedscopeURL, html.EscapeString(traceID))
	}
	_, _ = io.WriteString(w, "</body></html>")
}

func (h *AdminHandler) handleProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	traceID, ok := parseProfilePath(r.URL.Path)
	if !ok || !h.traces.Exists(traceID) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join("/tmp", traceID+".speedscope.json")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "open profile failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	http.ServeFile(w, r, path)
}

func parseProfilePath(path string) (string, bool) {
	if !strings.HasPrefix(path, "/profile/") {
		return "", false
	}
	name := strings.TrimPrefix(path, "/profile/")
	if !strings.HasSuffix(name, ".speedscope.json") {
		return "", false
	}
	traceID := strings.TrimSuffix(name, ".speedscope.json")
	if !traceIDPattern.MatchString(traceID) {
		return "", false
	}
	return traceID, true
}

func shouldRetryTraceWithAnotherProfile(stderr string) bool {
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "invalid profile name") {
		return true
	}
	return strings.Contains(lower, "does not apply to `dotnet-trace collect`")
}

func tailLines(content string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	start := len(lines) - maxLines
	return strings.Join(lines[start:], "\n")
}
