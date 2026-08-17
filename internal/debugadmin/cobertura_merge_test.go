package debugadmin

import (
	"encoding/xml"
	"strings"
	"testing"
)

const sampleCoberturaXML = `<?xml version="1.0" encoding="utf-8"?>
<coverage line-rate="0.5" branch-rate="1" complexity="2" version="1.9" timestamp="1" lines-covered="2" lines-valid="4">
  <packages>
    <package name="Demo" line-rate="0.5" branch-rate="1" complexity="2">
      <classes>
        <class name="Demo.BetFlow&lt;TRequest, TResponse&gt;" filename="/src/BetFlow.cs" line-rate="0" branch-rate="1" complexity="1">
          <methods>
            <method name="SetRequest" signature="()" line-rate="0" branch-rate="1" complexity="1">
              <lines>
                <line number="10" hits="0" branch="False" />
              </lines>
            </method>
          </methods>
          <lines>
            <line number="10" hits="0" branch="False" />
          </lines>
        </class>
        <class name="Demo.BetFlow.&lt;TriggerAsync&gt;d__5&lt;TRequest, TResponse&gt;" filename="/src/BetFlow.cs" line-rate="1" branch-rate="1" complexity="1">
          <methods>
            <method name="MoveNext" signature="()" line-rate="1" branch-rate="1" complexity="1">
              <lines>
                <line number="20" hits="1" branch="False" />
                <line number="21" hits="1" branch="False" />
              </lines>
            </method>
          </methods>
          <lines>
            <line number="20" hits="1" branch="False" />
            <line number="21" hits="1" branch="False" />
          </lines>
        </class>
      </classes>
    </package>
  </packages>
</coverage>`

func TestMergeCompilerGeneratedClassesIntoParents(t *testing.T) {
	cleaned, err := mergeCompilerGeneratedClassesIntoParents([]byte(sampleCoberturaXML))
	if err != nil {
		t.Fatalf("mergeCompilerGeneratedClassesIntoParents failed: %v", err)
	}

	var doc coberturaDoc
	if err := xml.Unmarshal(cleaned, &doc); err != nil {
		t.Fatalf("output is not valid xml: %v", err)
	}

	if len(doc.Packages.Package) != 1 {
		t.Fatalf("expected 1 package, got %d", len(doc.Packages.Package))
	}
	classes := doc.Packages.Package[0].Classes.Class
	if len(classes) != 1 {
		t.Fatalf("expected the state machine class to be merged away, got %d classes", len(classes))
	}

	parent := classes[0]
	if parent.Name != "Demo.BetFlow<TRequest, TResponse>" {
		t.Fatalf("unexpected parent class name: %q", parent.Name)
	}

	gotLines := coberturaLinesToMap(parent.Lines)
	wantLines := map[int]int{10: 0, 20: 1, 21: 1}
	for n, h := range wantLines {
		if gotLines[n] != h {
			t.Errorf("line %d: got hits=%d, want %d", n, gotLines[n], h)
		}
	}

	var triggerAsync *coberturaMethod
	for _, m := range parent.Methods.Method {
		if m.Name == "TriggerAsync" {
			triggerAsync = m
		}
	}
	if triggerAsync == nil {
		t.Fatalf("TriggerAsync method not merged into parent's method table")
	}
	if triggerAsync.LineRate != "1" {
		t.Errorf("TriggerAsync line-rate = %q, want %q", triggerAsync.LineRate, "1")
	}

	if parent.LineRate != "0.6666666666666666" {
		t.Errorf("recomputed parent line-rate = %q, want %q", parent.LineRate, "0.6666666666666666")
	}

	if strings.Contains(string(cleaned), "TriggerAsync&gt;d__5") {
		t.Errorf("state machine class should have been removed from output")
	}
}

func TestParseGeneratedClassName(t *testing.T) {
	tests := []struct {
		name       string
		wantParent string
		wantMethod string
		wantOK     bool
	}{
		{"Demo.BetFlow.<TriggerAsync>d__5<TRequest, TResponse>", "Demo.BetFlow<TRequest, TResponse>", "TriggerAsync", true},
		{"Demo.Foo.<>c", "Demo.Foo", "", true},
		{"Demo.Foo.<>c__DisplayClass1_0", "Demo.Foo", "", true},
		{"Demo.Foo.<>c__DisplayClass1_0<T>", "Demo.Foo<T>", "", true},
		{"Demo.BetFlow<TRequest, TResponse>", "", "", false},
		{"Demo.SampleBetFlow", "", "", false},
	}
	for _, tt := range tests {
		parent, method, ok := parseGeneratedClassName(tt.name)
		if ok != tt.wantOK || parent != tt.wantParent || method != tt.wantMethod {
			t.Errorf("parseGeneratedClassName(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.name, parent, method, ok, tt.wantParent, tt.wantMethod, tt.wantOK)
		}
	}
}
