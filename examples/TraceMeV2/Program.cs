using System.Collections.Concurrent;
using System.Diagnostics;
using System.Diagnostics.Tracing;
using System.Text.Encodings.Web;
using Microsoft.AspNetCore.Server.Kestrel.Core;
using Microsoft.Diagnostics.NETCore.Client;
using Microsoft.Diagnostics.Tracing;
using Microsoft.Diagnostics.Tracing.Etlx;
using Microsoft.Diagnostics.Tracing.Parsers;
using Microsoft.Diagnostics.Tracing.Stacks;
using Microsoft.Diagnostics.Tracing.Stacks.Formats;
using Microsoft.Diagnostics.Symbols;
using Microsoft.Extensions.FileProviders;

public class Program
{
    //private const string TraceOutputDir = "../../build/examples/TraceMeV2/";
    private const string TraceOutputDir = "/tmp/";

    public static void Main(string[] args)
    {
        var startupOptions = ParseStartupOptions(args);
        if (startupOptions.Cores is int cores)
        {
            ConfigureThreadPoolMaxThreads(cores);
        }

        var builder = WebApplication.CreateBuilder(startupOptions.ForwardedArgs);
        builder.Logging.SetMinimumLevel(LogLevel.Warning);
        builder.WebHost.UseUrls($"http://0.0.0.0:{startupOptions.Port}");
        builder.WebHost.ConfigureKestrel(options =>
        {
            options.ConfigureEndpointDefaults(endpoint => endpoint.Protocols = HttpProtocols.Http1);
        });

        var app = builder.Build();
        var generatedProfiles = new ConcurrentDictionary<string, string>(StringComparer.Ordinal);

        ConfigureSpeedscope(app);
        MapEcho(app);
        MapTraceMe(app, generatedProfiles);
        MapProfile(app, generatedProfiles);

        app.MapGet("/", () => "ok");
        app.Run();
    }

    private static StartupOptions ParseStartupOptions(string[] args)
    {
        const int defaultPort = 8080;
        var port = defaultPort;
        int? cores = null;
        var forwardedArgs = new List<string>(args.Length);

        foreach (var arg in args)
        {
            if (TryParseIntArg(arg, "-port", out var parsedPort))
            {
                if (parsedPort is < 1 or > 65535)
                {
                    throw new ArgumentOutOfRangeException(nameof(args), "-port must be between 1 and 65535");
                }

                port = parsedPort;
                continue;
            }

            if (TryParseIntArg(arg, "-cores", out var parsedCores))
            {
                if (parsedCores < 1)
                {
                    throw new ArgumentOutOfRangeException(nameof(args), "-cores must be >= 1");
                }

                cores = parsedCores;
                continue;
            }

            forwardedArgs.Add(arg);
        }

        return new StartupOptions(port, cores, forwardedArgs.ToArray());
    }

