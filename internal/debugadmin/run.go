package debugadmin

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultPort = 8089
	configPath  = "init.config.yaml"
)

func Run(staticFS fs.FS) int {
	options, err := loadOptions(configPath, os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "parse options failed: %v\n", err)
		return 2
	}
	broker := NewLogBroker()
	target, err := StartTarget(options.Startup, broker)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "start target process failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "target process started, pid=%d\n", target.PID())

	server, err := NewHTTPServer(options, staticFS, broker, target)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create http server failed: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "DebugAdmin listening on http://127.0.0.1:%d\n", options.AdminPort)
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.ListenAndServe()
	}()

	select {
	case targetErr := <-target.Done():
		_, _ = fmt.Fprintf(os.Stdout, "target process finished, DebugAdmin will exit, err=%v\n", targetErr)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		<-serverErrCh
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

func loadOptions(configPath string, args []string) (Options, error) {
	// cfg, err := LoadConfigFile(configPath)
	// if err != nil {
	// 	return Options{}, err
	// }
	cfg := &FileConfig{}
	port := defaultPort
	if cfg.AdminPort != 0 {
		port = cfg.AdminPort
	}
	startup := cfg.Startup

	flagSet := flag.NewFlagSet("DebugAdmin", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	flagSet.IntVar(&port, "admin.port", port, "http service listen port")
	flagSet.StringVar(&startup, "startup", startup, "startup dll or executable")
	if err := flagSet.Parse(args); err != nil {
		return Options{}, err
	}
	if port < 1 || port > 65535 {
		return Options{}, fmt.Errorf("admin.port should be between 1 and 65535, got %d", port)
	}
	startup = strings.TrimSpace(startup)
	if startup == "" {
		return Options{}, errors.New("startup is required; use -startup=xxx.dll or set startup in init.config.yaml")
	}
	return Options{
		AdminPort: port,
		Startup:   startup,
	}, nil
}
