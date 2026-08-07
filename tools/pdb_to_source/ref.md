
```
dotnet run --project PdbSourceExtractor \
  -- ./MyService.pdb ./extracted-source
```

这里提到的工具要怎么写？请给我一段源码的例子
-------------------------------------------

可以，而且这个工具实际上并不复杂。

不过我要先说明一个问题：

我上一个回答有一点需要修正。

System.Reflection.Metadata 并没有直接提供一个 ExtractAllSources() API。
它只能读取 PDB 中的 Metadata，需要你自己遍历 EmbeddedSource CustomDebugInformation。

也就是说，我们需要：

Portable PDB
        │
MetadataReader
        │
Document
        │
CustomDebugInformation
        │
EmbeddedSource
        │
解压(LZ4 或原始数据)
        │
写回 .cs

⸻

整体结构

建议建一个控制台程序：

PdbSourceExtractor/
    Program.cs
    EmbeddedSourceReader.cs
    SourceLinkReader.cs（以后可以加）

NuGet：

dotnet add package System.Reflection.Metadata
dotnet add package K4os.Compression.LZ4

其中：

* System.Reflection.Metadata
* K4os.Compression.LZ4（Portable PDB EmbeddedSource 用）

⸻

Program.cs

例如：

using System.Reflection.Metadata;
using System.Reflection.PortableExecutable;
if (args.Length != 2)
{
    Console.WriteLine("Usage:");
    Console.WriteLine("PdbSourceExtractor xxx.pdb output-dir");
    return;
}
EmbeddedSourceReader.Extract(
    args[0],
    args[1]);

⸻

打开 Portable PDB

using var fs = File.OpenRead(pdbPath);
using var provider =
    MetadataReaderProvider.FromPortablePdbStream(fs);
MetadataReader reader =
    provider.GetMetadataReader();

得到：

reader.Documents
reader.MethodDebugInformation
reader.CustomDebugInformation

⸻

遍历所有 Document

foreach (DocumentHandle handle in reader.Documents)
{
    Document doc = reader.GetDocument(handle);
    string name =
        reader.GetString(doc.Name);
    Console.WriteLine(name);
}

输出：

/src/UserService.cs
/src/Login.cs
/src/Main.cs

这一步只能拿到文件名。

⸻

找到 EmbeddedSource

关键就在这里。

Portable PDB 里面：

Document
    ↓
CustomDebugInformation
            ↓
EmbeddedSource

可以这样找：

Guid EmbeddedSourceGuid =
    new Guid("0E8A571B-6926-466E-B4AD-8AB04611F5FE");
foreach (var cdiHandle in reader.GetCustomDebugInformation(handle))
{
    var cdi =
        reader.GetCustomDebugInformation(cdiHandle);
    Guid kind =
        reader.GetGuid(cdi.Kind);
    if (kind == EmbeddedSourceGuid)
    {
        ...
    }
}

这个 Guid 是 Roslyn 定义的 EmbeddedSource GUID。

⸻

读取 Blob

BlobReader blob =
    reader.GetBlobReader(cdi.Value);

Blob 的格式：

int32 uncompressedSize
如果：
    uncompressedSize == 0
        后面就是源码
否则
    后面就是LZ4压缩数据

先读：

int size =
    blob.ReadInt32();

⸻

没压缩

byte[] data =
    blob.ReadBytes(blob.RemainingBytes);
string source =
    Encoding.UTF8.GetString(data);

⸻

LZ4

如果：

size > 0

说明：

后面
↓
LZ4 Frame

可以：

byte[] compressed =
    blob.ReadBytes(blob.RemainingBytes);
byte[] output =
    new byte[size];
LZ4Pickler
    .Unpickle(
        compressed,
        output);
string source =
    Encoding.UTF8.GetString(output);

然后：

File.WriteAllText(
    outputPath,
    source);

即可恢复源码。

⸻

保持目录结构

例如：

PDB里面：

/src/server/User.cs

输出：

string full =
    Path.Combine(
        outputDir,
        name.TrimStart('/'));
Directory.CreateDirectory(
    Path.GetDirectoryName(full)!);
File.WriteAllText(full, source);

最后得到：

output/
    src/
        server/
            User.cs

⸻

实际效果

例如：

Program.pdb

里面：

Program.cs
Main.cs
User.cs

运行：

PdbSourceExtractor Program.pdb out

输出：

out/
    src/
        Program.cs
        Main.cs
        User.cs

然后：

reportgenerator \
    -reports:coverage.xml \
    -targetdir:report \
    -sourcedirs:out/src

HTML 就可以显示源码了。

⸻

我建议做得更完整一点

如果你准备把它用于 dotnet-coverage + ReportGenerator 的生产流程，我建议直接做成一个 500 行左右的小工具，支持下面四种能力：

1. EmbeddedSource（优先）——从 PDB 中恢复源码。
2. SourceLink——如果没有嵌入源码，则根据 Source Link 自动下载对应版本的源码。
3. 普通 PDB——如果既没有 EmbeddedSource 也没有 SourceLink，则输出 PDB 中记录的源文件路径，方便从构建产物中定位源码。
4. 自动重写 Cobertura XML——将 filename="/home/build/.../Foo.cs" 修改为提取后的临时目录路径，这样无需保持原始目录结构即可直接交给 ReportGenerator。

这个版本大约 500～800 行 C#，作为独立工具已经足够实用。对于你目前希望在服务器上生成覆盖率 HTML 报告的场景，这也是我最推荐的实现方式。