    private static bool TryParseIntArg(string arg, string name, out int value)
    {
        var prefix = $"{name}=";
        if (!arg.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
        {
            value = default;
            return false;
        }

        var raw = arg[prefix.Length..];
        if (!int.TryParse(raw, out value))
        {
            throw new ArgumentException($"invalid value for {name}: {raw}", nameof(arg));
        }

        return true;
    }

    private static void ConfigureThreadPoolMaxThreads(int maxWorkerThreads)
    {
        ThreadPool.SetMinThreads(maxWorkerThreads, maxWorkerThreads);
        ThreadPool.SetMaxThreads(maxWorkerThreads, maxWorkerThreads);
    }

    private static void ConfigureSpeedscope(WebApplication app)
    {
        var assembly = typeof(Program).Assembly;
        var provider = new ManifestEmbeddedFileProvider(assembly, "speedscope");

        app.MapMethods("/speedscope/index.html", ["GET", "HEAD"], async context =>
        {
            var file = provider.GetFileInfo("index.html");
            if (!file.Exists)
            {
                context.Response.StatusCode = StatusCodes.Status404NotFound;
                return;
            }

            context.Response.ContentType = "text/html; charset=utf-8";
            await context.Response.SendFileAsync(file, context.RequestAborted);
        });

        app.UseStaticFiles(new StaticFileOptions
        {
            FileProvider = provider,
            RequestPath = "/speedscope",
            ServeUnknownFileTypes = true
        });
    }

    private static void MapEcho(WebApplication app)
    {
        app.MapMethods("/echo", ["GET", "POST"], async context =>
        {
            var message = context.Request.Method == HttpMethods.Get
                ? context.Request.Query["msg"].ToString()
                : await new StreamReader(context.Request.Body).ReadToEndAsync(context.RequestAborted);

            if (string.IsNullOrWhiteSpace(message))
            {
                message = "echo";
            }

            context.Response.ContentType = "text/plain; charset=utf-8";
            await context.Response.WriteAsync(message, context.RequestAborted);
        });
    }

    private static void MapTraceMe(WebApplication app, ConcurrentDictionary<string, string> generatedProfiles)
    {
        app.MapGet("/traceme", async context =>
        {
            var seconds = ParseSeconds(context.Request.Query["seconds"]);

            context.Response.Headers.CacheControl = "no-store";
            context.Response.ContentType = "text/html; charset=utf-8";
            await WriteHtmlChunk(
                context,
                "<!doctype html><meta charset=\"utf-8\"><title>TraceMeV2</title>" +
                "<h2>Trace collecting...</h2><p id=\"status\"></p><pre id=\"log\"></pre>" +
                "<script>" +
                "const s=document.getElementById('status');" +
                "const l=document.getElementById('log');" +
                "function setStatus(t){s.textContent=t;}" +
                "function appendLog(t){if(t){l.textContent+=(t+'\\n');}}" +
                "</script>");

            var traceTask = StartTraceOnDedicatedThread(seconds);
            var targetTime = DateTime.UtcNow.AddSeconds(seconds);

            while (!traceTask.IsCompleted)
            {
                var remain = (int)Math.Max(0, Math.Ceiling((targetTime - DateTime.UtcNow).TotalSeconds));
                var status = remain > 0
                    ? $"collecting... {remain}s left"
                    : "converting trace to speedscope...";

                await WriteHtmlChunk(context, $"<script>setStatus({ToJs(status)});</script>");

                try
                {
                    await Task.Delay(TimeSpan.FromSeconds(1), context.RequestAborted);
                }
                catch (OperationCanceledException)
                {
                    return;
                }
            }

            TraceArtifact artifact;
            try
            {
                artifact = await traceTask;
            }
            catch (Exception ex)
            {
                await WriteHtmlChunk(context, $"<script>setStatus({ToJs($"trace failed: {ex.Message}")});</script>");
                return;
            }

            generatedProfiles[artifact.ProfileFileName] = artifact.ProfileFilePath;
            var redirect = $"/speedscope/index.html#profileURL=/profile/{artifact.ProfileFileName}";
            await WriteHtmlChunk(context, $"<script>setStatus('done, redirecting...');location.href={ToJs(redirect)};</script>");
        });
    }

    private static void MapProfile(WebApplication app, ConcurrentDictionary<string, string> generatedProfiles)
    {
        app.MapGet("/profile/{name}.json", async (HttpContext context, string name) =>
        {
            var safeName = Path.GetFileName(name);
            if (!string.Equals(safeName, name, StringComparison.Ordinal))
            {
                context.Response.StatusCode = StatusCodes.Status400BadRequest;
                return;
            }

            var fileName = $"{safeName}.json";
            if (!generatedProfiles.TryGetValue(fileName, out var profilePath) || !File.Exists(profilePath))
            {
                context.Response.StatusCode = StatusCodes.Status404NotFound;
                return;
            }

            context.Response.ContentType = "application/json; charset=utf-8";
            await context.Response.SendFileAsync(profilePath, context.RequestAborted);
        });
    }

    private static int ParseSeconds(string? secondsRaw)
    {
        const int defaultSeconds = 10;
        if (!int.TryParse(secondsRaw, out var seconds))
        {
            return defaultSeconds;
        }

        return Math.Clamp(seconds, 1, 30);
    }

    private static Task<TraceArtifact> StartTraceOnDedicatedThread(int seconds)
    {
        // 把 task 的完成权交给别的线程
        var completion = new TaskCompletionSource<TraceArtifact>(TaskCreationOptions.RunContinuationsAsynchronously);

        var thread = new Thread(() =>
        {
            try
            {
                completion.SetResult(CollectTrace(seconds));
            }
            catch (Exception ex)
            {
                completion.SetException(ex);
            }
        })
        {
            IsBackground = true,
            Name = "TraceMeCollector"
        };

        thread.Start();
        return completion.Task;
    }

    private static TraceArtifact CollectTrace(int seconds)
    {
        Directory.CreateDirectory(TraceOutputDir);

        var stamp = DateTime.Now.ToString("yyyyMMddHHmmss_fff");
        var processId = Environment.ProcessId;
        var nettracePath = Path.Combine(TraceOutputDir, $"{stamp}.nettrace");
        var etlxPath = $"{nettracePath}.etlx";
        var profileFileName = $"{stamp}.speedscope.json";
        var profilePath = Path.Combine(TraceOutputDir, profileFileName);

        CollectCpuNettrace(processId, TimeSpan.FromSeconds(seconds), nettracePath);
        ConvertNettraceToSpeedscope(nettracePath, etlxPath, profilePath);
        TryDeleteFile(nettracePath);
        TryDeleteFile(etlxPath);

        return new TraceArtifact(profileFileName, profilePath);
    }

    private static void CollectCpuNettrace(int processId, TimeSpan duration, string outFile)
    {
        var providers = new List<EventPipeProvider>
        {
            new("Microsoft-Windows-DotNETRuntime", EventLevel.Informational, (long)ClrTraceEventParser.Keywords.Default),
            new("Microsoft-DotNETCore-SampleProfiler", EventLevel.Informational, 0)
        };

        var client = new DiagnosticsClient(processId);
        using var session = client.StartEventPipeSession(providers, requestRundown: true);
        using var stream = new FileStream(outFile, FileMode.Create, FileAccess.Write, FileShare.Read);

        var copyTask = session.EventStream.CopyToAsync(stream);
        Thread.Sleep(duration);
        session.Stop();

        try
        {
            copyTask.GetAwaiter().GetResult();
        }
        catch
        {
            // Stop() may race with the read loop; this is expected on shutdown.
        }
    }

    private static void ConvertNettraceToSpeedscope(string nettracePath, string etlxPath, string speedscopePath)
    {
        var options = new TraceLogOptions
        {
            ConversionLog = TextWriter.Null,
            ShouldResolveSymbols = _ => false
        };

        using var traceLog = new TraceLog(TraceLog.CreateFromEventPipeDataFile(nettracePath, etlxPath, options));
        var symbolReader = new SymbolReader(TextWriter.Null, SymbolPath.MicrosoftSymbolServerPath, null)
        {
            SecurityCheck = _ => true
        };

        var stackSource = new MutableTraceEventStackSource(traceLog);
        var computer = new SampleProfilerThreadTimeComputer(traceLog, symbolReader);
        // EventPipe traces from DiagnosticsClient are already scoped to the attached process.
        // Filtering by Environment.ProcessId can drop all samples when PID namespaces/remapping are involved.
        computer.GenerateThreadTimeStacks(stackSource, traceLog.Events);

        SpeedScopeStackSourceWriter.WriteStackViewAsJson(stackSource, speedscopePath);
    }

    private static void TryDeleteFile(string path)
    {
        if (!File.Exists(path))
        {
            return;
        }

        try
        {
            File.Delete(path);
        }
        catch
        {
            // Keep temporary files if cleanup fails.
        }
    }

    private static async Task WriteHtmlChunk(HttpContext context, string htmlChunk)
    {
        await context.Response.WriteAsync(htmlChunk, context.RequestAborted);
        await context.Response.Body.FlushAsync(context.RequestAborted);
    }

    private static string ToJs(string value)
    {
        return $"\"{JavaScriptEncoder.Default.Encode(value)}\"";
    }

    private readonly record struct TraceArtifact(string ProfileFileName, string ProfileFilePath);
    private readonly record struct StartupOptions(int Port, int? Cores, string[] ForwardedArgs);
}
