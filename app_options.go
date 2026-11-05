package main

import (
	"flag"
	"os"
)

type applicationOptions struct {
	dataDir         string
	recognitionTest bool
	paths           []string
}

func parseApplicationOptions(args []string) (applicationOptions, error) {
	var options applicationOptions
	flags := flag.NewFlagSet("foursmith-player", flag.ContinueOnError)
	flags.StringVar(&options.dataDir, "data-dir", "", "application data directory")
	flags.BoolVar(&options.recognitionTest, "recognition-test", os.Getenv("FSVR_RECOGNITION_TEST") == "1", "trace video type recognition and ignore saved video types")
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	options.paths = flags.Args()
	return options, nil
}
