package main

import (
	"io"
	"os"
	"testing"

	"f95rpc/internal/version"
)

func TestVersionCommandUsesInjectedTag(t *testing.T) {
	oldVersion := version.Version
	version.Version = "v1.2.3-rc.1"
	t.Cleanup(func() { version.Version = oldVersion })
	if got := runVersionCommand(t, "version"); got != "f95rpc v1.2.3-rc.1\n" {
		t.Fatalf("version output must preserve the full build tag without a doubled v, got %q", got)
	}
}

func TestVersionCommandDefaultsToDev(t *testing.T) {
	for _, command := range []string{"version", "--version", "-v"} {
		t.Run(command, func(t *testing.T) {
			if got := runVersionCommand(t, command); got != "f95rpc dev\n" {
				t.Fatalf("unversioned builds should report dev, got %q", got)
			}
		})
	}
}

func runVersionCommand(t *testing.T, command string) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldArgs, oldStdout := os.Args, os.Stdout
	t.Cleanup(func() {
		os.Args, os.Stdout = oldArgs, oldStdout
		_ = reader.Close()
		_ = writer.Close()
	})
	os.Args = []string{"f95rpc", command}
	os.Stdout = writer
	main()
	_ = writer.Close()
	os.Stdout = oldStdout
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}
