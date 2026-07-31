<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"/>
<title>Threads of PID {{.PID}}</title>
<style>
*{box-sizing:border-box;}
html,body{height:100%;margin:0;}
body{
	font-family:Consolas,Monaco,monospace;
	color:#1f2937;
	background:#f3f4f6;
}
.layout{display:flex;height:100vh;}
.thread-list{
	width:20%;
	min-width:220px;
	background:#fef9c3;
	border-right:1px solid #d1d5db;
	overflow-y:auto;
	padding:12px;
}
.thread-list h2{
	font-size:13px;
	text-transform:uppercase;
	letter-spacing:.04em;
	color:#6b7280;
	margin:0 0 10px 0;
}
.thread-item{
	padding:8px 10px;
	margin-bottom:4px;
	border-radius:6px;
	cursor:pointer;
	font-size:12px;
	white-space:pre-wrap;
	word-break:break-all;
}
.thread-item:hover{background:#fde68a;}
.thread-item.active{background:#f59e0b;color:#fff;font-weight:700;}
.stack-view{
	width:80%;
	padding:20px;
	overflow-y:auto;
	background:#ffffff;
}
.stack-view h1{font-size:18px;margin:0 0 12px 0;}
.stack-pre{
	white-space:pre-wrap;
	font-size:12px;
	line-height:1.4;
	background:#f9fafb;
	border:1px solid #e5e7eb;
	border-radius:8px;
	padding:12px;
}
.placeholder{color:#6b7280;font-style:italic;}
.misc,.stderr,.error{
	margin-top:16px;
	white-space:pre-wrap;
	padding:10px;
	border-radius:8px;
	font-size:12px;
}
.misc{background:#eef2ff;border:1px solid #c7d2fe;}
.stderr{background:#fff7ed;border:1px solid #fed7aa;color:#7c2d12;}
.error{background:#fee2e2;border:1px solid #fecaca;color:#991b1b;}
</style>
</head>
<body>
<div class="layout">

<div class="thread-list">
<h2>Threads (pid={{.PID}})</h2>
{{if .Threads}}{{range .Threads}}<div class="thread-item" data-target="stack-{{.ID}}" onclick="showStack({{.ID}})">{{.Label}}</div>
{{end}}{{else}}<div class="placeholder">no threads found</div>{{end}}
</div>

<div class="stack-view">
<h1>Stack Trace</h1>
{{if .Threads}}{{range .Threads}}<pre class="stack-pre" id="stack-{{.ID}}" style="display:none;">{{.Stack}}</pre>
{{end}}{{else}}<div class="placeholder">No thread data available.</div>{{end}}
{{if .Misc}}<div class="misc">{{.Misc}}</div>{{end}}
{{if .Stderr}}<div class="stderr">{{.Stderr}}</div>{{end}}
{{if .Error}}<div class="error">gdb error: {{.Error}}</div>{{end}}
</div>

</div>
<script>
function showStack(id){
	var items = document.querySelectorAll(".thread-item");
	for (var i = 0; i < items.length; i++){ items[i].classList.remove("active"); }
	var panes = document.querySelectorAll(".stack-pre");
	for (var i = 0; i < panes.length; i++){ panes[i].style.display = "none"; }
	var target = document.getElementById("stack-" + id);
	if (target){ target.style.display = "block"; }
	var clicked = document.querySelector('[data-target="stack-' + id + '"]');
	if (clicked){ clicked.classList.add("active"); }
}
window.addEventListener("DOMContentLoaded", function(){
	var first = document.querySelector(".thread-item");
	if (first){ first.click(); }
});
</script>
</body>
</html>
