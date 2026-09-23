package debugadmin

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestPdbPathForDLL(t *testing.T) {
	tests := []struct {
		dllPath string
		want    string
	}{
		{"/app/Workflow.dll", "/app/Workflow.pdb"},
		{"./build/linux/amd64/Workflow.dll", "./build/linux/amd64/Workflow.pdb"},
		{"Workflow.DLL", "Workflow.pdb"},
	}
	for _, tt := range tests {
		if got := pdbPathForDLL(tt.dllPath); got != tt.want {
			t.Errorf("pdbPathForDLL(%q) = %q, want %q", tt.dllPath, got, tt.want)
		}
	}
}

func TestBuildIlspycmdGeneratePDBCommand(t *testing.T) {
	cmd := buildIlspycmdGeneratePDBCommand("./build/linux/amd64/Workflow.dll")
	want := []string{
		"ilspycmd",
		"--generate-pdb",
		"--disable-updatecheck",
		"--referencepath", "build/linux/amd64",
		"./build/linux/amd64/Workflow.dll",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("buildIlspycmdGeneratePDBCommand() args = %q, want %q", cmd.Args, want)
	}
}

func TestFindDLLFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(rel string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", path, err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %q: %v", path, err)
		}
	}
	writeFile("App.dll")
	writeFile("App.pdb")
	writeFile("nested/Nested.DLL")
	writeFile("notes.txt")

	got, err := findDLLFiles(dir)
	if err != nil {
		t.Fatalf("findDLLFiles() error = %v", err)
	}
	sort.Strings(got)
	want := []string{
		filepath.Join(dir, "App.dll"),
		filepath.Join(dir, "nested/Nested.DLL"),
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findDLLFiles() = %q, want %q", got, want)
	}
}

func TestModuleFilterMatches(t *testing.T) {
	tests := []struct {
		name    string
		filter  *moduleFilter
		dllPath string
		want    bool
	}{
		{"nil filter matches everything", nil, "/app/Anything.dll", true},
		{"empty filter matches everything", &moduleFilter{}, "/app/Anything.dll", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.matches(tt.dllPath); got != tt.want {
				t.Errorf("matches(%q) = %t, want %t", tt.dllPath, got, tt.want)
			}
		})
	}
}

func TestLoadModuleFilterFromXML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.xml")
	content := `<?xml version="1.0" encoding="utf-8"?>
<Configuration>
  <CodeCoverage>
    <ModulePaths>
      <Include>
        <ModulePath>.*/MyProj\..*\.dll$</ModulePath>
      </Include>
      <Exclude>
        <ModulePath>.*Grpc\.Protobuf\.dll$</ModulePath>
      </Exclude>
    </ModulePaths>
  </CodeCoverage>
</Configuration>
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write settings xml: %v", err)
	}

	filter, err := loadModuleFilterFromXML(path)
	if err != nil {
		t.Fatalf("loadModuleFilterFromXML() error = %v", err)
	}

	tests := []struct {
		dllPath string
		want    bool
	}{
		{"/app/MyProj.Api.dll", true},
		{"/app/MyProj.Grpc.Protobuf.dll", false}, // excluded even though it matches Include
		{"/app/Other.dll", false},                // doesn't match Include
	}
	for _, tt := range tests {
		if got := filter.matches(tt.dllPath); got != tt.want {
			t.Errorf("matches(%q) = %t, want %t", tt.dllPath, got, tt.want)
		}
	}
}

func TestLoadModuleFilterFromXMLInvalidRegexp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.xml")
	content := `<Configuration><CodeCoverage><ModulePaths><Include><ModulePath>(</ModulePath></Include></ModulePaths></CodeCoverage></Configuration>`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write settings xml: %v", err)
	}
	if _, err := loadModuleFilterFromXML(path); err == nil {
		t.Fatal("loadModuleFilterFromXML() error = nil, want error for invalid regexp")
	}
}

func TestGeneratePDBFromDLLsSkipsExistingPDB(t *testing.T) {
	dir := t.TempDir()
	dllPath := filepath.Join(dir, "App.dll")
	pdbPath := filepath.Join(dir, "App.pdb")
	if err := os.WriteFile(dllPath, []byte("dll"), 0o644); err != nil {
		t.Fatalf("write dll: %v", err)
	}
	if err := os.WriteFile(pdbPath, []byte("pdb"), 0o644); err != nil {
		t.Fatalf("write pdb: %v", err)
	}

	// ilspycmd 未必安装在测试环境中；由于唯一的 dll 已经有对应的 pdb，
	// GeneratePDBFromDLLs 应当在不调用 ilspycmd 的情况下直接返回成功。
	if err := GeneratePDBFromDLLs(dir, ""); err != nil {
		t.Fatalf("GeneratePDBFromDLLs() error = %v", err)
	}
}

func TestGeneratePDBFromDLLsInvalidRunDir(t *testing.T) {
	if err := GeneratePDBFromDLLs(filepath.Join(t.TempDir(), "missing"), ""); err == nil {
		t.Fatal("GeneratePDBFromDLLs() error = nil, want error for missing run dir")
	}
}

func TestLoadOptionsWithGeneratePDBFromDLL(t *testing.T) {
	opts, err := loadOptions([]string{"-generate.pdb.from.dll", "--", "app.dll"})
	if err != nil {
		t.Fatalf("loadOptions() error = %v", err)
	}
	if !opts.GeneratePDBFromDLL {
		t.Error("loadOptions() GeneratePDBFromDLL = false, want true")
	}
}
