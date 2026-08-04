package debugadmin

type Options struct {
	AdminPort         int
	StartupParams     []string
	LogPushURL        string
	LogStdoutOutput   bool
	CoreDumpUnlimited bool
	AutoRestart       bool
	WithGDB           bool
	WithCoverage      bool
	CoverageName      string // 用于指定覆盖率文件名
}

// GlobalOptions 保存命令行解析得到的配置信息。
// 程序启动时由 Run() 赋值一次，此后任意位置都可以直接读取，无需再逐层传递。
var GlobalOptions *Options
