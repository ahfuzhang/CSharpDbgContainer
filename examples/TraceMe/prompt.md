
# 目标
实现一个 C# 的例子程序，当请求 http 端口 /traceme?seconds=10 时，开启 dotnet-trace 对自身进程 trace 信息采集，把采集到的 json 存储到 /tmp/ 中。然后 embed 整个 speedscope 目录，跳转到 /speedscope/index.html#profileURL=/profile/xxx.json。最终，仅通过浏览器就实现了快速查看一个进程的 trace 信息。

# 步骤

* 创建 TraceMe.csproj 文件
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
    - 请求这个路径时，调用命令行 `dotnet-trace collect --profile cpu-sampling --duration 00:00:$seconds --format Speedscope -p $pid -o /tmp/${YYYYMMDDHHMiSS.mmm}`
    - $pid 是当前进程的 pid
    - $seconds 是 querystring 中 seconds 参数的值。默认值为 10， 最小值为 1，最大值为 30。
    - ${YYYYMMDDHHMiSS.mmm} 是当前时间
    - 保持长连接，在 web ui 中显示 trace 的倒计时信息
    - dotnet-trace 运行结束后，页面跳转到 `/speedscope/index.html#profileURL=/profile/xxx.json`
  - 路径 `/speedscope/`: 把 embed 的 assets 输出到浏览器端.
    - 参考一下代码:

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

  - 路径 `/profile/${name}.json`: 访问由 dotnet-trace 生成到 `/tmp/${YYYYMMDDHHMiSS.mmm}` 中的文件。最终，可以在浏览器中看到火焰图。

