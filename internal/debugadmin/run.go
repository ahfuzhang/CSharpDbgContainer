package debugadmin

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"golang.org/x/sys/unix"
)

const (
	defaultPort = 8089
	//configPath  = "init.config.yaml"
	vectorCfg = "/tmp/vector.toml"
)

// Run 入口函数
// @param staticFS speedscope的 html 静态资源文件夹
// @param vectorTOMLTemplate logging/vector/vector.toml 的模板文件
func Run(staticFS fs.FS, vectorTOMLTemplate *template.Template) int {
	// todo: 检查特殊的分隔符 --
	options, err := loadOptions(os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "parse options failed: %v\n", err)
		return 2
	}
	if options.CoreDumpUnlimited {
		if err := enableUnlimitedCoreDump(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "set RLIMIT_CORE to unlimited failed: %v\n", err)
			return 1
		}
	}

	var (
		vectorProc  *VectorProcess
		vectorStdin io.Writer
	)
	if options.LogPushURL != "" {
		if err := checkLogPushURLReachable(options.LogPushURL); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "check -log.push.url failed: %v\n", err)
			return 2
		}
		vectorProc, err = StartVectorProcess(vectorTOMLTemplate, options.LogPushURL)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "start vector process failed: %v\n", err)
			return 1
		}
		vectorStdin = vectorProc.Stdin()
		defer func() {
			if stopErr := vectorProc.Stop(); stopErr != nil {
				_, _ = fmt.Fprintf(os.Stderr, "stop vector process failed: %v\n", stopErr)
			}
		}()
	}

	broker := NewLogBroker()
	history := NewRunHistory()
	// 创建子进程
	target, err := StartTarget(options, broker, vectorStdin, options.LogStdoutOutput, history)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "start target process failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "target process started, pid=%d\n", target.PID())

	server, handler, err := NewHTTPServer(options, staticFS, vectorTOMLTemplate, broker, target, history)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create http server failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "DebugAdmin listening on http://:%d\n", options.AdminPort)
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.ListenAndServe()
	}()

	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		<-serverErrCh
	}

	for {
		select {
		case targetErr := <-target.Done():
			_, _, abnormal := classifyExit(targetErr)
			// 开启 auto.restart 且进程异常退出时，立即重建子进程；正常退出则忽略。
			if options.AutoRestart && abnormal {
				_, _ = fmt.Fprintf(os.Stdout, "target process crashed (err=%v), restarting...\n", targetErr)
				newTarget, restartErr := StartTarget(options, broker, vectorStdin, options.LogStdoutOutput, history)
				if restartErr != nil {
					_, _ = fmt.Fprintf(os.Stderr, "restart target process failed: %v\n", restartErr)
					shutdown()
					return 1
				}
				target = newTarget
				handler.SetTarget(newTarget)
				_, _ = fmt.Fprintf(os.Stdout, "target process restarted, pid=%d\n", target.PID())
				continue
			}
			_, _ = fmt.Fprintf(os.Stdout, "target process finished, DebugAdmin will exit, err=%v\n", targetErr)
			shutdown()
			if targetErr != nil {
				return 1
			}
			return 0
		case serverErr := <-serverErrCh:
			if serverErr != nil && !errors.Is(serverErr, http.ErrServerClosed) {
				_, _ = fmt.Fprintf(os.Stderr, "http server error: %v\n", serverErr)
				return 1
			}
			return 0
		}
	}
}

func loadOptions(args []string) (*Options, error) {
	var startupParams []string
	for idx, item := range args {
		if item == "--" {
			startupParams = args[idx+1:]
			args = args[:idx]
			break
		}
	}
	port := defaultPort
	//startup := cfg.Startup
	logPushURL := ""
	logStdoutOutput := true
	coreDumpUnlimited := false
	autoRestart := false
	withGDB := false

	flagSet := flag.NewFlagSet("DebugAdmin", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	flagSet.IntVar(&port, "admin.port", port, "http service listen port")
	//flagSet.StringVar(&startup, "startup", startup, "startup dll or executable")
	flagSet.StringVar(&logPushURL, "log.push.url", logPushURL, "push logs to remote endpoint URL via vector")
	flagSet.BoolVar(&logStdoutOutput, "log.stdout.output", logStdoutOutput, "output target process stdout/stderr to DebugAdmin stdout/stderr")
	flagSet.BoolVar(&coreDumpUnlimited, "coredump.unlimited", coreDumpUnlimited, "set the core dump size limit to unlimited")
	flagSet.BoolVar(&autoRestart, "auto.restart", autoRestart, "automatically restart the target process when it crashes")
	flagSet.BoolVar(&withGDB, "with.gdb", withGDB, "start the target process with gdb")
	if err := flagSet.Parse(args); err != nil {
		return nil, err
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("admin.port should be between 1 and 65535, got %d", port)
	}
	//startup = strings.TrimSpace(startup)
	if len(startupParams) == 0 {
		return nil, errors.New("startup is required; use -- <startup command>")
	}
	logPushURL = strings.TrimSpace(logPushURL)
	return &Options{
		AdminPort:         port,
		StartupParams:     startupParams,
		LogPushURL:        logPushURL,
		LogStdoutOutput:   logStdoutOutput,
		CoreDumpUnlimited: coreDumpUnlimited,
		AutoRestart:       autoRestart,
		WithGDB:           withGDB,
	}, nil
}

func enableUnlimitedCoreDump() error {
	limit := unix.Rlimit{
		Cur: unix.RLIM_INFINITY,
		Max: unix.RLIM_INFINITY,
	}
	return unix.Setrlimit(unix.RLIMIT_CORE, &limit)
}

type VectorProcess struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan error
}

func StartVectorProcess(vectorTOMLTemplate *template.Template, logPushURL string) (*VectorProcess, error) {
	if err := writeVectorConfig(vectorTOMLTemplate, logPushURL); err != nil {
		return nil, err
	}

	cmd := exec.Command("vector", "-c", vectorCfg)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open vector stdin failed: %w", err)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("run vector failed: %w", err)
	}

	proc := &VectorProcess{
		cmd:   cmd,
		stdin: stdin,
		done:  make(chan error, 1),
	}
	go func() {
		proc.done <- cmd.Wait()
		close(proc.done)
	}()
	return proc, nil
}

func (p *VectorProcess) Stdin() io.Writer {
	if p == nil {
		return nil
	}
	return p.stdin
}

func (p *VectorProcess) Stop() error {
	if p == nil {
		return nil
	}
	_ = p.stdin.Close()

	select {
	case err := <-p.done:
		return err
	case <-time.After(2 * time.Second):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		return <-p.done
	}
}

func writeVectorConfig(vectorTOMLTemplate *template.Template, logPushURL string) error {
	if vectorTOMLTemplate == nil {
		return errors.New("vector template is nil")
	}

	var output bytes.Buffer
	if err := vectorTOMLTemplate.Execute(&output, struct{ URL string }{URL: logPushURL}); err != nil {
		return fmt.Errorf("render vector template failed: %w", err)
	}
	if err := os.WriteFile(vectorCfg, output.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s failed: %w", vectorCfg, err)
	}
	return nil
}

func checkLogPushURLReachable(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("invalid URL host in %q", rawURL)
	}

	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build HEAD request failed: %w", err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HEAD request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusMethodNotAllowed {
		return fmt.Errorf("HEAD %s returned status %d", rawURL, resp.StatusCode)
	}
	return nil
}
