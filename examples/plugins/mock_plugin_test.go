package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestMain(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	main()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)

	if !strings.Contains(buf.String(), "Plugin execution: success") {
		t.Errorf("expected plugin success message, got %s", buf.String())
	}
}
