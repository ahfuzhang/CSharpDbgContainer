package debugadmin

import (
	"bytes"
	"context"
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
	"text/template"
	"time"
)

const traceIDLayout = "20060102150405.000"

var traceIDPattern = regexp.MustCompile(`^\d{14}\.\d{3}$`)

type AdminHandler struct {
	traces             *TraceStore
	broker             *LogBroker
	target             *TargetProcess
	speedscope         fs.FS
	vectorTOMLTemplate *template.Template
	targetLabel        string
}

func NewHTTPServer(options Options, staticFS fs.FS, vectorTOMLTemplate *template.Template, broker *LogBroker, target *TargetProcess) (*http.Server, error) {
	speedscopeFS, err := fs.Sub(staticFS, "build/speedscope")
	if err != nil {
		return nil, fmt.Errorf("load embedded speedscope files: %w", err)
	}
	handler := &AdminHandler{
		traces:             NewTraceStore(),
		broker:             broker,
		target:             target,
		speedscope:         speedscopeFS,
		vectorTOMLTemplate: vectorTOMLTemplate,
		targetLabel:        options.Startup,
	}
	mux := http.NewServeMux()
	handler.Register(mux)
	return &http.Server{
		Addr:    fmt.Sprintf(":%d", options.AdminPort),
		Handler: mux,
	}, nil
}

func (h *AdminHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/", h.handleRoot)
	mux.HandleFunc("/log", h.handleLog)
	mux.HandleFunc("/stack", h.handleStack)
	mux.HandleFunc("/trace", h.handleTrace)
	mux.HandleFunc("/profile_list", h.handleProfileList)
	mux.HandleFunc("/profile/", h.handleProfile)
	mux.Handle("/speedscope/", http.StripPrefix("/speedscope/", http.FileServer(http.FS(h.speedscope))))
}

func (h *AdminHandler) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Add("Content-Type", "text/html")
	_, _ = fmt.Fprintf(w, "DebugAdmin target=%s pid=%d<br/>\n", h.targetLabel, h.target.PID())
	_, _ = io.WriteString(w, "GET /log\n<br/>GET /stack\n<br/>GET /trace?seconds=10\n<br/>GET /profile_list\n<br/>GET /speedscope/\n<br/>")
	_, _ = fmt.Fprintf(w, `
		<a href="/log" target="_blank">show log</a><br/>
		<a href="/stack" target="_blank">show stack</a><br/>
		<a href="/profile_list" target="_blank">show profile list</a><br/>
		Trace <input type="text" size=4 value=10 id="seconds"/> seconds, then <input type="button" value="Show Profile" onclick="profile()"/><br/>
		<script>
		function profile(){
			var textbox = document.getElementById("seconds");
			window.open("/trace?seconds=" + textbox.value, "about:blank");
		}
		</script>
	`)
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

	startupOutput, stackOutput, stderrOutput, err := collectStackOutput(ctx, h.target.PID())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	renderStackHTML(w, startupOutput, stackOutput, stderrOutput, err)
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

func renderStackHTML(w http.ResponseWriter, startupOutput, stackOutput, stderrOutput string, runErr error) {
	threads, misc := parseStackBlocks(stackOutput)

	_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><title>Stack</title><style>
body{margin:0;padding:24px;background:#f3f4f6;color:#111827;font-family:Consolas,Monaco,monospace;}
.wrap{max-width:1200px;margin:0 auto;background:#ffffff;border:1px solid #d1d5db;border-radius:12px;padding:18px 20px;}
h1{margin:0 0 12px 0;font-size:20px;}
h2{margin:18px 0 8px 0;font-size:14px;color:#1f2937;}
.startup{margin:0;background:#f9fafb;border:1px solid #e5e7eb;border-radius:8px;padding:10px;font-size:12px;color:#6b7280;white-space:pre-wrap;line-height:1.35;}
.thread{margin-top:14px;}
.thread-title{font-weight:700;color:#b91c1c;}
.thread-stack{margin-top:6px;margin-left:16px;padding-left:10px;border-left:2px solid #d1d5db;}
.frame,.thread-extra{white-space:pre-wrap;line-height:1.4;}
.frame-idx{color:#374151;}
.frame-ptr{color:#9ca3af;}
.frame-dll{color:#6b7280;}
.frame-sep{color:#6b7280;}
.frame-func{color:#111827;}
.frame-at{color:#6b7280;}
.frame-path{color:#0f766e;}
.frame-file{color:#1d4ed8;font-weight:700;}
.thread-extra{color:#374151;}
.misc,.stderr,.error{margin-top:16px;white-space:pre-wrap;padding:10px;border-radius:8px;}
.misc{background:#eef2ff;border:1px solid #c7d2fe;color:#1f2937;}
.stderr{background:#fff7ed;border:1px solid #fed7aa;color:#7c2d12;}
.error{background:#fee2e2;border:1px solid #fecaca;color:#991b1b;}
</style></head><body><div class="wrap"><h1>Stack Dump</h1>`)

	startupTrimmed := strings.TrimSpace(startupOutput)
	if startupTrimmed != "" {
		_, _ = io.WriteString(w, `<h2>netcoredbg stdout (before "bt all")</h2>`)
		_, _ = fmt.Fprintf(w, `<pre class="startup">%s</pre>`, html.EscapeString(startupTrimmed))
	}

	if len(threads) == 0 && strings.TrimSpace(stackOutput) == "" {
		_, _ = io.WriteString(w, `<div class="misc">No stack data returned.</div>`)
	}

	for _, thread := range threads {
		_, _ = io.WriteString(w, `<div class="thread">`)
		_, _ = fmt.Fprintf(w, `<div class="thread-title">%s</div>`, html.EscapeString(thread.header))
		if len(thread.frames) > 0 || len(thread.extra) > 0 {
			_, _ = io.WriteString(w, `<div class="thread-stack">`)
			for _, frame := range thread.frames {
				_, _ = fmt.Fprintf(w, `<div class="frame">%s</div>`, formatStackFrameHTML(frame))
			}
			for _, detail := range thread.extra {
				_, _ = fmt.Fprintf(w, `<div class="thread-extra">%s</div>`, html.EscapeString(detail))
			}
			_, _ = io.WriteString(w, `</div>`)
		}
		_, _ = io.WriteString(w, `</div>`)
	}

	if len(misc) > 0 {
		_, _ = io.WriteString(w, `<h2>Other Output</h2>`)
		_, _ = fmt.Fprintf(w, `<pre class="misc">%s</pre>`, html.EscapeString(strings.Join(misc, "\n")))
	}

	stderrTrimmed := strings.TrimSpace(stderrOutput)
	if stderrTrimmed != "" {
		_, _ = io.WriteString(w, `<h2>netcoredbg stderr</h2>`)
		_, _ = fmt.Fprintf(w, `<pre class="stderr">%s</pre>`, html.EscapeString(stderrTrimmed))
	}

	if runErr != nil {
		_, _ = fmt.Fprintf(w, `<div class="error">stack command error: %s</div>`, html.EscapeString(runErr.Error()))
	}

	_, _ = io.WriteString(w, `</div></body></html>`)
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
		cmd := BuildTraceCommand(h.target.PID(), seconds, outputPath, profile)
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
