# 目标

生成一个 c# 命令行工具，用于查看一个 dll 的版本号

# 参考

```c#
using System.Reflection;

var name = AssemblyName.GetAssemblyName(args[0]);
Console.WriteLine(name.Version);
```

# 输出

* csproj 文件
* .gitignore 文件
* cs 文件
* Makefile 文件

