package debugadmin

import (
	"compress/gzip"
	"io"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildStartupCommand(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "dll",
			opts: Options{StartupParams: []string{"app.dll", "-param1=value1", "param2=value2"}},
			want: []string{"dotnet", "app.dll", "-param1=value1", "param2=value2"},
		},
		{
			name: "dll with gdb",
			opts: Options{WithGDB: true, StartupParams: []string{"app.dll", "-param1=value1", "param2=value2"}},
			want: []string{"gdb", "--args", "dotnet", "app.dll", "-param1=value1", "param2=value2"},
		},
		{
			name: "executable with gdb",
			opts: Options{WithGDB: true, StartupParams: []string{"./app", "--port", "8080"}},
			want: []string{"gdb", "--args", "./app", "--port", "8080"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := BuildStartupCommand(&tt.opts)
			if err != nil {
				t.Fatalf("BuildStartupCommand() error = %v", err)
			}
			if !reflect.DeepEqual(cmd.Args, tt.want) {
				t.Errorf("BuildStartupCommand() args = %q, want %q", cmd.Args, tt.want)
			}
		})
	}
}

func TestLoadOptionsWithGDB(t *testing.T) {
	opts, err := loadOptions([]string{"-with.gdb", "--", "app.dll", "-param1=value1"})
	if err != nil {
		t.Fatalf("loadOptions() error = %v", err)
	}
	if !opts.WithGDB {
		t.Error("loadOptions() WithGDB = false, want true")
	}
	if want := []string{"app.dll", "-param1=value1"}; !reflect.DeepEqual(opts.StartupParams, want) {
		t.Errorf("loadOptions() StartupParams = %q, want %q", opts.StartupParams, want)
	}
}

func TestWriteGDBCommandScript(t *testing.T) {
	now := time.Date(2026, time.July, 21, 12, 34, 56, 0, time.UTC)
	scriptPath, logPath, err := WriteGDBCommandScript(now)
	if err != nil {
		t.Fatalf("WriteGDBCommandScript() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(scriptPath)
	})
	if want := "/tmp/20260721-123456.log"; logPath != want {
		t.Errorf("WriteGDBCommandScript() log path = %q, want %q", logPath, want)
	}
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read command script: %v", err)
	}
	script := string(data)
	for _, command := range []string{
		"set logging file " + logPath,
		"set logging on",
		"handle SIGSEGV stop print pass",
		"run",
		"bt 10",
		"quit 128",
	} {
		if !strings.Contains(script, command) {
			t.Errorf("command script does not contain %q", command)
		}
	}
	if strings.Index(script, "set logging on") > strings.Index(script, "run") {
		t.Error("gdb logging should be configured before run")
	}
}

func TestBuildGDBStartupCommand(t *testing.T) {
	options := &Options{WithGDB: true, StartupParams: []string{"app.dll", "-param1=value1"}}
	cmd, err := BuildGDBStartupCommand(options, "/tmp/debugadmin-gdb.gdb")
	if err != nil {
		t.Fatalf("BuildGDBStartupCommand() error = %v", err)
	}
	want := []string{"gdb", "-q", "-x", "/tmp/debugadmin-gdb.gdb", "--args", "dotnet", "app.dll", "-param1=value1"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("BuildGDBStartupCommand() args = %q, want %q", cmd.Args, want)
	}
}

func TestHandleGDBLog(t *testing.T) {
	logPath := "/tmp/20260721-123456.log"
	if err := os.WriteFile(logPath, []byte("crash backtrace"), 0o600); err != nil {
		t.Fatalf("write gdb log: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(logPath)
	})
	handler := &AdminHandler{history: &RunHistory{records: []RunRecord{{GDBLogPath: logPath}}}}
	request := httptest.NewRequest("GET", "/gdb-log?index=0", nil)
	response := httptest.NewRecorder()
	handler.handleGDBLog(response, request)
	if response.Code != 200 {
		t.Fatalf("handleGDBLog() status = %d, want 200", response.Code)
	}
	if got := response.Body.String(); got != "crash backtrace" {
		t.Errorf("handleGDBLog() body = %q", got)
	}
}

func TestHandleGDBLogGzip(t *testing.T) {
	logPath := "/tmp/20260721-123457.log"
	if err := os.WriteFile(logPath, []byte("crash backtrace"), 0o600); err != nil {
		t.Fatalf("write gdb log: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(logPath)
	})
	handler := &AdminHandler{history: &RunHistory{records: []RunRecord{{GDBLogPath: logPath}}}}
	request := httptest.NewRequest("GET", "/gdb-log?index=0", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.handleGDBLog(response, request)
	if got := response.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	reader, err := gzip.NewReader(response.Result().Body)
	if err != nil {
		t.Fatalf("create gzip reader: %v", err)
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip response: %v", err)
	}
	if string(decompressed) != "crash backtrace" {
		t.Errorf("gzip response = %q", decompressed)
	}
}

func TestHandleCurrentGDBLog(t *testing.T) {
	logPath := "/tmp/20260721-123458.log"
	if err := os.WriteFile(logPath, []byte("current crash backtrace"), 0o600); err != nil {
		t.Fatalf("write gdb log: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(logPath)
	})
	handler := &AdminHandler{}
	handler.target.Store(&TargetProcess{gdbLogPath: logPath})
	request := httptest.NewRequest("GET", "/current-gdb-log", nil)
	response := httptest.NewRecorder()
	handler.handleCurrentGDBLog(response, request)
	if response.Code != 200 {
		t.Fatalf("handleCurrentGDBLog() status = %d, want 200", response.Code)
	}
	if got := response.Body.String(); got != "current crash backtrace" {
		t.Errorf("handleCurrentGDBLog() body = %q", got)
	}
}

func TestHandleRootShowsCurrentGDBLogOnlyForGDBTarget(t *testing.T) {
	tests := []struct {
		name       string
		gdbLogPath string
		wantLink   bool
	}{
		{name: "with gdb", gdbLogPath: "/tmp/20260721-123459.log", wantLink: true},
		{name: "without gdb", wantLink: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &AdminHandler{history: NewRunHistory()}
			handler.target.Store(&TargetProcess{pid: 123, gdbLogPath: tt.gdbLogPath})
			response := httptest.NewRecorder()
			handler.handleRoot(response, httptest.NewRequest("GET", "/", nil))
			gotLink := strings.Contains(response.Body.String(), `href="/current-gdb-log"`)
			if gotLink != tt.wantLink {
				t.Errorf("Current Gdb Log link shown = %t, want %t", gotLink, tt.wantLink)
			}
		})
	}
}
