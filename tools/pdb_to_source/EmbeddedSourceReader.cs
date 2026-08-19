using System.IO.Compression;
using System.Reflection.Metadata;
using System.Text;

namespace PdbToSource;

internal readonly record struct ExtractResult(int TotalDocuments, int ExtractedCount, int SkippedCount, int SkippedObjCount);

internal static class EmbeddedSourceReader
{
    private static readonly Guid EmbeddedSourceGuid = new("0E8A571B-6926-466E-B4AD-8AB04611F5FE");

    public static ExtractResult Extract(string pdbPath, string targetDir, bool skipObj = false)
    {
        using var fs = File.OpenRead(pdbPath);
        using var provider = MetadataReaderProvider.FromPortablePdbStream(fs);
        var reader = provider.GetMetadataReader();

        int total = 0;
        int extracted = 0;
        int skippedObj = 0;

        foreach (var docHandle in reader.Documents)
        {
            total++;
            var doc = reader.GetDocument(docHandle);
            var name = reader.GetString(doc.Name);

            var relativePath = name.Replace('\\', '/').TrimStart('/');

            if (skipObj && IsUnderObjDirectory(relativePath))
            {
                skippedObj++;
                continue;
            }

            var source = TryReadEmbeddedSource(reader, docHandle);
            if (source == null)
            {
                continue;
            }

            var fullPath = Path.Combine(targetDir, relativePath);
            Directory.CreateDirectory(Path.GetDirectoryName(fullPath)!);
            File.WriteAllText(fullPath, source);
            extracted++;
        }

        return new ExtractResult(total, extracted, total - extracted - skippedObj, skippedObj);
    }

    private static bool IsUnderObjDirectory(string relativePath)
    {
        foreach (var segment in relativePath.Split('/'))
        {
            if (segment.Equals("obj", StringComparison.Ordinal))
            {
                return true;
            }
        }

        return false;
    }

    private static string? TryReadEmbeddedSource(MetadataReader reader, DocumentHandle docHandle)
    {
        foreach (var cdiHandle in reader.GetCustomDebugInformation(docHandle))
        {
            var cdi = reader.GetCustomDebugInformation(cdiHandle);
            var kind = reader.GetGuid(cdi.Kind);
            if (kind != EmbeddedSourceGuid)
            {
                continue;
            }

            var blob = reader.GetBlobReader(cdi.Value);
            var uncompressedSize = blob.ReadInt32();

            if (uncompressedSize == 0)
            {
                var raw = blob.ReadBytes(blob.RemainingBytes);
                return Encoding.UTF8.GetString(raw);
            }

            var compressed = blob.ReadBytes(blob.RemainingBytes);
            using var compressedStream = new MemoryStream(compressed.ToArray());
            using var deflate = new DeflateStream(compressedStream, CompressionMode.Decompress);
            using var output = new MemoryStream(uncompressedSize);
            deflate.CopyTo(output);
            return Encoding.UTF8.GetString(output.GetBuffer(), 0, (int)output.Length);
        }

        return null;
    }
}
