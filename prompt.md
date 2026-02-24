
# 目标

这是一个 golang 实现的辅助进行 C# 程序调试的管理程序。
这个程序叫做 DebugAdmin.
DebugAdmin 做了如下功能：
1. 首先 DebugAdmin 在镜像 csharp-dbg-all-in-one 中启动；
2. 通过配置文件 init.config.yaml 来指定需要调试的 c# dll 文件；
3. DebugAdmin 创建进程 `dotnet xxx.dll`，并且拿到 pid
4. 启动 web 服务，以 web ui 的方式，为开发者提供方便的在线调试能力

# 约束

* 除了 main.go 以外，golang 代码放到 internal/ 目录下
* 每个函数尽量不超过 100 行
* 尽量每个文件一个类型

# 命令行参数

* `-admin.port=8089`
  - http 服务监听的端口
* `-startup=xxx.dll`
  - 指定服务启动后，需要创建的调试进程
  - 如果以 .dll 结尾，则创建进程 `dotnet xxx.dll`
  - 如果不是 dll，直接执行 `xxx`
  - 指定后收集进程的 pid，这是一个关键的全局信息

# 功能
* 把项目的 `./build/speedscope/` 目录下的所有文件， embed 到二进制程序中
  - 当 http 服务器中访问 `/speedscope/` 路径时，可以浏览到目录下的文件

# http 接口

* `/log`
  - 访问此路径，以 http chunked 的方式，把日志以文本格式输出到浏览器端
  - 就算没有用户访问这个接口，也需要把被调试进程的日志再输出一份到 stdout
* `/trace?seconds=10`
  - 访问此路径，使用 dotnet trace 采集
  - 启动进程 `dotnet-trace collect --profile cpu-sampling --duration 00:00:$seconds --format Speedscope -p $pid -o /tmp/${YYYYMMDDHHMiSS.mmm}`
  - 其中：
    - $seconds 是 url 中的 seconds 参数的值，最小为 1，最大为 30。
    - $pid 是被调试进程的 pid
    - ${YYYYMMDDHHMiSS.mmm} 是一个由详细当前时间构成的字符串，并且需要把这个时间记录到一个全局数组中
  - 在采集期间，通过 http chunk 的方式保持长链接，在页面上显示采集 n 秒的倒计时信息
  - trace 信息采集完成后，跳转到 `/speedscope/index.html#profileURL=/profile/${YYYYMMDDHHMiSS.mmm}.speedscope.json`
* `/profile/${YYYYMMDDHHMiSS.mmm}.speedscope.json`
  - 当访问如上路径时，从每次调用 `/trace` 接口产生的时间值的数组中搜索，是否存在相匹配的时间值。
  - 如果找到对应的时间值，输出文件 `/tmp/${YYYYMMDDHHMiSS.mmm}.speedscope.json` 的内容。
  - 最终，在 `/speedscope/index.html#profileURL=/profile/${YYYYMMDDHHMiSS.mmm}.speedscope.json` 路径中能够看见火焰图
  
