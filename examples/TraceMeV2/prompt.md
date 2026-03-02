
# 目标

实现一个使用微软的 trace 库实现 cpu profling 的例子程序。

同一进程内用 Microsoft.Diagnostics.NETCore.Client 启动 EventPipe CPU 采样，把 trace 落成 .nettrace，再用 TraceEvent 把它转换成 speedscope.json（可直接丢进 speedscope.app）。

# 依赖的库

依赖的 NuGet：
	•	Microsoft.Diagnostics.NETCore.Client（DiagnosticsClient / EventPipe） ￼
	•	Microsoft.Diagnostics.Tracing.TraceEvent（TraceLog / StackSource / SpeedScopeStackSourceWriter） 

# 例子代码

```csharp
// <Project Sdk="Microsoft.NET.Sdk">
//   <PropertyGroup>
//     <OutputType>Exe</OutputType>
//     <TargetFramework>net8.0</TargetFramework>
//     <ImplicitUsings>enable</ImplicitUsings>
//     <Nullable>enable</Nullable>
//   </PropertyGroup>
//   <ItemGroup>
//     <PackageReference Include="Microsoft.Diagnostics.NETCore.Client" Version="*" />
//     <PackageReference Include="Microsoft.Diagnostics.Tracing.TraceEvent" Version="*" />
//   </ItemGroup>
// </Project>

using Microsoft.Diagnostics.NETCore.Client;
using Microsoft.Diagnostics.Tracing;
using Microsoft.Diagnostics.Tracing.Parsers;
using Microsoft.Diagnostics.Tracing.Etlx;
using Microsoft.Diagnostics.Tracing.Stacks;
using Microsoft.Diagnostics.Tracing.Stacks.Formats;
using Microsoft.Diagnostics.Tracing.Symbols;
using System.Diagnostics;
using System.Diagnostics.Tracing;

static class Program
{
    static async Task<int> Main(string[] args)
    {
        int seconds = args.Length > 0 && int.TryParse(args[0], out var s) ? s : 5;
        int pid = Environment.ProcessId;

        string nettrace = Path.GetFullPath($"self_{DateTime.UtcNow:yyyyMMdd_HHmmss}.nettrace");
        string etlx = nettrace + ".etlx";
        string speedscope = Path.ChangeExtension(nettrace, ".speedscope.json");

        Console.WriteLine($"PID: {pid}");
        Console.WriteLine($"Collecting {seconds}s -> {nettrace}");

        // 1) 采集：EventPipe CPU trace（相当于 dotnet-trace 的底层做法） [oai_citation:2‡Microsoft Learn](https://learn.microsoft.com/en-us/dotnet/core/diagnostics/diagnostics-client-library)
        await CollectCpuNettraceAsync(pid, TimeSpan.FromSeconds(seconds), nettrace);

        // 2) 转换：nettrace -> speedscope.json（PerfView/TraceEvent 的导出器） [oai_citation:3‡GitHub](https://github.com/microsoft/perfview/issues/862?utm_source=chatgpt.com)
        ConvertNettraceToSpeedscope(nettrace, etlx, speedscope, pid);

        Console.WriteLine($"Done.");
        Console.WriteLine($"Speedscope: {speedscope}");
        return 0;
    }

    static async Task CollectCpuNettraceAsync(int pid, TimeSpan duration, string outFile)
    {
        // 这组 provider 基本等价于常见的“CPU 采样 + runtime 默认事件”组合 [oai_citation:4‡Microsoft Learn](https://learn.microsoft.com/en-us/dotnet/core/diagnostics/diagnostics-client-library)
        var providers = new List<EventPipeProvider>
        {
            new("Microsoft-Windows-DotNETRuntime",
                EventLevel.Informational,
                (long)ClrTraceEventParser.Keywords.Default),

            new("Microsoft-DotNETCore-SampleProfiler",
                EventLevel.Informational,
                (long)ClrTraceEventParser.Keywords.None)
        };

        var client = new DiagnosticsClient(pid);
        using var session = client.StartEventPipeSession(providers, requestRundown: true);

        // 让进程在采样期间有点 CPU 活干（避免空 profile）
        using var cts = new CancellationTokenSource(duration);
        var burnTask = Task.Run(() => BurnCpu(cts.Token));

        await using (var fs = new FileStream(outFile, FileMode.Create, FileAccess.Write, FileShare.Read))
        {
            var copyTask = session.EventStream.CopyToAsync(fs, cts.Token);
            try { await Task.Delay(duration, cts.Token); } catch { /* ignore */ }
            session.Stop(); // 停止后 CopyToAsync 会结束
            try { await copyTask; } catch { /* ignore */ }
        }

        try { await burnTask; } catch { /* ignore */ }
    }

    static void ConvertNettraceToSpeedscope(string nettrace, string etlx, string speedscope, int pid)
    {
        var options = new TraceLogOptions
        {
            ConversionLog = TextWriter.Null,
            // Demo 里为了简单/稳定：不强制解析符号（有 PDB/符号服务器再开也行）
            ShouldResolveSymbols = _ => false
        };

        using var traceLog = new TraceLog(TraceLog.CreateFromEventPipeDataFile(nettrace, etlx, options));

        // 符号读取器：不解析也可以传一个（ThreadTimeStackComputer 需要） [oai_citation:5‡GitHub](https://github.com/microsoft/perfview/issues/862?utm_source=chatgpt.com)
        var symbolReader = new SymbolReader(TextWriter.Null)
        {
            SymbolPath = SymbolPath.MicrosoftSymbolServerPath,
            SecurityCheck = _ => true
        };

        var stackSource = new MutableTraceEventStackSource(traceLog);
        var computer = new ThreadTimeStackComputer(traceLog, symbolReader);

        // 只导出本进程（“自采集”场景通常只有一个进程，但过滤一下更稳）
        computer.GenerateThreadTimeStacks(
            stackSource,
            traceLog.Events.Filter(e => e.ProcessID == pid)
        );

        SpeedScopeStackSourceWriter.WriteStackViewAsJson(stackSource, speedscope); //  [oai_citation:6‡GitHub](https://raw.githubusercontent.com/microsoft/perfview/main/src/TraceEvent/Stacks/SpeedScopeStackSourceWriter.cs)
    }

    static void BurnCpu(CancellationToken token)
    {
        // 简单制造一些可采样的栈；你可以替换成真实业务
        long x = 0;
        while (!token.IsCancellationRequested)
        {
            x += Fib(20);
        }
        GC.KeepAlive(x);

        static int Fib(int n) => n <= 1 ? n : Fib(n - 1) + Fib(n - 2);
    }
}
```

