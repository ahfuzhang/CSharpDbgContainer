using System.Collections.Concurrent;
using System.Diagnostics;
using System.Text;
using System.Text.Encodings.Web;
using System.Text.RegularExpressions;
using System.Threading.Channels;
using Microsoft.AspNetCore.Server.Kestrel.Core;
using Microsoft.Extensions.FileProviders;

public class Program
{
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
        var traceFiles = new ConcurrentDictionary<string, string>(StringComparer.Ordinal);

        ConfigureSpeedscope(app);
        MapEcho(app);
        MapTraceMe(app, traceFiles);
        MapProfile(app, traceFiles);

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
        // ThreadPool.GetMinThreads(out var minWorkerThreads, out var minIoThreads);
        // if (maxWorkerThreads < minWorkerThreads)
        // {
        //     if (!ThreadPool.SetMinThreads(maxWorkerThreads, minIoThreads))
        //     {
        //         throw new InvalidOperationException($"failed to set ThreadPool min worker threads to {maxWorkerThreads}");
        //     }
        // }

        // ThreadPool.GetMaxThreads(out _, out var maxIoThreads);
        // if (!ThreadPool.SetMaxThreads(maxWorkerThreads, maxIoThreads))
        // {
        //     throw new InvalidOperationException($"failed to set ThreadPool max worker threads to {maxWorkerThreads}");
        // }
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

    const string defaultPath = "/tmp";

    private static void MapTraceMe(WebApplication app, ConcurrentDictionary<string, string> traceFiles)
    {
        app.MapGet("/traceme", async context =>
        {
            var seconds = ParseSeconds(context.Request.Query["seconds"]);
            var traceName = DateTime.Now.ToString("yyyyMMddHHmmss_fff");
            var outputTracePath = $"{defaultPath}/{traceName}";
            var speedscopePath = $"{defaultPath}/{traceName}.speedscope.json";
            traceFiles[traceName] = speedscopePath;

            context.Response.Headers.CacheControl = "no-store";
            context.Response.ContentType = "text/html; charset=utf-8";

            await WriteHtmlChunk(
                context,
                "<!doctype html><meta charset=\"utf-8\"><title>TraceMe</title>" +
                "<h2>Trace collecting...</h2><p id=\"status\"></p><pre id=\"log\"></pre>" +
                "<script>" +
                "const s=document.getElementById('status');" +
                "const l=document.getElementById('log');" +
                "function setStatus(t){s.textContent=t;}" +
                "function appendLog(t){if(t){l.textContent+=(t+'\\n');}}" +
                "</script>");

            var commandArgs = BuildCollectCommandArgs(seconds, Environment.ProcessId, outputTracePath);

            TraceCollectResult collectResult;
            try
            {
                collectResult = await RunTraceCollect(context, commandArgs, seconds, true);
            }
            catch (OperationCanceledException)
            {
                traceFiles.TryRemove(traceName, out _);
                return;
            }

            if (!collectResult.Started)
            {
                traceFiles.TryRemove(traceName, out _);
                await WriteHtmlChunk(context, $"<script>setStatus({ToJs($"failed: {collectResult.StartError}")});</script>");
                return;
            }
            if (collectResult.ExitCode != 0){
                await WriteHtmlChunk(context, $"<script>setStatus('exit code:{collectResult.ExitCode}');</script>");
                return;
            }
            if (!File.Exists(speedscopePath)){
                await WriteHtmlChunk(context, $"<script>setStatus('not exists:{speedscopePath}');</script>");
                return;
            }
            if (collectResult.ExitCode == 0 && File.Exists(speedscopePath))
            {
                var redirect = $"/speedscope/index.html#profileURL=/profile/{traceName}.speedscope.json";
                await WriteHtmlChunk(context, $"<script>setStatus('done, redirecting...');location.href={ToJs(redirect)};</script>");
                return;
            }

            traceFiles.TryRemove(traceName, out _);
            await WriteHtmlChunk(
                context,
                $"<script>setStatus({ToJs($"trace failed, exit code: {collectResult.ExitCode}")});</script>");
            return;
        });
    }

