package cli

import "testing"

func TestParseGlobalOptionsConsumesConfigPath_BitsUT(t *testing.T) {
	opts, args, err := parseGlobalOptions([]string{"--config", "/tmp/cohert.yaml", "config"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.ConfigPath != "/tmp/cohert.yaml" {
		t.Fatalf("config path = %q, want /tmp/cohert.yaml", opts.ConfigPath)
	}
	if len(args) != 1 || args[0] != "config" {
		t.Fatalf("args = %#v, want [config]", args)
	}
}

func TestParseGlobalOptionsConsumesConfigEquals_BitsUT(t *testing.T) {
	opts, args, err := parseGlobalOptions([]string{"--config=/tmp/cohert.yaml", "tools"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.ConfigPath != "/tmp/cohert.yaml" {
		t.Fatalf("config path = %q, want /tmp/cohert.yaml", opts.ConfigPath)
	}
	if len(args) != 1 || args[0] != "tools" {
		t.Fatalf("args = %#v, want [tools]", args)
	}
}

func TestParseGlobalOptionsStopsAtCommand_BitsUT(t *testing.T) {
	opts, args, err := parseGlobalOptions([]string{"ask", "--config", "not-global", "task"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.ConfigPath != "" {
		t.Fatalf("config path = %q, want empty", opts.ConfigPath)
	}
	if len(args) != 4 || args[0] != "ask" || args[1] != "--config" {
		t.Fatalf("args = %#v, want original command args", args)
	}
}
