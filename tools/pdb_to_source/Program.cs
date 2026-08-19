namespace PdbToSource;

internal static class Program
{
    private static int Main(string[] args)
    {
        string? pdbPath = null;
        string? targetDir = null;
        bool skipObj = false;

        foreach (var arg in args)
        {
            if (TryGetFlagValue(arg, "-pdb=", out var pdb))
            {
                pdbPath = pdb;
            }
            else if (TryGetFlagValue(arg, "-target.dir=", out var dir))
            {
                targetDir = dir;
            }
            else if (arg == "-skip.obj")
            {
                skipObj = true;
            }
        }

        if (string.IsNullOrEmpty(pdbPath) || string.IsNullOrEmpty(targetDir))
        {
            Console.WriteLine("Usage:");
            Console.WriteLine("  pdb_to_source -pdb=xx.pdb -target.dir=./xxx/ [-skip.obj]");
            return 1;
        }

        if (!File.Exists(pdbPath))
        {
            Console.Error.WriteLine($"pdb file not found: {pdbPath}");
            return 1;
        }

        Directory.CreateDirectory(targetDir);

        var result = EmbeddedSourceReader.Extract(pdbPath, targetDir, skipObj);

        Console.WriteLine($"documents: {result.TotalDocuments}");
        Console.WriteLine($"extracted: {result.ExtractedCount}");
        Console.WriteLine($"skipped (no embedded source): {result.SkippedCount}");
        if (skipObj)
        {
            Console.WriteLine($"skipped (obj dir): {result.SkippedObjCount}");
        }

        return 0;
    }

    private static bool TryGetFlagValue(string arg, string prefix, out string value)
    {
        if (arg.StartsWith(prefix, StringComparison.Ordinal))
        {
            value = arg[prefix.Length..];
            return true;
        }

        value = string.Empty;
        return false;
    }
}
