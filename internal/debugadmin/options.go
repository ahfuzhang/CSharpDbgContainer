package debugadmin

type Options struct {
	AdminPort         int
	Startup           string
	LogPushURL        string
	LogStdoutOutput   bool
	CoreDumpUnlimited bool
	AutoRestart       bool
}