    private static void MapProfile(WebApplication app, ConcurrentDictionary<string, string> traceFiles)
    {

        app.MapGet("/profile/{name}", async (HttpContext context, string name) =>
        {
            context.Response.ContentType = "application/json; charset=utf-8";
            await context.Response.SendFileAsync($"{defaultPath}/{name}", context.RequestAborted);
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

    private static string BuildCollectCommandArgs(int seconds, int processId, string outputTracePath)
    {
        return
            $"collect --profile dotnet-sampled-thread-time --duration 00:00:{seconds:00} --format Speedscope -p {processId} -o {outputTracePath}";
    }

    private static async Task<TraceCollectResult> RunTraceCollect(
        HttpContext context,
        string commandArgs,
        int seconds,
        bool logCommandLine)
    {
        var responseWriteLock = new SemaphoreSlim(1, 1);

        async Task WriteScriptAsync(string script)
        {
            await responseWriteLock.WaitAsync(context.RequestAborted);
            try
            {
                await WriteHtmlChunk(context, script);
            }
            finally
            {
                responseWriteLock.Release();
            }
        }

        if (logCommandLine)
        {
            await WriteScriptAsync($"<script>appendLog({ToJs($"$ dotnet-trace {commandArgs}")});</script>");
        }

        Process? process;
        try
        {
            process = Process.Start(new ProcessStartInfo("dotnet-trace", commandArgs)
            {
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                UseShellExecute = false,
                CreateNoWindow = true
            });
        }
        catch (Exception ex)
        {
            return TraceCollectResult.StartFailure(ex.Message);
        }

        if (process is null)
        {
            return TraceCollectResult.StartFailure("cannot start dotnet-trace");
        }

        var stdoutBuilder = new StringBuilder();
        var stderrBuilder = new StringBuilder();
        var logChannel = Channel.CreateUnbounded<string>(new UnboundedChannelOptions
        {
            SingleReader = true,
            SingleWriter = false
        });

        static void AppendLine(StringBuilder builder, string line)
        {
            if (builder.Length > 0)
            {
                builder.AppendLine();
            }

            builder.Append(line);
        }

        async Task ReadStreamToChannel(StreamReader reader, StringBuilder output, string label)
        {
            while (true)
            {
                var line = await reader.ReadLineAsync();
                if (line is null)
                {
                    break;
                }

                AppendLine(output, line);
                await logChannel.Writer.WriteAsync($"[{label}] {line}", context.RequestAborted);
            }
        }

        var stdoutReaderTask = ReadStreamToChannel(process.StandardOutput, stdoutBuilder, "stdout");
        var stderrReaderTask = ReadStreamToChannel(process.StandardError, stderrBuilder, "stderr");
        var readStreamsTask = Task.WhenAll(stdoutReaderTask, stderrReaderTask);
        _ = readStreamsTask.ContinueWith(_ =>
        {
            logChannel.Writer.TryComplete();
        }, TaskScheduler.Default);

        var logForwardTask = Task.Run(async () =>
        {
            await foreach (var line in logChannel.Reader.ReadAllAsync(context.RequestAborted))
            {
                await WriteScriptAsync($"<script>appendLog({ToJs(line)});</script>");
            }
        });

        try
        {
            var target = DateTime.UtcNow.AddSeconds(seconds);
            while (!process.HasExited)
            {
                var remain = (int)Math.Max(0, Math.Ceiling((target - DateTime.UtcNow).TotalSeconds));
                await WriteScriptAsync($"<script>setStatus({ToJs($"collecting... {remain}s left")});</script>");
                if (remain == 0)
                {
                    break;
                }

                await Task.Delay(TimeSpan.FromSeconds(1), context.RequestAborted);
            }

            await process.WaitForExitAsync(context.RequestAborted);
            await readStreamsTask;
            await logForwardTask;
        }
        catch (OperationCanceledException)
        {
            process.Kill(true);
            logChannel.Writer.TryComplete();

            try
            {
                await readStreamsTask;
                await logForwardTask;
            }
            catch
            {
                // Request is canceled, best-effort cleanup only.
            }

            throw;
        }

        var stdout = stdoutBuilder.ToString();
        var stderr = stderrBuilder.ToString();
        return new TraceCollectResult(process.ExitCode, stdout, stderr, null);
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

    private readonly record struct TraceCollectResult(int ExitCode, string Stdout, string Stderr, string? StartError)
    {
        public bool Started => StartError is null;

        public static TraceCollectResult StartFailure(string message)
        {
            return new TraceCollectResult(-1, string.Empty, string.Empty, message);
        }
    }

    private readonly record struct StartupOptions(int Port, int? Cores, string[] ForwardedArgs);
}
