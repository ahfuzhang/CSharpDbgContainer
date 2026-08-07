<!doctype html>
<html>
<head>
<meta charset="utf-8"/>
<title>Stack</title>
<style>
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
</style>
</head>
<body>
<div class="wrap">
<h1>Stack Dump</h1>
{{if .StartupOutput}}<h2>netcoredbg stdout (before "bt all")</h2><pre class="startup">{{.StartupOutput}}</pre>{{end}}
{{if .NoData}}<div class="misc">No stack data returned.</div>{{end}}
{{range .Threads}}<div class="thread"><div class="thread-title">{{.Header}}</div>{{if or .Frames .Extra}}<div class="thread-stack">{{range .Frames}}<div class="frame">{{.}}</div>
{{end}}{{range .Extra}}<div class="thread-extra">{{.}}</div>
{{end}}</div>{{end}}</div>
{{end}}
{{if .Misc}}<h2>Other Output</h2><pre class="misc">{{.Misc}}</pre>{{end}}
{{if .StderrOutput}}<h2>netcoredbg stderr</h2><pre class="stderr">{{.StderrOutput}}</pre>{{end}}
{{if .Error}}<div class="error">stack command error: {{.Error}}</div>{{end}}
</div>
</body>
</html>