# 步骤

* 创建 TraceMeV2.csproj 文件
  - 使用如下语法，嵌入 speedscope 文件夹

```xml
<ItemGroup>
  <EmbeddedResource Include="../../build/speedscope/**/*" />
</ItemGroup>
```

* 创建 Program.cs
* 创建一个 http1 的 kestrel 服务器
  - 路径 `/echo`: 实现一个 echo 服务器，便于测试
  - 路径 `/traceme?seconds=10`:
    - 请求这个路径时
      - 创建新的 os thread，在新线程中采集 trace 信息
      - 采集 seconds 对应的秒数后，转换为 Speedscope 格式
      - 把文件写入 /tmp/${YYYYMMDDHHMiSS_fff}.speedscope.json
      - 跳转到 /speedscope/index.html#profileURL=/profile/${YYYYMMDDHHMiSS_fff}.speedscope.json
    - $pid 是当前进程的 pid
    - $seconds 是 querystring 中 seconds 参数的值。默认值为 10， 最小值为 1，最大值为 30。
    - ${YYYYMMDDHHMiSS.mmm} 是当前时间
    - 保持长连接，在 web ui 中显示 trace 的倒计时信息
  - 路径 `/speedscope/`: 把 embed 的 assets 输出到浏览器端.
    - 参考以下代码:

```csharp
using Microsoft.Extensions.FileProviders;

var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();

// 你的程序集（通常就是当前入口程序集）
var assembly = typeof(Program).Assembly;

// 重要：这里的 baseNamespace 要和资源名的前缀匹配
// 一般是：<RootNamespace>.Assets
var embeddedProvider = new ManifestEmbeddedFileProvider(assembly, "Assets");

app.UseStaticFiles(new StaticFileOptions
{
    FileProvider = embeddedProvider,
    RequestPath = "/assets"
});

app.MapGet("/", () => "ok");

app.Run();
```

  - 路径 `/profile/${name}.json`: 访问由 trace 生成到 `/tmp/${YYYYMMDDHHMiSS_fff}.speedscope.json` 中的文件。最终，可以在浏览器中看到火焰图。


* 生成 Makefile 文件，提供 build, run 命令
  - 生成代码后执行 make build，确保可以正常编译。
  
