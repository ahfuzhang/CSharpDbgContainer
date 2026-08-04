<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"/>
<title>Dotnet Debug Container All-In-One</title>
<style>
	:root{
		--fg:#1f2937;
		--muted:#6b7280;
		--border:#d1d5db;
		--card-border:#e5e7eb;
	}
	*{box-sizing:border-box;}
	body{
		margin:0;
		padding:24px;
		background:#f3f4f6;
		color:var(--fg);
		font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;
	}
	h1{
		margin:0 0 20px 0;
		font-size:24px;
	}
	h1 .sub{
		display:block;
		margin-top:4px;
		font-size:13px;
		font-weight:400;
		color:var(--muted);
		font-family:Consolas,Monaco,monospace;
	}
	section{
		border:1px solid var(--card-border);
		border-radius:8px;
		padding:16px 20px;
		margin-bottom:20px;
	}
	section > h2{
		margin:0 0 12px 0;
		font-size:15px;
		text-transform:uppercase;
		letter-spacing:.04em;
		color:var(--muted);
	}
	.section-overview{background:#eff6ff;}
	.section-links{background:#f0fdf4;}
	.section-processes{background:#fefce8;}
	.section-history{background:#fdf2f8;}
	a{color:#2563eb;}
	.links a{
		display:inline-block;
		margin:2px 12px 2px 0;
	}
	.trace-form{margin-top:10px;}
	input[type=text]{
		font-family:Consolas,Monaco,monospace;
		padding:2px 4px;
	}
	input[type=button]{
		padding:3px 10px;
		cursor:pointer;
	}
	table{
		border-collapse:collapse;
		font-family:Consolas,Monaco,monospace;
		font-size:12px;
		width:100%;
		background:#fff;
	}
	table caption{
		caption-side:top;
	}
	th,td{
		border:1px solid var(--border);
		padding:6px;
		text-align:left;
		vertical-align:top;
	}
	th{
		background:#f9fafb;
	}
	.empty{
		color:var(--muted);
		font-style:italic;
	}
</style>
</head>
<body>

<h1>Dotnet Debug Container All-In-One
<span class="sub">target={{.TargetLabel}} pid={{.PID}}</span>
</h1>

<section class="section-links">
<h2>Quick Links</h2>
<div class="links">
<a href="/log" target="_blank">show log</a>
<a href="/stack" target="_blank">show stack</a>
<a href="/profile_list" target="_blank">show cpuprofile list</a>
{{if .ShowCurrentGDBLog}}<a href="/current-gdb-log" target="_blank">Current Gdb Log</a>{{end}}
</div>
<div class="trace-form">
Trace <input type="text" size=4 value=10 id="seconds"/> seconds, then <input type="button" value="Show CPU Profile" onclick="profile()"/>
</div>
<script>
function profile(){
	var textbox = document.getElementById("seconds");
	window.open("/trace?seconds=" + textbox.value, "about:blank");
}
</script>
</section>

<section class="section-processes">
<h2>Container Processes</h2>
<table>
<tr><th>PID</th><th>Uptime</th><th>Memory</th><th>Thread Count</th><th>Cmdline</th><th>Actions</th></tr>
{{range .Processes}}<tr{{if .IsTarget}} style="background-color:#fde68a;"{{end}}><td>{{.PID}}</td><td>{{.Uptime}}</td><td>{{.Memory}}</td><td>{{.ThreadCount}}</td><td>{{.Cmdline}}</td><td>
{{if .IsTarget}}
{{if $.WithCoverage}}
  <input type="button" value="Show Code Coverage" onclick="window.open('/code_coverage/', '_blank')"/>
  <input type="button" value="Reset Coverage Data" onclick="resetCoverageData()"/>
{{end}}
{{end}}</td></tr>
{{end}}</table>
</section>

<section class="section-history">
<h2>Run History</h2>
{{if .RunHistory}}<table>
<tr><th>#</th><th>PID</th><th>Start</th><th>End</th><th>Duration</th><th>Exit</th><th>CoreDump</th><th>GDB Log</th><th>Last Logs</th></tr>
{{range .RunHistory}}<tr><td>{{.Index}}</td><td>{{.PID}}</td><td>{{.Start}}</td><td>{{.End}}</td><td>{{.Duration}}</td><td>{{if .Abnormal}}<span style="color:#b91c1c;font-weight:700;">code={{.ExitCode}}{{if .Signal}} signal={{.Signal}}{{end}} (abnormal)</span>{{else}}<span style="color:#166534;">code={{.ExitCode}}{{if .Signal}} signal={{.Signal}}{{end}} (normal)</span>{{end}}{{if .ErrMsg}}<br/><span style="color:#b91c1c;">{{.ErrMsg}}</span>{{end}}</td><td>{{if .CoreDumpPath}}{{.CoreDumpPath}}{{else}}-{{end}}</td><td>{{if .GDBLogPath}}<a href="/gdb-log?index={{.GDBLogIndex}}" target="_blank">{{.GDBLogPath}}</a>{{else}}-{{end}}</td><td>{{if .LastLogs}}<pre style="margin:0;white-space:pre-wrap;max-height:160px;overflow:auto;">{{.LastLogs}}</pre>{{else}}-{{end}}</td></tr>
{{end}}</table>{{else}}<div class="empty">no exit records yet</div>{{end}}
</section>

<script>
function resetCoverageData(){
	fetch("/reset_coverage_data", {method: "POST"}).then(function(resp){
		if(!resp.ok){
			return resp.text().then(function(text){
				throw new Error(text || ("HTTP " + resp.status));
			});
		}
		alert("Effective in 10s");
	}).catch(function(err){
		alert("Reset coverage data failed: " + err.message);
	});
}
</script>

</body>
</html>
