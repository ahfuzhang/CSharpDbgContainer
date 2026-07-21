package debugadmin

type Options struct {
	AdminPort         int
	StartupParams     []string
	LogPushURL        string
	LogStdoutOutput   bool
	CoreDumpUnlimited bool
	AutoRestart       bool
	WithGDB           bool
}
