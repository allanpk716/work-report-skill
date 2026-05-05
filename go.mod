module wr

go 1.24.11

require (
	github.com/allanpk716/agent-cli-sdk v0.0.0
	github.com/robfig/cron/v3 v3.0.1
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.9
)

require github.com/inconshreveable/mousetrap v1.1.0 // indirect

replace github.com/allanpk716/agent-cli-sdk => ../../../../../../../WorkSpace/agent/cli--agent-things/ai-agent-cli-rules/sdks/go
