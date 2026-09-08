using System.Reflection;

namespace DllVersion;

internal static class Program
{
    private static int Main(string[] args)
    {
        if (args.Length < 1 || string.IsNullOrWhiteSpace(args[0]))
        {
            Console.WriteLine("Usage:");
            Console.WriteLine("  dll_version <path-to-dll>");
            return 1;
        }

        var dllPath = args[0];

        if (!File.Exists(dllPath))
        {
            Console.Error.WriteLine($"file not found: {dllPath}");
            return 1;
        }

        try
        {
            var name = AssemblyName.GetAssemblyName(dllPath);
            Console.WriteLine(name.Version);
            return 0;
        }
        catch (BadImageFormatException ex)
        {
            Console.Error.WriteLine($"not a valid .NET assembly: {dllPath} ({ex.Message})");
            return 1;
        }
    }
}
