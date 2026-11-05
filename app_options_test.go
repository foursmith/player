package main

import "testing"

func TestRecognitionTestFlagAndEnvironmentLeaveFileArgumentsIntact(t *testing.T) {
	t.Setenv("FSVR_RECOGNITION_TEST", "")
	options, err := parseApplicationOptions([]string{"--recognition-test", "--data-dir", "/tmp/foursmith-test", "--", "a.mp4", "b.mp4"})
	if err != nil || !options.recognitionTest || options.dataDir != "/tmp/foursmith-test" || len(options.paths) != 2 || options.paths[0] != "a.mp4" {
		t.Fatalf("flag parsing: %+v %v", options, err)
	}
	t.Setenv("FSVR_RECOGNITION_TEST", "1")
	options, err = parseApplicationOptions(nil)
	if err != nil || !options.recognitionTest {
		t.Fatalf("environment switch: %+v %v", options, err)
	}
	options, err = parseApplicationOptions([]string{"--recognition-test=false", "a.mp4"})
	if err != nil || options.recognitionTest || len(options.paths) != 1 {
		t.Fatalf("explicit flag override: %+v %v", options, err)
	}
}
