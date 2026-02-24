package debugadmin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const traceIDLayout = "20060102150405.000"

var traceIDPattern = regexp.MustCompile(`^\d{14}\.\d{3}$`)

type AdminHandler struct {
	traces      *TraceStore
	broker      *LogBroker
	target      *TargetProcess
	speedscope  fs.FS
	targetLabel string
}

func NewHTTPServer(options Options, staticFS fs.FS, broker *LogBroker, target *TargetProcess) (*http.Server, error) {
	speedscopeFS, err := fs.Sub(staticFS, "build/speedscope")
	if err != nil {
		return nil, fmt.Errorf("load embedded speedscope files: %w", err)
	}
	handler := &AdminHandler{
		traces:      NewTraceStore(),
		broker:      broker,
		target:      target,
		speedscope:  speedscopeFS,
		targetLabel: options.Startup,
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
	mux.HandleFunc("/profile/", h.handleProfile)
	mux.Handle("/speedscope/", http.StripPrefix("/speedscope/", http.FileServer(http.FS(h.speedscope))))
}

func (h *AdminHandler) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Add("Content-Type", "text/html")
	_, _ = fmt.Fprintf(w, "DebugAdmin target=%s pid=%d<br/>\n", h.targetLabel, h.target.PID())
	_, _ = io.WriteString(w, "GET /log\n<br/>GET /stack\n<br/>GET /trace?seconds=10\n<br/>GET /speedscope/\n<br/>")
	_, _ = fmt.Fprintf(w, `
		<a href="/log" target="_blank">show log</a><br/>
		<a href="/stack" target="_blank">show stack</a><br/>
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

	cmd := BuildStackCommand(ctx, h.target.PID())
	cmd.Stdin = strings.NewReader("bt all\n")
	output, err := cmd.Output()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if len(output) > 0 {
		_, _ = w.Write(output)
	}
	if err != nil {
		_, _ = fmt.Fprintf(w, "\n[stack command error] %v\n", err)
	}
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
